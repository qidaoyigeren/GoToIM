package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// PushClient calls goim Logic's HTTP push API.
type PushClient struct {
	logicAddr   string
	client      *http.Client
	maxRetries  int
	backoffBase time.Duration
	breaker     *CircuitBreaker
}

// PushClientConfig controls HTTP timeout and retry behavior.
type PushClientConfig struct {
	Timeout                 time.Duration
	MaxRetries              int
	BackoffBase             time.Duration
	CircuitFailureThreshold int
	CircuitOpenInterval     time.Duration
}

// NewPushClient creates a new PushClient targeting the given Logic HTTP address.
func NewPushClient(logicAddr string) *PushClient {
	return NewPushClientWithConfig(logicAddr, PushClientConfig{
		Timeout:                 3 * time.Second,
		MaxRetries:              2,
		BackoffBase:             100 * time.Millisecond,
		CircuitFailureThreshold: 5,
		CircuitOpenInterval:     5 * time.Second,
	})
}

// NewPushClientWithConfig creates a PushClient with explicit reliability settings.
func NewPushClientWithConfig(logicAddr string, cfg PushClientConfig) *PushClient {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 100 * time.Millisecond
	}
	if cfg.CircuitFailureThreshold <= 0 {
		cfg.CircuitFailureThreshold = 5
	}
	if cfg.CircuitOpenInterval <= 0 {
		cfg.CircuitOpenInterval = 5 * time.Second
	}
	return &PushClient{
		logicAddr:   logicAddr,
		maxRetries:  cfg.MaxRetries,
		backoffBase: cfg.BackoffBase,
		breaker:     NewCircuitBreaker(cfg.CircuitFailureThreshold, cfg.CircuitOpenInterval),
		client: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 32,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// PushError classifies delivery failures for retry and DLQ decisions.
type PushError struct {
	Type       string
	Class      string
	StatusCode int
	Message    string
	Retryable  bool
	Err        error
}

// DeliveryResult is the structured delivery response returned by Logic and persisted in attempts.
type DeliveryResult struct {
	MsgID        string  `json:"msg_id"`
	Path         string  `json:"path"`
	TargetNode   string  `json:"target_node,omitempty"`
	ErrorCode    string  `json:"error_code,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	LatencyMs    float64 `json:"latency_ms"`
	AttemptNo    int64   `json:"attempt_no"`
}

// CircuitBreaker is a lightweight closed/open/half-open breaker for Logic calls.
type CircuitBreaker struct {
	mu               sync.Mutex
	failureThreshold int
	openInterval     time.Duration
	state            string
	failures         int
	openedAt         time.Time
}

func NewCircuitBreaker(threshold int, openInterval time.Duration) *CircuitBreaker {
	return &CircuitBreaker{failureThreshold: threshold, openInterval: openInterval, state: "closed"}
}

func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != "open" {
		return true
	}
	if time.Since(b.openedAt) >= b.openInterval {
		b.state = "half_open"
		return true
	}
	return false
}

func (b *CircuitBreaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.state = "closed"
}

func (b *CircuitBreaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= b.failureThreshold || b.state == "half_open" {
		b.state = "open"
		b.openedAt = time.Now()
	}
}

func (b *CircuitBreaker) State() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (e *PushError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Type + ": " + e.Message
	}
	return e.Type
}

func (e *PushError) Unwrap() error { return e.Err }

func IsRetryablePushError(err error) bool {
	var pe *PushError
	if errors.As(err, &pe) {
		return pe.Retryable
	}
	return false
}

func IsPermanentPushError(err error) bool {
	var pe *PushError
	if errors.As(err, &pe) {
		return pe.Class == "permanent"
	}
	return false
}

// PushToUser sends a message to a specific user by their keys (device-level).
func (c *PushClient) PushToUser(op int32, keys []string, msg []byte) ([]string, error) {
	results, err := c.PushToUserDetailedContext(context.Background(), op, keys, msg)
	return deliveryMsgIDs(results), err
}

// PushToUserContext sends a message to a specific user by their keys.
func (c *PushClient) PushToUserContext(ctx context.Context, op int32, keys []string, msg []byte) ([]string, error) {
	results, err := c.PushToUserDetailedContext(ctx, op, keys, msg)
	return deliveryMsgIDs(results), err
}

func (c *PushClient) PushToUserDetailedContext(ctx context.Context, op int32, keys []string, msg []byte) ([]DeliveryResult, error) {
	params := url.Values{}
	params.Set("operation", strconv.Itoa(int(op)))
	for _, k := range keys {
		params.Add("keys", k)
	}
	return c.doPostDetailedContext(ctx, "/goim/push/keys", params, msg)
}

// PushToUsers sends a message to multiple users by their mids (user IDs).
func (c *PushClient) PushToUsers(op int32, mids []int64, msg []byte) ([]string, error) {
	results, err := c.PushToUsersDetailedContext(context.Background(), op, mids, msg)
	return deliveryMsgIDs(results), err
}

// PushToUsersContext sends a message to multiple users by their mids.
func (c *PushClient) PushToUsersContext(ctx context.Context, op int32, mids []int64, msg []byte) ([]string, error) {
	results, err := c.PushToUsersDetailedContext(ctx, op, mids, msg)
	return deliveryMsgIDs(results), err
}

func (c *PushClient) PushToUsersDetailedContext(ctx context.Context, op int32, mids []int64, msg []byte) ([]DeliveryResult, error) {
	params := url.Values{}
	params.Set("operation", strconv.Itoa(int(op)))
	for _, m := range mids {
		params.Add("mids", strconv.FormatInt(m, 10))
	}
	return c.doPostDetailedContext(ctx, "/goim/push/mids", params, msg)
}

// PushToRoom broadcasts a message to a room.
func (c *PushClient) PushToRoom(op int32, roomType, roomID string, msg []byte) (string, error) {
	return c.PushToRoomContext(context.Background(), op, roomType, roomID, msg)
}

func (c *PushClient) PushToRoomContext(ctx context.Context, op int32, roomType, roomID string, msg []byte) (string, error) {
	params := url.Values{}
	params.Set("operation", strconv.Itoa(int(op)))
	params.Set("type", roomType)
	params.Set("room", roomID)
	ids, err := c.doPostContext(ctx, "/goim/push/room", params, msg)
	if err != nil {
		return "", err
	}
	if len(ids) > 0 {
		return ids[0], nil
	}
	return "", nil
}

// OnlineTotal holds the response from Logic's online/total endpoint.
type OnlineTotal struct {
	IPCount        int64 `json:"ip_count"`
	ConnCount      int64 `json:"conn_count"`
	UserCount      int64 `json:"user_count"`
	OfflinePending int64 `json:"offline_pending"`
	DirectPushed   int64 `json:"direct_pushed"`
	KafkaFallback  int64 `json:"kafka_fallback"`
}

// FetchOnlineTotal fetches the total online connection count from goim Logic.
func (c *PushClient) FetchOnlineTotal() (*OnlineTotal, error) {
	return c.FetchOnlineTotalContext(context.Background())
}

// FetchOnlineTotalContext fetches the total online connection count from goim Logic.
func (c *PushClient) FetchOnlineTotalContext(ctx context.Context) (*OnlineTotal, error) {
	u := fmt.Sprintf("http://%s/goim/online/total", c.logicAddr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, classifyHTTPError(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read online total response failed: %w", err)
	}

	var result struct {
		Code int         `json:"code"`
		Data OnlineTotal `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal online total response failed: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("logic online total error: code=%d", result.Code)
	}
	return &result.Data, nil
}

// PushAll broadcasts a message to all connected clients.
func (c *PushClient) PushAll(op int32, speed int32, msg []byte) (string, error) {
	return c.PushAllContext(context.Background(), op, speed, msg)
}

func (c *PushClient) PushAllContext(ctx context.Context, op int32, speed int32, msg []byte) (string, error) {
	params := url.Values{}
	params.Set("operation", strconv.Itoa(int(op)))
	params.Set("speed", strconv.Itoa(int(speed)))
	ids, err := c.doPostContext(ctx, "/goim/push/all", params, msg)
	if err != nil {
		return "", err
	}
	if len(ids) > 0 {
		return ids[0], nil
	}
	return "", nil
}

func (c *PushClient) doPost(path string, params url.Values, body []byte) ([]string, error) {
	results, err := c.doPostDetailedContext(context.Background(), path, params, body)
	return deliveryMsgIDs(results), err
}

func (c *PushClient) doPostContext(ctx context.Context, path string, params url.Values, body []byte) ([]string, error) {
	results, err := c.doPostDetailedContext(ctx, path, params, body)
	return deliveryMsgIDs(results), err
}

func (c *PushClient) doPostDetailedContext(ctx context.Context, path string, params url.Values, body []byte) ([]DeliveryResult, error) {
	if c.breaker != nil && !c.breaker.Allow() {
		return nil, &PushError{Type: "circuit_open", Class: "circuit_open", Message: "push circuit is open", Retryable: true}
	}
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		results, err := c.doPostOnceDetailed(ctx, path, params, body)
		if err == nil {
			if c.breaker != nil {
				c.breaker.RecordSuccess()
			}
			return results, nil
		}
		lastErr = err
		if !IsRetryablePushError(err) || attempt == c.maxRetries {
			break
		}
		timer := time.NewTimer(c.backoffBase * time.Duration(1<<attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, classifyHTTPError(ctx.Err())
		case <-timer.C:
		}
	}
	if c.breaker != nil {
		c.breaker.RecordFailure()
	}
	return nil, lastErr
}

func (c *PushClient) doPostOnce(ctx context.Context, path string, params url.Values, body []byte) ([]string, error) {
	results, err := c.doPostOnceDetailed(ctx, path, params, body)
	return deliveryMsgIDs(results), err
}

func (c *PushClient) doPostOnceDetailed(ctx context.Context, path string, params url.Values, body []byte) ([]DeliveryResult, error) {
	u := fmt.Sprintf("http://%s%s?%s", c.logicAddr, path, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, classifyHTTPError(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &PushError{Type: "invalid_response", Class: "transient", Message: err.Error(), Retryable: true, Err: err}
	}
	if resp.StatusCode >= 500 {
		return nil, &PushError{Type: "http_5xx", Class: "transient", StatusCode: resp.StatusCode, Message: string(respBody), Retryable: true}
	}
	if resp.StatusCode >= 400 {
		return nil, &PushError{Type: "http_4xx", Class: "permanent", StatusCode: resp.StatusCode, Message: string(respBody), Retryable: false}
	}

	var result struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, &PushError{Type: "invalid_response", Class: "permanent", Message: err.Error(), Retryable: false, Err: err}
	}
	if result.Code != 0 {
		return nil, &PushError{Type: "logic_error", Class: "permanent", Message: fmt.Sprintf("code=%d, msg=%s", result.Code, result.Message), Retryable: false}
	}

	var data struct {
		MsgIDs          []string         `json:"msg_ids"`
		MsgID           string           `json:"msg_id"`
		DeliveryResults []DeliveryResult `json:"delivery_results"`
	}
	if err := json.Unmarshal(result.Data, &data); err == nil {
		if len(data.DeliveryResults) > 0 {
			return data.DeliveryResults, nil
		}
		var results []DeliveryResult
		for _, id := range data.MsgIDs {
			results = append(results, DeliveryResult{MsgID: id, Path: "logic_push", AttemptNo: 1})
		}
		if len(results) == 0 && data.MsgID != "" {
			results = append(results, DeliveryResult{MsgID: data.MsgID, Path: "logic_push", AttemptNo: 1})
		}
		return results, nil
	}
	return nil, nil
}

func classifyHTTPError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &PushError{Type: "timeout", Class: "timeout", Message: err.Error(), Retryable: true, Err: err}
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &PushError{Type: "timeout", Class: "timeout", Message: err.Error(), Retryable: true, Err: err}
	}
	return &PushError{Type: "connection_refused", Class: "transient", Message: err.Error(), Retryable: true, Err: err}
}

// PushJSONToUser marshals data to JSON and pushes to a user by keys.
func (c *PushClient) PushJSONToUser(op int32, keys []string, data interface{}) ([]string, error) {
	return c.PushJSONToUserContext(context.Background(), op, keys, data)
}

func (c *PushClient) PushJSONToUserContext(ctx context.Context, op int32, keys []string, data interface{}) ([]string, error) {
	results, err := c.PushJSONToUserDetailedContext(ctx, op, keys, data)
	return deliveryMsgIDs(results), err
}

func (c *PushClient) PushJSONToUserDetailedContext(ctx context.Context, op int32, keys []string, data interface{}) ([]DeliveryResult, error) {
	body, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	return c.PushToUserDetailedContext(ctx, op, keys, body)
}

// PushJSONToUsers marshals data to JSON and pushes to multiple users by mids.
func (c *PushClient) PushJSONToUsers(op int32, mids []int64, data interface{}) ([]string, error) {
	return c.PushJSONToUsersContext(context.Background(), op, mids, data)
}

func (c *PushClient) PushJSONToUsersContext(ctx context.Context, op int32, mids []int64, data interface{}) ([]string, error) {
	results, err := c.PushJSONToUsersDetailedContext(ctx, op, mids, data)
	return deliveryMsgIDs(results), err
}

func (c *PushClient) PushJSONToUsersDetailedContext(ctx context.Context, op int32, mids []int64, data interface{}) ([]DeliveryResult, error) {
	body, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	return c.PushToUsersDetailedContext(ctx, op, mids, body)
}

func deliveryMsgIDs(results []DeliveryResult) []string {
	msgIDs := make([]string, 0, len(results))
	for _, result := range results {
		if result.MsgID != "" {
			msgIDs = append(msgIDs, result.MsgID)
		}
	}
	return msgIDs
}

// BuildNotificationJSON creates a JSON-serializable map for a notification message.
func BuildNotificationJSON(notifyType, title, content, notifyID, orderID string) map[string]interface{} {
	return map[string]interface{}{
		"type":      notifyType,
		"title":     title,
		"content":   content,
		"notify_id": notifyID,
		"order_id":  orderID,
		"timestamp": time.Now().UnixMilli(),
	}
}

// statusNotification returns a human-readable title and content for an order status change.
func statusNotification(status string, orderID string, extra map[string]string) (title, content string) {
	switch status {
	case "created":
		return "订单已创建", fmt.Sprintf("订单 %s 已创建，等待支付", orderID)
	case "paid":
		return "订单已支付", fmt.Sprintf("订单 %s 已支付，商家正在确认", orderID)
	case "confirmed":
		return "订单已确认", fmt.Sprintf("订单 %s 已确认，准备发货", orderID)
	case "shipped":
		loc := ""
		if extra != nil {
			loc = extra["location"]
		}
		if loc != "" {
			return "订单已发货", fmt.Sprintf("订单 %s 已从%s发出", orderID, loc)
		}
		return "订单已发货", fmt.Sprintf("订单 %s 已发货，正在运送途中", orderID)
	case "delivered":
		return "订单已签收", fmt.Sprintf("订单 %s 已签收，感谢您的购买", orderID)
	case "cancelled":
		reason := ""
		if extra != nil {
			reason = extra["reason"]
		}
		if reason != "" {
			return "订单已取消", fmt.Sprintf("订单 %s 已取消：%s", orderID, reason)
		}
		return "订单已取消", fmt.Sprintf("订单 %s 已取消", orderID)
	case "delivery_failed":
		reason := ""
		if extra != nil {
			reason = extra["reason"]
		}
		if reason != "" {
			return "配送异常", fmt.Sprintf("订单 %s 配送失败：%s", orderID, reason)
		}
		return "配送异常", fmt.Sprintf("订单 %s 配送失败，请联系客服", orderID)
	default:
		return "订单状态更新", fmt.Sprintf("订单 %s 状态更新为：%s", orderID, status)
	}
}

// LogistisNotification returns a human-readable title and content for a logistics update.
func LogistisNotification(orderID, location, desc string) (title, content string) {
	return "物流更新", fmt.Sprintf("订单 %s — %s：%s", orderID, location, desc)
}

// FlashSaleNotification returns a human-readable title and content for a flash sale alert.
func FlashSaleNotification(saleTitle, desc string) (title, content string) {
	return "限时闪购", fmt.Sprintf("%s — %s，立即抢购", saleTitle, desc)
}

// extractKeysFromMids converts user ID strings to connection key format for goim.
func extractKeysFromMids(mids []string) []string {
	keys := make([]string, len(mids))
	for i, mid := range mids {
		keys[i] = fmt.Sprintf("uid:%s", mid)
	}
	return keys
}
