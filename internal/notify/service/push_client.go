package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// PushClient calls goim Logic's HTTP push API.
type PushClient struct {
	logicAddr string
	client    *http.Client
}

// NewPushClient creates a new PushClient targeting the given Logic HTTP address.
func NewPushClient(logicAddr string) *PushClient {
	return &PushClient{
		logicAddr: logicAddr,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 32,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// PushToUser sends a message to a specific user by their keys (device-level).
func (c *PushClient) PushToUser(op int32, keys []string, msg []byte) ([]string, error) {
	params := url.Values{}
	params.Set("operation", strconv.Itoa(int(op)))
	for _, k := range keys {
		params.Add("keys", k)
	}
	return c.doPost("/goim/push/keys", params, msg)
}

// PushToUsers sends a message to multiple users by their mids (user IDs).
func (c *PushClient) PushToUsers(op int32, mids []int64, msg []byte) ([]string, error) {
	params := url.Values{}
	params.Set("operation", strconv.Itoa(int(op)))
	for _, m := range mids {
		params.Add("mids", strconv.FormatInt(m, 10))
	}
	return c.doPost("/goim/push/mids", params, msg)
}

// PushToRoom broadcasts a message to a room.
func (c *PushClient) PushToRoom(op int32, roomType, roomID string, msg []byte) (string, error) {
	params := url.Values{}
	params.Set("operation", strconv.Itoa(int(op)))
	params.Set("type", roomType)
	params.Set("room", roomID)
	ids, err := c.doPost("/goim/push/room", params, msg)
	if err != nil {
		return "", err
	}
	if len(ids) > 0 {
		return ids[0], nil
	}
	return "", nil
}

// PushAll broadcasts a message to all connected clients.
func (c *PushClient) PushAll(op int32, speed int32, msg []byte) (string, error) {
	params := url.Values{}
	params.Set("operation", strconv.Itoa(int(op)))
	params.Set("speed", strconv.Itoa(int(speed)))
	ids, err := c.doPost("/goim/push/all", params, msg)
	if err != nil {
		return "", err
	}
	if len(ids) > 0 {
		return ids[0], nil
	}
	return "", nil
}

func (c *PushClient) doPost(path string, params url.Values, body []byte) ([]string, error) {
	u := fmt.Sprintf("http://%s%s?%s", c.logicAddr, path, params.Encode())
	resp, err := c.client.Post(u, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("push request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}

	var result struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response failed: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("goim push error: code=%d, msg=%s", result.Code, result.Message)
	}

	var data struct {
		MsgIDs []string `json:"msg_ids"`
		MsgID  string   `json:"msg_id"`
	}
	var msgIDs []string
	if err := json.Unmarshal(result.Data, &data); err == nil {
		if len(data.MsgIDs) > 0 {
			msgIDs = data.MsgIDs
		} else if data.MsgID != "" {
			msgIDs = []string{data.MsgID}
		}
	}
	return msgIDs, nil
}

// PushJSONToUser marshals data to JSON and pushes to a user by keys.
func (c *PushClient) PushJSONToUser(op int32, keys []string, data interface{}) ([]string, error) {
	body, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	return c.PushToUser(op, keys, body)
}

// PushJSONToUsers marshals data to JSON and pushes to multiple users by mids.
func (c *PushClient) PushJSONToUsers(op int32, mids []int64, data interface{}) ([]string, error) {
	body, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	return c.PushToUsers(op, mids, body)
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
