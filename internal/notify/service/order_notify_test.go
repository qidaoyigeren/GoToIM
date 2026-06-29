package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/store"
)

func openMySQLTestStore(t *testing.T) *store.SQLStore {
	t.Helper()
	dsn := os.Getenv("GOIM_NOTIFY_MYSQL_DSN")
	if dsn == "" {
		t.Skip("GOIM_NOTIFY_MYSQL_DSN not set, skipping MySQL test")
	}
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st
}

func newTestOrderService(t *testing.T) *OrderNotifyService {
	t.Helper()
	st := openMySQLTestStore(t)
	t.Cleanup(func() { _ = st.Close() })
	svc := NewOrderNotifyServiceWithStore(nil, st)
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

type staticDeviceResolver struct {
	targets []string
	primary string
}

func (r staticDeviceResolver) ResolveDevices(string) ([]string, string, error) {
	return r.targets, r.primary, nil
}

func TestCreateOrderPersistsAndCanBeQueried(t *testing.T) {
	svc := newTestOrderService(t)

	order, notif, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	if notif == nil || notif.NotifyID == "" {
		t.Fatal("CreateOrder did not create notification")
	}

	got, ok := svc.GetOrder(order.OrderID)
	if !ok {
		t.Fatal("stored order was not found")
	}
	if got.UserID != "1001" || got.Status != model.OrderCreated || got.Total != 99 {
		t.Fatalf("stored order = %+v, want created order for user 1001", got)
	}
}

func TestDeviceResolverPopulatesNotificationTargets(t *testing.T) {
	svc := newTestOrderService(t)
	svc.SetDeviceResolver(staticDeviceResolver{targets: []string{"dev-a", "dev-b"}, primary: "dev-a"})
	_, notif, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	if len(notif.TargetDeviceIDs) != 2 || notif.PrimaryDeviceID != "dev-a" {
		t.Fatalf("notification device target = %+v primary=%q", notif.TargetDeviceIDs, notif.PrimaryDeviceID)
	}
	stored, err := svc.Store().GetNotification(notif.NotifyID)
	if err != nil {
		t.Fatalf("GetNotification returned error: %v", err)
	}
	if len(stored.TargetDeviceIDs) != 2 || stored.PrimaryDeviceID != "dev-a" {
		t.Fatalf("stored device target = %+v primary=%q", stored.TargetDeviceIDs, stored.PrimaryDeviceID)
	}
}

func TestStatusChangeCreatesEventAndNotification(t *testing.T) {
	svc := newTestOrderService(t)
	order, _, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}

	updated, notif, err := svc.ChangeOrderStatus(order.OrderID, model.OrderConfirmed, nil)
	if err != nil {
		t.Fatalf("ChangeOrderStatus returned error: %v", err)
	}
	if updated.Status != model.OrderConfirmed {
		t.Fatalf("status = %s, want confirmed", updated.Status)
	}
	if notif.OrderID != order.OrderID {
		t.Fatalf("notification order_id = %q, want %q", notif.OrderID, order.OrderID)
	}
	events, err := svc.Store().StatusEventCount()
	if err != nil {
		t.Fatalf("StatusEventCount returned error: %v", err)
	}
	if events != 1 {
		t.Fatalf("status event count = %d, want 1", events)
	}
}

func TestInvalidStatusChangeReturnsTypedError(t *testing.T) {
	svc := newTestOrderService(t)
	order, _, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}

	_, _, err = svc.ChangeOrderStatus(order.OrderID, model.OrderDelivered, nil)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("ChangeOrderStatus error = %v, want ErrInvalidTransition", err)
	}
}

func TestCreateOrderIdempotencyDoesNotCreateDuplicateOrder(t *testing.T) {
	svc := newTestOrderService(t)
	items := []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}

	first, firstNotif, err := svc.CreateOrderIdempotent("1001", items, 99, "create-1")
	if err != nil {
		t.Fatalf("first CreateOrderIdempotent returned error: %v", err)
	}
	second, secondNotif, err := svc.CreateOrderIdempotent("1001", items, 99, "create-1")
	if err != nil {
		t.Fatalf("second CreateOrderIdempotent returned error: %v", err)
	}

	if first.OrderID != second.OrderID || firstNotif.NotifyID != secondNotif.NotifyID {
		t.Fatalf("idempotency replay changed ids: first=%s/%s second=%s/%s",
			first.OrderID, firstNotif.NotifyID, second.OrderID, secondNotif.NotifyID)
	}
	orders := svc.GetUserOrders("1001")
	if len(orders) != 1 {
		t.Fatalf("user order count = %d, want 1", len(orders))
	}
}

func TestCreateOrderIdempotencyConcurrentReplay(t *testing.T) {
	svc := newTestOrderService(t)
	items := []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}
	userID := "user-concurrent-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	key := "create-concurrent-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	const workers = 8
	var wg sync.WaitGroup
	orders := make([]*model.Order, workers)
	notifs := make([]*model.Notification, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			orders[idx], notifs[idx], errs[idx] = svc.CreateOrderIdempotent(userID, items, 99, key)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("CreateOrderIdempotent[%d] returned error: %v", i, err)
		}
		if orders[i] == nil || notifs[i] == nil {
			t.Fatalf("CreateOrderIdempotent[%d] returned nil result", i)
		}
		if orders[i].OrderID != orders[0].OrderID || notifs[i].NotifyID != notifs[0].NotifyID {
			t.Fatalf("replay[%d] changed ids: first=%s/%s got=%s/%s",
				i, orders[0].OrderID, notifs[0].NotifyID, orders[i].OrderID, notifs[i].NotifyID)
		}
	}
	if got := len(svc.GetUserOrders(userID)); got != 1 {
		t.Fatalf("user order count = %d, want 1", got)
	}
}

func TestStatusChangeIdempotencyDoesNotCreateDuplicateEventOrNotification(t *testing.T) {
	svc := newTestOrderService(t)
	order, _, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}

	_, firstNotif, err := svc.ChangeOrderStatusIdempotent(order.OrderID, model.OrderConfirmed, nil, "status-1")
	if err != nil {
		t.Fatalf("first ChangeOrderStatusIdempotent returned error: %v", err)
	}
	_, secondNotif, err := svc.ChangeOrderStatusIdempotent(order.OrderID, model.OrderConfirmed, nil, "status-1")
	if err != nil {
		t.Fatalf("second ChangeOrderStatusIdempotent returned error: %v", err)
	}
	if firstNotif.NotifyID != secondNotif.NotifyID {
		t.Fatalf("idempotency replay changed notify id: first=%s second=%s", firstNotif.NotifyID, secondNotif.NotifyID)
	}
	events, err := svc.Store().StatusEventCount()
	if err != nil {
		t.Fatalf("StatusEventCount returned error: %v", err)
	}
	if events != 1 {
		t.Fatalf("status event count = %d, want 1", events)
	}
	notifs, err := svc.Store().NotificationCount()
	if err != nil {
		t.Fatalf("NotificationCount returned error: %v", err)
	}
	if notifs != 2 {
		t.Fatalf("notification count = %d, want 2 (create + status change)", notifs)
	}
}

func TestStatusChangeIdempotencyConcurrentReplay(t *testing.T) {
	svc := newTestOrderService(t)
	userID := "status-concurrent-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	order, _, err := svc.CreateOrder(userID, []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	key := "status-concurrent-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	const workers = 8
	var wg sync.WaitGroup
	orders := make([]*model.Order, workers)
	notifs := make([]*model.Notification, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			orders[idx], notifs[idx], errs[idx] = svc.ChangeOrderStatusIdempotent(order.OrderID, model.OrderConfirmed, nil, key)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("ChangeOrderStatusIdempotent[%d] returned error: %v", i, err)
		}
		if orders[i] == nil || notifs[i] == nil {
			t.Fatalf("ChangeOrderStatusIdempotent[%d] returned nil result", i)
		}
		if orders[i].OrderID != orders[0].OrderID || notifs[i].NotifyID != notifs[0].NotifyID {
			t.Fatalf("replay[%d] changed ids: first=%s/%s got=%s/%s",
				i, orders[0].OrderID, notifs[0].NotifyID, orders[i].OrderID, notifs[i].NotifyID)
		}
	}
	events, err := svc.Store().ListOrderStatusEvents(order.OrderID)
	if err != nil {
		t.Fatalf("ListOrderStatusEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("status event count for order = %d, want 1", len(events))
	}
}

func TestAckUpdatesNotificationAndWritesAckOnce(t *testing.T) {
	svc := newTestOrderService(t)
	_, notif, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	time.Sleep(time.Millisecond)

	recorded, err := svc.RecordAckIdempotent(AckInput{
		NotifyID:  notif.NotifyID,
		MsgID:     "msg-1",
		DeviceID:  "dev-1",
		SessionID: "sess-1",
	}, "")
	if err != nil {
		t.Fatalf("RecordAckIdempotent returned error: %v", err)
	}
	if !recorded {
		t.Fatal("first ACK was not recorded")
	}
	recorded, err = svc.RecordAckIdempotent(AckInput{NotifyID: notif.NotifyID}, "")
	if err != nil {
		t.Fatalf("duplicate RecordAckIdempotent returned error: %v", err)
	}
	if recorded {
		t.Fatal("duplicate ACK was recorded")
	}

	stored, err := svc.Store().GetNotification(notif.NotifyID)
	if err != nil {
		t.Fatalf("GetNotification returned error: %v", err)
	}
	if stored.Status != "acked" {
		t.Fatalf("notification status = %s, want acked", stored.Status)
	}
	acks, err := svc.Store().AckCount()
	if err != nil {
		t.Fatalf("AckCount returned error: %v", err)
	}
	if acks != 1 {
		t.Fatalf("ack count = %d, want 1", acks)
	}
}

func TestPlatformStatsUsePersistedNotificationAckAndLatency(t *testing.T) {
	svc := newTestOrderService(t)
	_, notif1, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	_, _, err = svc.CreateOrder("1002", []model.OrderItem{{ProductName: "case", Quantity: 1, Price: 9}}, 9)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	time.Sleep(time.Millisecond)
	if recorded, err := svc.RecordAckIdempotent(AckInput{NotifyID: notif1.NotifyID}, "ack-1"); err != nil || !recorded {
		t.Fatalf("RecordAckIdempotent recorded=%v err=%v, want true nil", recorded, err)
	}
	if recorded, err := svc.RecordAckIdempotent(AckInput{NotifyID: notif1.NotifyID}, "ack-1"); err != nil || !recorded {
		t.Fatalf("idempotent ACK replay recorded=%v err=%v, want true nil", recorded, err)
	}

	stats := svc.GetStats()
	if stats.TotalPushed != 2 {
		t.Fatalf("TotalPushed = %d, want 2", stats.TotalPushed)
	}
	if stats.AckRate != 0.5 {
		t.Fatalf("AckRate = %f, want 0.5", stats.AckRate)
	}
	if stats.LatencyMaxMs <= 0 {
		t.Fatalf("LatencyMaxMs = %f, want positive latency", stats.LatencyMaxMs)
	}
	acks, err := svc.Store().AckCount()
	if err != nil {
		t.Fatalf("AckCount returned error: %v", err)
	}
	if acks != 1 {
		t.Fatalf("ack count = %d, want 1 after idempotent replay", acks)
	}
}

func TestCampaignTokenBucketRateLimit(t *testing.T) {
	svc := &OrderNotifyService{}
	if !svc.tryAcquireCampaignToken("camp-rate", 1) {
		t.Fatal("first token was not available")
	}
	if svc.tryAcquireCampaignToken("camp-rate", 1) {
		t.Fatal("second token should be rate limited")
	}
	time.Sleep(1100 * time.Millisecond)
	if !svc.tryAcquireCampaignToken("camp-rate", 1) {
		t.Fatal("token was not refilled")
	}
}

func TestCreateOrderWritesOutboxWithoutDirectPush(t *testing.T) {
	svc := newTestOrderService(t)
	_, _, err := svc.CreateOrderIdempotent("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99, "create-outbox")
	if err != nil {
		t.Fatalf("CreateOrderIdempotent returned error: %v", err)
	}
	pending, err := svc.Store().OutboxCount("pending")
	if err != nil {
		t.Fatalf("OutboxCount returned error: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending outbox = %d, want 1", pending)
	}
	_, _, err = svc.CreateOrderIdempotent("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99, "create-outbox")
	if err != nil {
		t.Fatalf("idempotent replay returned error: %v", err)
	}
	pending, _ = svc.Store().OutboxCount("pending")
	if pending != 1 {
		t.Fatalf("pending outbox after replay = %d, want 1", pending)
	}
}

func TestOutboxWorkerSuccessfulDeliveryUpdatesAttemptAndOutbox(t *testing.T) {
	var calls int64
	logic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"msg_ids":["msg-1"]}}`))
	}))
	defer logic.Close()

	svc := newTestOrderService(t)
	svc.pushClient = NewPushClientWithConfig(strings.TrimPrefix(logic.URL, "http://"), PushClientConfig{
		Timeout:     time.Second,
		MaxRetries:  0,
		BackoffBase: time.Millisecond,
	})
	_, notif, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	worker := NewOutboxWorker(svc, OutboxWorkerConfig{Enabled: true, BatchSize: 10, MaxRetries: 3, LockTTL: time.Minute})
	if err := worker.ProcessOnce(testContext(t)); err != nil {
		t.Fatalf("ProcessOnce returned error: %v", err)
	}
	sent, _ := svc.Store().OutboxCount("sent")
	if sent != 1 {
		t.Fatalf("sent outbox = %d, want 1", sent)
	}
	stored, err := svc.Store().GetNotification(notif.NotifyID)
	if err != nil {
		t.Fatalf("GetNotification: %v", err)
	}
	if stored.Status != "delivered" {
		t.Fatalf("notification status = %s, want delivered", stored.Status)
	}
	if atomic.LoadInt64(&calls) != 1 {
		t.Fatalf("logic calls = %d, want 1", calls)
	}
	stats := svc.GetStats()
	if stats.DeliveryPathDetail.LogicPush != 1 || stats.DeliveryPath.KafkaFallback != 0 {
		t.Fatalf("delivery path detail=%+v legacy=%+v", stats.DeliveryPathDetail, stats.DeliveryPath)
	}
}

func TestOutboxWorkerPersistsStructuredDeliveryPath(t *testing.T) {
	logic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"msg_ids":["msg-1"],"delivery_results":[{"msg_id":"msg-1","path":"grpc_direct","target_node":"comet-1","latency_ms":12.5,"attempt_no":1}]}}`))
	}))
	defer logic.Close()

	svc := newTestOrderService(t)
	svc.pushClient = NewPushClientWithConfig(strings.TrimPrefix(logic.URL, "http://"), PushClientConfig{
		Timeout:     time.Second,
		MaxRetries:  0,
		BackoffBase: time.Millisecond,
	})
	if _, _, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99); err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	worker := NewOutboxWorker(svc, OutboxWorkerConfig{Enabled: true, BatchSize: 10, MaxRetries: 3, LockTTL: time.Minute})
	if err := worker.ProcessOnce(testContext(t)); err != nil {
		t.Fatalf("ProcessOnce returned error: %v", err)
	}
	stats := svc.GetStats()
	if stats.DeliveryPathDetail.GrpcDirect != 1 || stats.DeliveryPathDetail.LogicPush != 0 {
		t.Fatalf("delivery detail = %+v, want grpc_direct only", stats.DeliveryPathDetail)
	}
}

func TestOutboxWorkerTreatsFailedDeliveryResultAsFailure(t *testing.T) {
	logic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"msg_ids":["msg-1"],"delivery_results":[{"msg_id":"msg-1","path":"failed","error_code":"reliable_enqueue_failed","error_message":"kafka down","latency_ms":3.5,"attempt_no":1}]}}`))
	}))
	defer logic.Close()

	svc := newTestOrderService(t)
	svc.pushClient = NewPushClientWithConfig(strings.TrimPrefix(logic.URL, "http://"), PushClientConfig{
		Timeout:     time.Second,
		MaxRetries:  0,
		BackoffBase: time.Millisecond,
	})
	if _, _, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99); err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	worker := NewOutboxWorker(svc, OutboxWorkerConfig{Enabled: true, BatchSize: 10, MaxRetries: 3, LockTTL: time.Minute})
	if err := worker.ProcessOnce(testContext(t)); err != nil {
		t.Fatalf("ProcessOnce returned error: %v", err)
	}
	sent, _ := svc.Store().OutboxCount("sent")
	failed, _ := svc.Store().OutboxCount("failed")
	if sent != 0 || failed != 1 {
		t.Fatalf("outbox sent=%d failed=%d, want 0/1", sent, failed)
	}
	dlq, err := svc.ListDLQ()
	if err != nil {
		t.Fatalf("ListDLQ returned error: %v", err)
	}
	if len(dlq) != 0 {
		t.Fatalf("dlq len = %d, want retry instead of terminal dlq", len(dlq))
	}
	stats := svc.GetStats()
	if stats.DeliveryPathDetail.Failed != 1 {
		t.Fatalf("delivery detail = %+v, want failed path", stats.DeliveryPathDetail)
	}
}

func TestPushClientCircuitBreakerOpens(t *testing.T) {
	logic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporary", http.StatusInternalServerError)
	}))
	defer logic.Close()
	client := NewPushClientWithConfig(strings.TrimPrefix(logic.URL, "http://"), PushClientConfig{
		Timeout:                 time.Second,
		MaxRetries:              0,
		BackoffBase:             time.Millisecond,
		CircuitFailureThreshold: 1,
		CircuitOpenInterval:     time.Minute,
	})
	_, _ = client.PushJSONToUsers(1, []int64{1001}, map[string]string{"hello": "world"})
	_, err := client.PushJSONToUsers(1, []int64{1001}, map[string]string{"hello": "world"})
	var pe *PushError
	if !errors.As(err, &pe) || pe.Type != "circuit_open" {
		t.Fatalf("second push error = %v, want circuit_open", err)
	}
}

func TestPushClientTemporaryErrorRetries(t *testing.T) {
	var calls int64
	logic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&calls, 1) == 1 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"msg_ids":["msg-1"]}}`))
	}))
	defer logic.Close()

	client := NewPushClientWithConfig(strings.TrimPrefix(logic.URL, "http://"), PushClientConfig{
		Timeout:     time.Second,
		MaxRetries:  1,
		BackoffBase: time.Millisecond,
	})
	if _, err := client.PushJSONToUsers(1, []int64{1001}, map[string]string{"hello": "world"}); err != nil {
		t.Fatalf("PushJSONToUsers returned error: %v", err)
	}
	if atomic.LoadInt64(&calls) != 2 {
		t.Fatalf("logic calls = %d, want 2", calls)
	}
}

func TestScenarioRunStatsAreIsolatedByRunID(t *testing.T) {
	var calls int64
	logic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if atomic.AddInt64(&calls, 1) == 1 {
			_, _ = w.Write([]byte(`{"code":0,"data":{"msg_ids":["msg-1"],"delivery_results":[{"msg_id":"msg-1","path":"grpc_direct","target_node":"comet-1","latency_ms":2,"attempt_no":1}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"msg_ids":["msg-2"],"delivery_results":[{"msg_id":"msg-2","path":"failed","error_code":"http_4xx","error_message":"bad target","latency_ms":4,"attempt_no":1}]}}`))
	}))
	defer logic.Close()

	svc := newTestOrderService(t)
	svc.pushClient = NewPushClientWithConfig(strings.TrimPrefix(logic.URL, "http://"), PushClientConfig{
		Timeout:     time.Second,
		MaxRetries:  0,
		BackoffBase: time.Millisecond,
	})
	worker := NewOutboxWorker(svc, OutboxWorkerConfig{Enabled: true, BatchSize: 10, MaxRetries: 1, LockTTL: time.Minute})

	run1, err := svc.CreateScenarioRun("run-1", 1, 1)
	if err != nil {
		t.Fatalf("CreateScenarioRun run1: %v", err)
	}
	_, notif1, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder run1: %v", err)
	}
	if err := worker.ProcessOnce(testContext(t)); err != nil {
		t.Fatalf("ProcessOnce run1: %v", err)
	}
	if recorded, err := svc.RecordAckIdempotent(AckInput{NotifyID: notif1.NotifyID, MsgID: "msg-1", DeviceID: "dev-1"}, "ack-run-1"); err != nil || !recorded {
		t.Fatalf("RecordAck run1 recorded=%v err=%v", recorded, err)
	}
	if err := svc.StopScenarioRun(run1.RunID); err != nil {
		t.Fatalf("StopScenarioRun run1: %v", err)
	}

	run2, err := svc.CreateScenarioRun("run-2", 1, 1)
	if err != nil {
		t.Fatalf("CreateScenarioRun run2: %v", err)
	}
	if _, _, err := svc.CreateOrder("1002", []model.OrderItem{{ProductName: "case", Quantity: 1, Price: 9}}, 9); err != nil {
		t.Fatalf("CreateOrder run2: %v", err)
	}
	if err := worker.ProcessOnce(testContext(t)); err != nil {
		t.Fatalf("ProcessOnce run2: %v", err)
	}

	got1, err := svc.GetScenarioRun(run1.RunID)
	if err != nil {
		t.Fatalf("GetScenarioRun run1: %v", err)
	}
	got2, err := svc.GetScenarioRun(run2.RunID)
	if err != nil {
		t.Fatalf("GetScenarioRun run2: %v", err)
	}
	if got1.GeneratedOrders != 1 || got1.SentCount != 1 || got1.AckedCount != 1 || got1.FailedCount != 0 || got1.DLQCount != 0 {
		t.Fatalf("run1 counters = %+v", got1)
	}
	if got2.GeneratedOrders != 1 || got2.SentCount != 0 || got2.AckedCount != 0 || got2.FailedCount != 1 || got2.DLQCount != 1 {
		t.Fatalf("run2 counters = %+v", got2)
	}
	if got1.DeliveryPathDetail.GrpcDirect != 1 || got1.DeliveryPathDetail.Failed != 0 {
		t.Fatalf("run1 delivery detail = %+v", got1.DeliveryPathDetail)
	}
	if got2.DeliveryPathDetail.Failed != 1 || got2.DeliveryPathDetail.GrpcDirect != 0 {
		t.Fatalf("run2 delivery detail = %+v", got2.DeliveryPathDetail)
	}
	if got1.P95LatencyMs <= 0 || got1.P99LatencyMs <= 0 {
		t.Fatalf("run1 latency p95=%f p99=%f, want positive", got1.P95LatencyMs, got1.P99LatencyMs)
	}
	if len(got1.RecentEvents) == 0 || len(got2.RecentEvents) == 0 {
		t.Fatalf("recent events run1=%d run2=%d, want non-empty", len(got1.RecentEvents), len(got2.RecentEvents))
	}
}

func TestOutboxWorkerMovesToDLQAfterMaxRetries(t *testing.T) {
	logic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer logic.Close()

	svc := newTestOrderService(t)
	svc.pushClient = NewPushClientWithConfig(strings.TrimPrefix(logic.URL, "http://"), PushClientConfig{
		Timeout:     time.Second,
		MaxRetries:  0,
		BackoffBase: time.Millisecond,
	})
	_, _, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	worker := NewOutboxWorker(svc, OutboxWorkerConfig{Enabled: true, BatchSize: 10, MaxRetries: 1, LockTTL: time.Minute})
	if err := worker.ProcessOnce(testContext(t)); err != nil {
		t.Fatalf("ProcessOnce returned error: %v", err)
	}
	dlq, err := svc.ListDLQ()
	if err != nil {
		t.Fatalf("ListDLQ returned error: %v", err)
	}
	if len(dlq) != 1 {
		t.Fatalf("dlq len = %d, want 1", len(dlq))
	}
}

func TestFlashSaleTargetedNotificationPersists(t *testing.T) {
	svc := newTestOrderService(t)
	flash := NewFlashSaleService(nil, svc.GetStatsCollector())
	flash.SetOrderService(svc)
	if _, err := flash.CreateFlashSale("promo", "desc", []string{"1001", "1002"}); err != nil {
		t.Fatalf("CreateFlashSale returned error: %v", err)
	}
	count, err := svc.Store().NotificationCount()
	if err != nil {
		t.Fatalf("NotificationCount returned error: %v", err)
	}
	if count != 2 {
		t.Fatalf("notification count = %d, want 2", count)
	}
	stats := svc.GetStats()
	if stats.NotificationsByType[string(model.NotifyFlashSale)] != 2 {
		t.Fatalf("flash_sale stats = %+v", stats.NotificationsByType)
	}
}

func TestFlashSaleLargeBatchPersistsOutbox(t *testing.T) {
	svc := newTestOrderService(t)
	flash := NewFlashSaleService(nil, svc.GetStatsCollector())
	flash.SetOrderService(svc)
	targets := make([]string, 0, 250)
	for i := 0; i < 250; i++ {
		targets = append(targets, strconv.Itoa(1000+i))
	}
	if _, err := flash.CreateFlashSale("promo", "desc", targets); err != nil {
		t.Fatalf("CreateFlashSale returned error: %v", err)
	}
	count, err := svc.Store().NotificationCount()
	if err != nil {
		t.Fatalf("NotificationCount returned error: %v", err)
	}
	if count != int64(len(targets)) {
		t.Fatalf("notification count = %d, want %d", count, len(targets))
	}
	pending, err := svc.Store().OutboxCount("pending")
	if err != nil {
		t.Fatalf("OutboxCount returned error: %v", err)
	}
	if pending != int64(len(targets)) {
		t.Fatalf("pending outbox = %d, want %d", pending, len(targets))
	}
}

func TestFlashSaleBroadcastPersistsRoomOutbox(t *testing.T) {
	var roomCalls int64
	logic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/goim/push/room" {
			t.Fatalf("unexpected logic path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("room"); got != "flash_sale_all" {
			t.Fatalf("room = %q, want flash_sale_all", got)
		}
		atomic.AddInt64(&roomCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"msg_id":"room-msg"}}`))
	}))
	defer logic.Close()

	svc := newTestOrderService(t)
	svc.pushClient = NewPushClientWithConfig(strings.TrimPrefix(logic.URL, "http://"), PushClientConfig{
		Timeout:     time.Second,
		MaxRetries:  0,
		BackoffBase: time.Millisecond,
	})
	flash := NewFlashSaleService(nil, svc.GetStatsCollector())
	flash.SetOrderService(svc)
	if _, err := flash.CreateFlashSale("promo", "desc", nil); err != nil {
		t.Fatalf("CreateFlashSale returned error: %v", err)
	}
	count, err := svc.Store().NotificationCount()
	if err != nil {
		t.Fatalf("NotificationCount returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("notification count = %d, want 1", count)
	}
	pending, err := svc.Store().OutboxCount("pending")
	if err != nil {
		t.Fatalf("OutboxCount returned error: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending outbox = %d, want 1", pending)
	}
	worker := NewOutboxWorker(svc, OutboxWorkerConfig{Enabled: true, BatchSize: 10, MaxRetries: 1, LockTTL: time.Minute})
	if err := worker.ProcessOnce(testContext(t)); err != nil {
		t.Fatalf("ProcessOnce returned error: %v", err)
	}
	if atomic.LoadInt64(&roomCalls) != 1 {
		t.Fatalf("room push calls = %d, want 1", roomCalls)
	}
	stats := svc.GetStats()
	if stats.DeliveryPathDetail.LogicPush != 1 {
		t.Fatalf("delivery detail = %+v, want logic_push broadcast", stats.DeliveryPathDetail)
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}
