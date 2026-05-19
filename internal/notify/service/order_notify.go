package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/policy"
	"github.com/Terry-Mao/goim/internal/notify/store"
)

var (
	// ErrOrderNotFound marks a missing order lookup.
	ErrOrderNotFound = errors.New("order not found")
	// ErrInvalidTransition marks an illegal order status transition.
	ErrInvalidTransition = errors.New("invalid transition")
)

// AckInput captures optional client ACK metadata.
type AckInput struct {
	NotifyID       string `json:"notify_id"`
	MsgID          string `json:"msg_id,omitempty"`
	DeviceID       string `json:"device_id,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// DeviceResolver provides the notification-time target device snapshot used by ACK policies.
type DeviceResolver interface {
	ResolveDevices(userID string) (targetDeviceIDs []string, primaryDeviceID string, err error)
}

// NoopDeviceResolver keeps legacy behavior when no device source is configured.
type NoopDeviceResolver struct{}

// ResolveDevices returns no device snapshot for legacy deployments.
func (NoopDeviceResolver) ResolveDevices(string) ([]string, string, error) {
	return nil, "", nil
}

// OrderNotifyService handles order status change notifications.
type OrderNotifyService struct {
	mu                  sync.Mutex
	pushClient          *PushClient
	stats               *StatsCollector
	store               *store.SQLStore
	deviceResolver      DeviceResolver
	scenarioMu          sync.RWMutex
	activeScenarioRunID string
}

// StatsCollector tracks realtime and legacy aggregate platform metrics.
type StatsCollector struct {
	mu             sync.RWMutex
	startTime      time.Time
	TotalPushed    int64
	TotalAcked     int64
	AckRate        float64
	LatencyP50Ms   float64
	LatencyP95Ms   float64
	LatencyP99Ms   float64
	LatencyMaxMs   float64
	GrpcDirect     int64
	KafkaFallback  int64
	ActiveConns    int64
	OnlineUsers    int64
	OfflinePending int64
	pushTimes      map[string]time.Time // notifyID -> push time
	pendingAcks    map[string]int64     // notifyID -> expected ACK count
	latencies      []float64            // recent latencies for percentile calc
}

// NewOrderNotifyService creates a new OrderNotifyService with the default MySQL store.
func NewOrderNotifyService(pushClient *PushClient) *OrderNotifyService {
	st, err := store.Open("")
	if err != nil {
		panic(err)
	}
	return NewOrderNotifyServiceWithStore(pushClient, st)
}

// NewOrderNotifyServiceWithStore creates a service backed by the provided store.
func NewOrderNotifyServiceWithStore(pushClient *PushClient, st *store.SQLStore) *OrderNotifyService {
	return &OrderNotifyService{
		pushClient:     pushClient,
		stats:          newStatsCollector(),
		store:          st,
		deviceResolver: NoopDeviceResolver{},
	}
}

// Close closes the service store.
func (s *OrderNotifyService) Close() error {
	return s.store.Close()
}

// Store exposes the backing store for focused tests.
func (s *OrderNotifyService) Store() *store.SQLStore {
	return s.store
}

// SetDeviceResolver installs a target device source for primary/all-device ACK policies.
func (s *OrderNotifyService) SetDeviceResolver(resolver DeviceResolver) {
	if resolver == nil {
		resolver = NoopDeviceResolver{}
	}
	s.deviceResolver = resolver
}

// CreateOrder creates a new order and sends a notification.
func (s *OrderNotifyService) CreateOrder(userID string, items []model.OrderItem, total float64) (*model.Order, *model.Notification, error) {
	return s.CreateOrderIdempotent(userID, items, total, "")
}

// CreateOrderIdempotent creates an order and replays the first response for a repeated key.
func (s *OrderNotifyService) CreateOrderIdempotent(userID string, items []model.OrderItem, total float64, idempotencyKey string) (*model.Order, *model.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var replay struct {
		Order        *model.Order        `json:"order"`
		Notification *model.Notification `json:"notification"`
	}
	if ok, err := s.loadIdempotency("order_create", idempotencyKey, &replay); err != nil {
		return nil, nil, err
	} else if ok {
		return replay.Order, replay.Notification, nil
	}

	now := time.Now()
	order := &model.Order{
		OrderID:   generateOrderID(),
		UserID:    userID,
		Status:    model.OrderCreated,
		Items:     items,
		Total:     total,
		CreatedAt: now,
		UpdatedAt: now,
	}
	notif := s.createNotification(userID, model.NotifyOrderStatus, order.OrderID, order.Status, nil, idempotencyKey)
	outbox, err := s.createOutbox(notif)
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := marshalReplay(replayPayload(order, notif))
	if err != nil {
		return nil, nil, err
	}
	if err := s.store.CreateOrderNotificationOutbox(order, notif, outbox, "order_create", idempotencyKey, "order", order.OrderID, snapshot); err != nil {
		return nil, nil, fmt.Errorf("create order transaction: %w", err)
	}
	s.incrementScenarioRun(notif.ScenarioRunID, 1, 1, 0, 0, 0, 0)
	return order, notif, nil
}

// ChangeOrderStatus updates an order's status and notifies the user.
func (s *OrderNotifyService) ChangeOrderStatus(orderID string, newStatus model.OrderStatus, extra map[string]string) (*model.Order, *model.Notification, error) {
	return s.ChangeOrderStatusIdempotent(orderID, newStatus, extra, "")
}

// ChangeOrderStatusIdempotent updates status and replays the first response for a repeated key.
func (s *OrderNotifyService) ChangeOrderStatusIdempotent(orderID string, newStatus model.OrderStatus, extra map[string]string, idempotencyKey string) (*model.Order, *model.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var replay struct {
		Order        *model.Order        `json:"order"`
		Notification *model.Notification `json:"notification"`
	}
	if ok, err := s.loadIdempotency("order_status_change", idempotencyKey, &replay); err != nil {
		return nil, nil, err
	} else if ok {
		return replay.Order, replay.Notification, nil
	}

	order, err := s.store.GetOrder(orderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if !model.ValidTransition(order.Status, newStatus) {
		return nil, nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, order.Status, newStatus)
	}

	now := time.Now()
	event := &model.OrderStatusEvent{
		EventID:        generateEventID(),
		OrderID:        orderID,
		FromStatus:     order.Status,
		ToStatus:       newStatus,
		Extra:          extra,
		IdempotencyKey: idempotencyKey,
		CreatedAt:      now,
	}
	order.Status = newStatus
	order.UpdatedAt = now
	notif := s.createNotification(order.UserID, model.NotifyOrderStatus, orderID, order.Status, extra, idempotencyKey)
	outbox, err := s.createOutbox(notif)
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := marshalReplay(replayPayload(order, notif))
	if err != nil {
		return nil, nil, err
	}
	if err := s.store.ChangeOrderNotificationOutbox(order, event, notif, outbox, "order_status_change", idempotencyKey, "order_status_event", event.EventID, snapshot); err != nil {
		return nil, nil, err
	}
	s.incrementScenarioRun(notif.ScenarioRunID, 0, 1, 0, 0, 0, 0)
	return order, notif, nil
}

// GetOrder returns an order by ID.
func (s *OrderNotifyService) GetOrder(orderID string) (*model.Order, bool) {
	order, err := s.store.GetOrder(orderID)
	return order, err == nil
}

// GetUserOrders returns all orders for a user.
func (s *OrderNotifyService) GetUserOrders(userID string) []*model.Order {
	orders, err := s.store.ListUserOrders(userID)
	if err != nil {
		return nil
	}
	return orders
}

// GetUserNotifications returns notification history for a user.
func (s *OrderNotifyService) GetUserNotifications(userID string) []*model.Notification {
	notifs, err := s.store.ListUserNotifications(userID)
	if err != nil {
		return nil
	}
	return notifs
}

// GetStatsCollector returns the stats collector for sharing with other services.
func (s *OrderNotifyService) GetStatsCollector() *StatsCollector {
	return s.stats
}

// RefreshRealtimeStats fetches real connection counts from goim Logic.
func (s *OrderNotifyService) RefreshRealtimeStats() {
	if s.pushClient == nil {
		return
	}
	online, err := s.pushClient.FetchOnlineTotal()
	if err != nil {
		return
	}
	s.stats.mu.Lock()
	s.stats.ActiveConns = online.ConnCount
	s.stats.OnlineUsers = online.UserCount
	s.stats.OfflinePending = online.OfflinePending
	s.stats.GrpcDirect = online.DirectPushed
	s.stats.KafkaFallback = online.KafkaFallback
	s.stats.mu.Unlock()
}

// GetStats returns current platform statistics based on persisted notifications.
func (s *OrderNotifyService) GetStats() model.PlatformStats {
	s.stats.mu.RLock()
	activeConns := s.stats.ActiveConns
	onlineUsers := s.stats.OnlineUsers
	offlinePending := s.stats.OfflinePending
	s.stats.mu.RUnlock()

	stats, err := s.store.PlatformStats(activeConns, onlineUsers, offlinePending)
	if err == nil {
		return stats
	}

	s.stats.mu.RLock()
	defer s.stats.mu.RUnlock()
	return model.PlatformStats{
		PushRatePerSec: float64(s.stats.TotalPushed) / max(time.Since(s.stats.startTime).Seconds(), 1),
		TotalPushed:    s.stats.TotalPushed,
		AckRate:        s.stats.AckRate,
		LatencyP50Ms:   s.stats.LatencyP50Ms,
		LatencyP95Ms:   s.stats.LatencyP95Ms,
		LatencyP99Ms:   s.stats.LatencyP99Ms,
		LatencyMaxMs:   s.stats.LatencyMaxMs,
		ActiveConns:    s.stats.ActiveConns,
		DeliveryPath: model.DeliveryPathRatio{
			GrpcDirect:    ratio(s.stats.GrpcDirect, s.stats.GrpcDirect+s.stats.KafkaFallback),
			KafkaFallback: ratio(s.stats.KafkaFallback, s.stats.GrpcDirect+s.stats.KafkaFallback),
		},
		OnlineUsers:    s.stats.OnlineUsers,
		OfflinePending: s.stats.OfflinePending,
	}
}

// CreateScenarioRun records a trackable scenario run.
func (s *OrderNotifyService) CreateScenarioRun(mode string, qps, users int) (*model.ScenarioRun, error) {
	now := time.Now()
	run := &model.ScenarioRun{
		RunID:     generateScenarioID(),
		Mode:      mode,
		Status:    "running",
		QPS:       qps,
		Users:     users,
		StartedAt: now,
	}
	if err := s.store.CreateScenarioRun(run); err != nil {
		return nil, err
	}
	s.SetActiveScenarioRun(run.RunID)
	_ = s.store.InsertScenarioEvent(&model.ScenarioEvent{
		EventID:     generateEventID(),
		RunID:       run.RunID,
		Type:        "started",
		PayloadJSON: fmt.Sprintf(`{"mode":%q,"qps":%d,"users":%d}`, mode, qps, users),
		CreatedAt:   now,
	})
	return run, nil
}

// GetScenarioRun returns a persisted scenario run.
func (s *OrderNotifyService) GetScenarioRun(runID string) (*model.ScenarioRun, error) {
	return s.store.GetScenarioRun(runID)
}

// StopScenarioRun marks a run as stopped.
func (s *OrderNotifyService) StopScenarioRun(runID string) error {
	now := time.Now()
	if err := s.store.FinishScenarioRun(runID, "stopped", "", now); err != nil {
		return err
	}
	s.SetActiveScenarioRun("")
	return s.store.InsertScenarioEvent(&model.ScenarioEvent{
		EventID:     generateEventID(),
		RunID:       runID,
		Type:        "stopped",
		PayloadJSON: `{}`,
		CreatedAt:   now,
	})
}

// ListScenarioEvents returns persisted scenario events.
func (s *OrderNotifyService) ListScenarioEvents(runID string) ([]*model.ScenarioEvent, error) {
	return s.store.ListScenarioEvents(runID, 200)
}

// ListDLQ returns DLQ items.
func (s *OrderNotifyService) ListDLQ() ([]*model.NotificationDLQ, error) {
	return s.store.ListDLQ(100)
}

func (s *OrderNotifyService) GetDLQ(id string) (*model.NotificationDLQ, error) {
	return s.store.GetDLQ(id)
}

func (s *OrderNotifyService) ReplayDLQ(id, user string) error {
	return s.store.ReplayDLQ(id, user)
}

func (s *OrderNotifyService) ResolveDLQ(id, user, resolution string) error {
	return s.store.ResolveDLQ(id, user, resolution)
}

func (s *OrderNotifyService) createNotification(userID string, nType model.NotifyType, orderID string, status model.OrderStatus, extra map[string]string, idempotencyKey string) *model.Notification {
	title, content := statusNotification(string(status), orderID, extra)
	now := time.Now()
	p := policy.Resolve("order", string(status))
	notif := &model.Notification{
		NotifyID:          generateNotifyID(),
		UserID:            userID,
		Type:              nType,
		BusinessType:      p.BusinessType,
		EventType:         p.EventType,
		Title:             title,
		Content:           content,
		OrderID:           orderID,
		CreatedAt:         now,
		UpdatedAt:         now,
		Status:            "pending",
		Priority:          p.Priority,
		TTLSeconds:        int64(p.TTL.Seconds()),
		AckPolicy:         p.AckPolicy,
		ExpectedAckCount:  p.ExpectedAckCount,
		BusinessAckStatus: "pending",
		IdempotencyKey:    idempotencyKey,
		ScenarioRunID:     s.currentScenarioRunID(),
	}
	s.applyDeviceTargets(notif)
	return notif
}

func (s *OrderNotifyService) createOutbox(notif *model.Notification) (*model.NotificationOutbox, error) {
	payload := BuildNotificationJSON(string(notif.Type), notif.Title, notif.Content, notif.NotifyID, notif.OrderID)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	now := notif.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	return &model.NotificationOutbox{
		OutboxID:      generateOutboxID(),
		NotifyID:      notif.NotifyID,
		UserID:        notif.UserID,
		OrderID:       notif.OrderID,
		BusinessType:  notif.BusinessType,
		EventType:     notif.EventType,
		PayloadJSON:   string(body),
		Priority:      notif.Priority,
		TTLSeconds:    notif.TTLSeconds,
		Status:        "pending",
		ScenarioRunID: notif.ScenarioRunID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

func (s *OrderNotifyService) sendNotification(notif *model.Notification) {
	payload := BuildNotificationJSON(string(notif.Type), notif.Title, notif.Content, notif.NotifyID, notif.OrderID)
	startedAt := time.Now()
	finishedAt := startedAt
	channel := "logic_push"
	target := notif.UserID
	var err error

	if s.pushClient != nil {
		mid, parseErr := strconv.ParseInt(notif.UserID, 10, 64)
		if parseErr == nil {
			_, err = s.pushClient.PushJSONToUsers(protocol.OpRaw, []int64{mid}, payload)
		} else {
			keys := extractKeysFromMids([]string{notif.UserID})
			target = keys[0]
			_, err = s.pushClient.PushJSONToUser(protocol.OpRaw, keys, payload)
		}
		finishedAt = time.Now()
	}

	status := "delivered"
	attemptStatus := "success"
	errMsg := ""
	if err != nil {
		status = "failed"
		attemptStatus = "failed"
		errMsg = err.Error()
	}
	notif.Status = status
	notif.UpdatedAt = finishedAt
	_ = s.store.UpdateNotificationStatus(notif.NotifyID, status, finishedAt)
	_ = s.store.InsertAttempt(&model.NotificationAttempt{
		AttemptID:    generateAttemptID(),
		NotifyID:     notif.NotifyID,
		Channel:      channel,
		Target:       target,
		Status:       attemptStatus,
		ErrorMessage: errMsg,
		StartedAt:    startedAt,
		FinishedAt:   &finishedAt,
	})

	s.stats.mu.Lock()
	s.stats.TotalPushed++
	s.stats.recordPendingAck(notif.NotifyID, 1, startedAt)
	s.stats.mu.Unlock()
}

// SendCustomNotification creates and sends a notification with arbitrary content
// without changing order status. Used for logistics updates and system messages.
func (s *OrderNotifyService) SendCustomNotification(userID string, nType model.NotifyType, orderID, title, content string) *model.Notification {
	notif, _ := s.SendCustomNotificationIdempotent(userID, nType, orderID, title, content, "")
	return notif
}

// SendCustomNotificationIdempotent creates a custom notification with idempotency support.
func (s *OrderNotifyService) SendCustomNotificationIdempotent(userID string, nType model.NotifyType, orderID, title, content, idempotencyKey string) (*model.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var replay model.Notification
	if ok, err := s.loadIdempotency("logistics_update", idempotencyKey, &replay); err != nil {
		return nil, err
	} else if ok {
		return &replay, nil
	}

	now := time.Now()
	p := policy.Resolve("logistics", "update")
	notif := &model.Notification{
		NotifyID:          generateNotifyID(),
		UserID:            userID,
		Type:              nType,
		BusinessType:      p.BusinessType,
		EventType:         p.EventType,
		Title:             title,
		Content:           content,
		OrderID:           orderID,
		CreatedAt:         now,
		UpdatedAt:         now,
		Status:            "pending",
		Priority:          p.Priority,
		TTLSeconds:        int64(p.TTL.Seconds()),
		AckPolicy:         p.AckPolicy,
		ExpectedAckCount:  p.ExpectedAckCount,
		BusinessAckStatus: "pending",
		IdempotencyKey:    idempotencyKey,
		ScenarioRunID:     s.currentScenarioRunID(),
	}
	s.applyDeviceTargets(notif)
	outbox, err := s.createOutbox(notif)
	if err != nil {
		return nil, err
	}
	snapshot, err := marshalReplay(notif)
	if err != nil {
		return nil, err
	}
	if err := s.store.CreateNotificationOutbox(notif, outbox, "logistics_update", idempotencyKey, "notification", notif.NotifyID, snapshot); err != nil {
		return nil, err
	}
	s.incrementScenarioRun(notif.ScenarioRunID, 0, 1, 0, 0, 0, 0)
	return notif, nil
}

// CreateFlashSaleNotification persists a targeted flash-sale notification and outbox row.
func (s *OrderNotifyService) CreateFlashSaleNotification(userID, campaignID, title, content string) (*model.Notification, error) {
	return s.createFlashSaleNotificationForTarget(userID, campaignID, title, content)
}

// CreateFlashSaleBroadcastNotification persists a campaign-level broadcast outbox item.
func (s *OrderNotifyService) CreateFlashSaleBroadcastNotification(campaignID, title, content string) (*model.Notification, error) {
	return s.createFlashSaleNotificationForTarget("room:flash_sale_all", campaignID, title, content)
}

func (s *OrderNotifyService) createFlashSaleNotificationForTarget(userID, campaignID, title, content string) (*model.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	p := policy.Resolve("flash_sale", "notify")
	notif := &model.Notification{
		NotifyID:          generateNotifyID(),
		UserID:            userID,
		Type:              model.NotifyFlashSale,
		BusinessType:      p.BusinessType,
		EventType:         p.EventType,
		Title:             title,
		Content:           content,
		OrderID:           campaignID,
		CreatedAt:         now,
		UpdatedAt:         now,
		Status:            "pending",
		Priority:          p.Priority,
		TTLSeconds:        int64(p.TTL.Seconds()),
		AckPolicy:         p.AckPolicy,
		ExpectedAckCount:  p.ExpectedAckCount,
		BusinessAckStatus: "pending",
		ScenarioRunID:     s.currentScenarioRunID(),
	}
	s.applyDeviceTargets(notif)
	outbox, err := s.createOutbox(notif)
	if err != nil {
		return nil, err
	}
	if err := s.store.CreateNotificationOutbox(notif, outbox, "", "", "notification", notif.NotifyID, nil); err != nil {
		return nil, err
	}
	s.incrementScenarioRun(notif.ScenarioRunID, 0, 1, 0, 0, 0, 0)
	return notif, nil
}

// RecordAck records an ACK with only a notification id for backwards compatibility.
func (s *OrderNotifyService) RecordAck(notifyID string) bool {
	recorded, _ := s.RecordAckIdempotent(AckInput{NotifyID: notifyID}, "")
	return recorded
}

// RecordAckIdempotent records an ACK with optional client metadata.
func (s *OrderNotifyService) RecordAckIdempotent(input AckInput, idempotencyKey string) (bool, error) {
	if input.NotifyID == "" {
		return false, nil
	}
	if idempotencyKey == "" {
		idempotencyKey = input.IdempotencyKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var replay struct {
		Recorded bool `json:"recorded"`
	}
	if ok, err := s.loadIdempotency("ack", idempotencyKey, &replay); err != nil {
		return false, err
	} else if ok {
		return replay.Recorded, nil
	}

	notif, err := s.store.GetNotification(input.NotifyID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ackAt := time.Now()
	baseTime := notif.CreatedAt
	if attemptStart, ok, err := s.store.FirstAttemptStart(input.NotifyID); err != nil {
		return false, err
	} else if ok {
		baseTime = attemptStart
	}
	latencyMs := float64(ackAt.Sub(baseTime).Microseconds()) / 1000.0
	if latencyMs < 0 {
		latencyMs = 0
	}

	ack := &model.NotificationAck{
		AckID:     generateAckID(),
		NotifyID:  input.NotifyID,
		UserID:    notif.UserID,
		MsgID:     input.MsgID,
		DeviceID:  input.DeviceID,
		SessionID: input.SessionID,
		LatencyMs: latencyMs,
		CreatedAt: ackAt,
	}
	recorded, err := s.store.RecordAckWithIdempotency(ack, "ack", idempotencyKey, "notification_ack", input.NotifyID, nil)
	if err != nil {
		return false, err
	}

	if recorded {
		s.recordAckStats(input.NotifyID, baseTime)
		s.incrementScenarioRun(notif.ScenarioRunID, 0, 0, 0, 1, 0, 0)
	}
	return recorded, nil
}

func (s *OrderNotifyService) applyDeviceTargets(notif *model.Notification) {
	if notif == nil || s.deviceResolver == nil {
		return
	}
	targets, primary, err := s.deviceResolver.ResolveDevices(notif.UserID)
	if err != nil {
		return
	}
	notif.TargetDeviceIDs = targets
	notif.PrimaryDeviceID = primary
	if notif.AckPolicy == "all_devices" && notif.ExpectedAckCount == 0 && len(targets) > 0 {
		notif.ExpectedAckCount = int64(len(targets))
	}
}

func (s *OrderNotifyService) SetActiveScenarioRun(runID string) {
	s.scenarioMu.Lock()
	defer s.scenarioMu.Unlock()
	s.activeScenarioRunID = runID
}

func (s *OrderNotifyService) incrementScenario(generatedOrders, generatedNotifications, sent, acked, failed, dlq int64) {
	s.incrementScenarioRun(s.currentScenarioRunID(), generatedOrders, generatedNotifications, sent, acked, failed, dlq)
}

func (s *OrderNotifyService) currentScenarioRunID() string {
	s.scenarioMu.RLock()
	defer s.scenarioMu.RUnlock()
	return s.activeScenarioRunID
}

func (s *OrderNotifyService) incrementScenarioRun(runID string, generatedOrders, generatedNotifications, sent, acked, failed, dlq int64) {
	if runID != "" {
		_ = s.store.IncrementScenarioRunCounters(runID, generatedOrders, generatedNotifications, sent, acked, failed, dlq)
	}
}

func (s *OrderNotifyService) recordAckStats(notifyID string, pushTime time.Time) {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()

	if !pushTime.IsZero() {
		s.stats.pushTimes[notifyID] = pushTime
		s.stats.pendingAcks[notifyID] = 1
	}
	remaining := s.stats.pendingAcks[notifyID]
	if remaining <= 0 {
		return
	}

	s.stats.TotalAcked++
	if s.stats.TotalPushed > 0 {
		s.stats.AckRate = float64(s.stats.TotalAcked) / float64(s.stats.TotalPushed)
	}

	latency := float64(time.Since(s.stats.pushTimes[notifyID]).Microseconds()) / 1000.0
	s.stats.latencies = append(s.stats.latencies, latency)
	if remaining == 1 {
		delete(s.stats.pushTimes, notifyID)
		delete(s.stats.pendingAcks, notifyID)
	} else {
		s.stats.pendingAcks[notifyID] = remaining - 1
	}
	if len(s.stats.latencies) > 1000 {
		s.stats.latencies = s.stats.latencies[len(s.stats.latencies)-1000:]
	}
	samples := append([]float64(nil), s.stats.latencies...)
	sort.Float64s(samples)
	n := len(samples)
	if n > 0 {
		s.stats.LatencyP50Ms = samples[n*50/100]
		s.stats.LatencyP95Ms = samples[n*95/100]
		s.stats.LatencyP99Ms = samples[n*99/100]
		s.stats.LatencyMaxMs = samples[n-1]
	}
}

func (s *OrderNotifyService) loadIdempotency(scope, key string, dst any) (bool, error) {
	if key == "" {
		return false, nil
	}
	snapshot, ok, err := s.store.GetIdempotency(scope, key)
	if err != nil || !ok {
		return ok, err
	}
	if err := json.Unmarshal(snapshot, dst); err != nil {
		return false, err
	}
	return true, nil
}

func (s *OrderNotifyService) saveIdempotency(scope, key, resourceType, resourceID string, payload any) error {
	if key == "" {
		return nil
	}
	snapshot, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.store.SaveIdempotency(scope, key, resourceType, resourceID, snapshot)
}

func marshalReplay(payload any) ([]byte, error) {
	if payload == nil {
		return nil, nil
	}
	return json.Marshal(payload)
}

func replayPayload(values ...any) any {
	switch len(values) {
	case 1:
		if recorded, ok := values[0].(bool); ok {
			return struct {
				Recorded bool `json:"recorded"`
			}{Recorded: recorded}
		}
		return values[0]
	case 2:
		return struct {
			Order        any `json:"order"`
			Notification any `json:"notification"`
		}{Order: values[0], Notification: values[1]}
	default:
		return values
	}
}

func newStatsCollector() *StatsCollector {
	return &StatsCollector{
		startTime:   time.Now(),
		pushTimes:   make(map[string]time.Time),
		pendingAcks: make(map[string]int64),
	}
}

func (s *StatsCollector) recordPendingAck(notifyID string, count int64, pushedAt time.Time) {
	if notifyID == "" || count <= 0 {
		return
	}
	if s.pushTimes == nil {
		s.pushTimes = make(map[string]time.Time)
	}
	if s.pendingAcks == nil {
		s.pendingAcks = make(map[string]int64)
	}
	s.pushTimes[notifyID] = pushedAt
	s.pendingAcks[notifyID] += count
}

var (
	orderSeq    int64
	notifSeq    int64
	eventSeq    int64
	attemptSeq  int64
	ackSeq      int64
	outboxSeq   int64
	dlqSeq      int64
	scenarioSeq int64
)

func generateOrderID() string {
	seq := atomic.AddInt64(&orderSeq, 1)
	return fmt.Sprintf("ORD-%d-%06d", time.Now().UnixNano(), seq)
}

func generateNotifyID() string {
	seq := atomic.AddInt64(&notifSeq, 1)
	return fmt.Sprintf("NTF-%d-%06d", time.Now().UnixNano(), seq)
}

func generateEventID() string {
	seq := atomic.AddInt64(&eventSeq, 1)
	return fmt.Sprintf("EVT-%d-%06d", time.Now().UnixNano(), seq)
}

func generateAttemptID() string {
	seq := atomic.AddInt64(&attemptSeq, 1)
	return fmt.Sprintf("ATM-%d-%06d", time.Now().UnixNano(), seq)
}

func generateAckID() string {
	seq := atomic.AddInt64(&ackSeq, 1)
	return fmt.Sprintf("ACK-%d-%06d", time.Now().UnixNano(), seq)
}

func generateOutboxID() string {
	seq := atomic.AddInt64(&outboxSeq, 1)
	return fmt.Sprintf("OBX-%d-%06d", time.Now().UnixNano(), seq)
}

func generateDLQID() string {
	seq := atomic.AddInt64(&dlqSeq, 1)
	return fmt.Sprintf("DLQ-%d-%06d", time.Now().UnixNano(), seq)
}

func generateScenarioID() string {
	seq := atomic.AddInt64(&scenarioSeq, 1)
	return fmt.Sprintf("SCN-%d-%06d", time.Now().UnixNano(), seq)
}

func ratio(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}
