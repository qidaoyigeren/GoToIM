package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/store"
)

func newTestOrderService(t *testing.T) *OrderNotifyService {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	svc := NewOrderNotifyServiceWithStore(nil, st)
	t.Cleanup(func() { _ = svc.Close() })
	return svc
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

func TestStatusChangeCreatesEventAndNotification(t *testing.T) {
	svc := newTestOrderService(t)
	order, _, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}

	updated, notif, err := svc.ChangeOrderStatus(order.OrderID, model.OrderPaid, nil)
	if err != nil {
		t.Fatalf("ChangeOrderStatus returned error: %v", err)
	}
	if updated.Status != model.OrderPaid {
		t.Fatalf("status = %s, want paid", updated.Status)
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

func TestStatusChangeIdempotencyDoesNotCreateDuplicateEventOrNotification(t *testing.T) {
	svc := newTestOrderService(t)
	order, _, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}

	_, firstNotif, err := svc.ChangeOrderStatusIdempotent(order.OrderID, model.OrderPaid, nil, "status-1")
	if err != nil {
		t.Fatalf("first ChangeOrderStatusIdempotent returned error: %v", err)
	}
	_, secondNotif, err := svc.ChangeOrderStatusIdempotent(order.OrderID, model.OrderPaid, nil, "status-1")
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

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}
