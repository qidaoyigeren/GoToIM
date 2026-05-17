package service

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/notify/model"
)

// OrderNotifyService handles order status change notifications.
type OrderNotifyService struct {
	mu            sync.RWMutex
	orders        map[string]*model.Order          // orderID -> Order
	userOrders    map[string][]string              // userID -> []orderID
	notifications map[string][]*model.Notification // userID -> notifications
	pushClient    *PushClient
	stats         *StatsCollector
}

// StatsCollector tracks aggregate platform metrics.
type StatsCollector struct {
	mu             sync.RWMutex
	startTime      time.Time
	TotalPushed    int64
	TotalAcked     int64
	AckRate        float64
	LatencyP50Ms   float64
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

// NewOrderNotifyService creates a new OrderNotifyService.
func NewOrderNotifyService(pushClient *PushClient) *OrderNotifyService {
	return &OrderNotifyService{
		orders:        make(map[string]*model.Order),
		userOrders:    make(map[string][]string),
		notifications: make(map[string][]*model.Notification),
		pushClient:    pushClient,
		stats:         newStatsCollector(),
	}
}

// CreateOrder creates a new order and sends a notification.
func (s *OrderNotifyService) CreateOrder(userID string, items []model.OrderItem, total float64) (*model.Order, *model.Notification, error) {
	order := &model.Order{
		OrderID:   generateOrderID(),
		UserID:    userID,
		Status:    model.OrderCreated,
		Items:     items,
		Total:     total,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	s.mu.Lock()
	s.orders[order.OrderID] = order
	s.userOrders[userID] = append(s.userOrders[userID], order.OrderID)
	s.mu.Unlock()

	notif := s.createNotification(userID, model.NotifyOrderStatus, order.OrderID, order.Status, nil)
	s.sendNotification(notif)
	return order, notif, nil
}

// ChangeOrderStatus updates an order's status and notifies the user.
func (s *OrderNotifyService) ChangeOrderStatus(orderID string, newStatus model.OrderStatus, extra map[string]string) (*model.Order, *model.Notification, error) {
	s.mu.Lock()
	order, ok := s.orders[orderID]
	if !ok {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("order %s not found", orderID)
	}
	if !model.ValidTransition(order.Status, newStatus) {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("invalid transition: %s -> %s", order.Status, newStatus)
	}
	order.Status = newStatus
	order.UpdatedAt = time.Now()
	s.mu.Unlock()

	notif := s.createNotification(order.UserID, model.NotifyOrderStatus, orderID, order.Status, extra)
	s.sendNotification(notif)
	return order, notif, nil
}

// GetOrder returns an order by ID.
func (s *OrderNotifyService) GetOrder(orderID string) (*model.Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[orderID]
	return o, ok
}

// GetUserOrders returns all orders for a user.
func (s *OrderNotifyService) GetUserOrders(userID string) []*model.Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	orderIDs := s.userOrders[userID]
	orders := make([]*model.Order, 0, len(orderIDs))
	for _, id := range orderIDs {
		if o, ok := s.orders[id]; ok {
			orders = append(orders, o)
		}
	}
	return orders
}

// GetUserNotifications returns notification history for a user.
func (s *OrderNotifyService) GetUserNotifications(userID string) []*model.Notification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	notifs := s.notifications[userID]
	result := make([]*model.Notification, len(notifs))
	copy(result, notifs)
	return result
}

// GetStatsCollector returns the stats collector for sharing with other services.
func (s *OrderNotifyService) GetStatsCollector() *StatsCollector {
	return s.stats
}

// RefreshRealtimeStats fetches real connection counts from goim Logic.
func (s *OrderNotifyService) RefreshRealtimeStats() {
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

// GetStats returns current platform statistics.
func (s *OrderNotifyService) GetStats() model.PlatformStats {
	s.stats.mu.RLock()
	defer s.stats.mu.RUnlock()
	return model.PlatformStats{
		PushRatePerSec: float64(s.stats.TotalPushed) / max(time.Since(s.stats.startTime).Seconds(), 1),
		TotalPushed:    s.stats.TotalPushed,
		AckRate:        s.stats.AckRate,
		LatencyP50Ms:   s.stats.LatencyP50Ms,
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

func (s *OrderNotifyService) createNotification(userID string, nType model.NotifyType, orderID string, status model.OrderStatus, extra map[string]string) *model.Notification {
	title, content := statusNotification(string(status), orderID, extra)
	notif := &model.Notification{
		NotifyID:  generateNotifyID(),
		UserID:    userID,
		Type:      nType,
		Title:     title,
		Content:   content,
		OrderID:   orderID,
		CreatedAt: time.Now(),
		Status:    "pending",
	}

	s.mu.Lock()
	s.notifications[userID] = append(s.notifications[userID], notif)
	s.mu.Unlock()
	return notif
}

func (s *OrderNotifyService) sendNotification(notif *model.Notification) {
	payload := BuildNotificationJSON(string(notif.Type), notif.Title, notif.Content, notif.NotifyID, notif.OrderID)
	pushStartedAt := time.Now()
	mid, parseErr := strconv.ParseInt(notif.UserID, 10, 64)
	var err error
	if parseErr == nil {
		_, err = s.pushClient.PushJSONToUsers(protocol.OpRaw, []int64{mid}, payload)
	} else {
		keys := extractKeysFromMids([]string{notif.UserID})
		_, err = s.pushClient.PushJSONToUser(protocol.OpRaw, keys, payload)
	}

	s.mu.Lock()
	if err != nil {
		notif.Status = "failed"
	} else {
		notif.Status = "delivered"
	}
	s.mu.Unlock()

	s.stats.mu.Lock()
	s.stats.TotalPushed++
	s.stats.recordPendingAck(notif.NotifyID, 1, pushStartedAt)
	s.stats.mu.Unlock()
}

// SendCustomNotification creates and sends a notification with arbitrary content
// without changing order status. Used for logistics updates and system messages.
func (s *OrderNotifyService) SendCustomNotification(userID string, nType model.NotifyType, orderID, title, content string) *model.Notification {
	notif := &model.Notification{
		NotifyID:  generateNotifyID(),
		UserID:    userID,
		Type:      nType,
		Title:     title,
		Content:   content,
		OrderID:   orderID,
		CreatedAt: time.Now(),
		Status:    "pending",
	}

	s.mu.Lock()
	s.notifications[userID] = append(s.notifications[userID], notif)
	s.mu.Unlock()

	s.sendNotification(notif)
	return notif
}

// RecordAck increments the acked counter and records latency from push to ack.
// It returns false for empty, unknown, or duplicate notification ACKs.
func (s *OrderNotifyService) RecordAck(notifyID string) bool {
	if notifyID == "" {
		return false
	}

	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()

	pushTime, ok := s.stats.pushTimes[notifyID]
	if !ok {
		return false
	}
	remaining := s.stats.pendingAcks[notifyID]
	if remaining <= 0 {
		delete(s.stats.pushTimes, notifyID)
		delete(s.stats.pendingAcks, notifyID)
		return false
	}

	s.stats.TotalAcked++
	if s.stats.TotalPushed > 0 {
		s.stats.AckRate = float64(s.stats.TotalAcked) / float64(s.stats.TotalPushed)
	}

	latency := float64(time.Since(pushTime).Microseconds()) / 1000.0 // ms
	s.stats.latencies = append(s.stats.latencies, latency)
	if remaining == 1 {
		delete(s.stats.pushTimes, notifyID)
		delete(s.stats.pendingAcks, notifyID)
	} else {
		s.stats.pendingAcks[notifyID] = remaining - 1
	}

	// Keep recent samples; percentile sorting uses a copy below.
	if len(s.stats.latencies) > 1000 {
		s.stats.latencies = s.stats.latencies[len(s.stats.latencies)-1000:]
	}

	samples := append([]float64(nil), s.stats.latencies...)
	sort.Float64s(samples)
	n := len(samples)
	if n > 0 {
		s.stats.LatencyP50Ms = samples[n*50/100]
		s.stats.LatencyP99Ms = samples[n*99/100]
		s.stats.LatencyMaxMs = samples[n-1]
	}
	return true
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
	orderSeq int64
	notifSeq int64
)

func generateOrderID() string {
	seq := atomic.AddInt64(&orderSeq, 1)
	return fmt.Sprintf("ORD-%06d", seq)
}

func generateNotifyID() string {
	seq := atomic.AddInt64(&notifSeq, 1)
	return fmt.Sprintf("NTF-%06d", seq)
}

func ratio(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}
