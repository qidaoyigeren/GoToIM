package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

func newTestStore(t *testing.T) *SQLStore {
	t.Helper()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func sampleOrder(id string) *model.Order {
	now := time.Now()
	return &model.Order{
		OrderID: id, UserID: "1001", Status: model.OrderCreated,
		Items: []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}},
		Total: 99, CreatedAt: now, UpdatedAt: now,
	}
}

func sampleNotification(id string) *model.Notification {
	now := time.Now()
	return &model.Notification{
		NotifyID: id, UserID: "1001", Type: model.NotifyOrderStatus,
		BusinessType: "order", EventType: "created", Title: "created", Content: "created",
		OrderID: "ord-1", Status: "pending", Priority: "normal", TTLSeconds: 600,
		AckPolicy: "all_devices", ExpectedAckCount: 2, BusinessAckStatus: "pending",
		CreatedAt: now, UpdatedAt: now,
	}
}

func sampleOutbox(id, notifyID string) *model.NotificationOutbox {
	now := time.Now()
	return &model.NotificationOutbox{
		OutboxID: id, NotifyID: notifyID, UserID: "1001", OrderID: "ord-1",
		BusinessType: "order", EventType: "created", PayloadJSON: `{"notify_id":"` + notifyID + `"}`,
		Priority: "normal", TTLSeconds: 600, Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
}

func TestStoreSchemaOrderNotificationOutboxAndIdempotency(t *testing.T) {
	st := newTestStore(t)
	order := sampleOrder("ord-1")
	notif := sampleNotification("ntf-1")
	outbox := sampleOutbox("obx-1", notif.NotifyID)
	snapshot, _ := json.Marshal(map[string]string{"order_id": order.OrderID})

	if err := st.CreateOrderNotificationOutbox(order, notif, outbox, "order_create", "key-1", "order", order.OrderID, snapshot); err != nil {
		t.Fatalf("CreateOrderNotificationOutbox: %v", err)
	}
	got, err := st.GetOrder(order.OrderID)
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if got.UserID != "1001" || got.Status != model.OrderCreated {
		t.Fatalf("stored order = %+v", got)
	}
	claimed, err := st.ClaimOutbox("worker-1", 10, time.Minute)
	if err != nil {
		t.Fatalf("ClaimOutbox: %v", err)
	}
	if len(claimed) != 1 || claimed[0].OutboxID != outbox.OutboxID {
		t.Fatalf("claimed = %+v", claimed)
	}
	replay, ok, err := st.GetIdempotency("order_create", "key-1")
	if err != nil || !ok || len(replay) == 0 {
		t.Fatalf("GetIdempotency ok=%v len=%d err=%v", ok, len(replay), err)
	}
}

func TestStoreStatusEventTransactionAndOutboxRetry(t *testing.T) {
	st := newTestStore(t)
	order := sampleOrder("ord-1")
	if err := st.InsertOrder(order); err != nil {
		t.Fatalf("InsertOrder: %v", err)
	}
	now := time.Now()
	order.Status = model.OrderPaid
	order.UpdatedAt = now
	notif := sampleNotification("ntf-2")
	notif.EventType = "paid"
	outbox := sampleOutbox("obx-2", notif.NotifyID)
	event := &model.OrderStatusEvent{EventID: "evt-1", OrderID: order.OrderID, FromStatus: model.OrderCreated, ToStatus: model.OrderPaid, CreatedAt: now}
	if err := st.ChangeOrderNotificationOutbox(order, event, notif, outbox, "", "", "", "", nil); err != nil {
		t.Fatalf("ChangeOrderNotificationOutbox: %v", err)
	}
	events, _ := st.StatusEventCount()
	if events != 1 {
		t.Fatalf("events = %d, want 1", events)
	}
	attemptAt := time.Now()
	err := st.MarkOutboxRetry(outbox.OutboxID, 1, attemptAt.Add(-time.Second), "temporary", &model.NotificationAttempt{
		AttemptID: "atm-1", NotifyID: notif.NotifyID, Channel: "logic_push", Target: "1001",
		Status: "direct_failed", ErrorMessage: "temporary", StartedAt: attemptAt, FinishedAt: &attemptAt,
	})
	if err != nil {
		t.Fatalf("MarkOutboxRetry: %v", err)
	}
	claimed, err := st.ClaimOutbox("worker-2", 10, time.Minute)
	if err != nil {
		t.Fatalf("ClaimOutbox retry: %v", err)
	}
	if len(claimed) != 1 || claimed[0].RetryCount != 1 {
		t.Fatalf("retry claimed = %+v", claimed)
	}
}

func TestStoreMultiDeviceAckAndStats(t *testing.T) {
	st := newTestStore(t)
	order := sampleOrder("ord-1")
	notif := sampleNotification("ntf-1")
	outbox := sampleOutbox("obx-1", notif.NotifyID)
	if err := st.CreateOrderNotificationOutbox(order, notif, outbox, "", "", "", "", nil); err != nil {
		t.Fatalf("CreateOrderNotificationOutbox: %v", err)
	}
	if _, err := st.RecordAck(&model.NotificationAck{AckID: "ack-1", NotifyID: notif.NotifyID, UserID: "1001", MsgID: "m1", DeviceID: "d1", LatencyMs: 5, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("RecordAck #1: %v", err)
	}
	if recorded, err := st.RecordAck(&model.NotificationAck{AckID: "ack-dup", NotifyID: notif.NotifyID, UserID: "1001", MsgID: "m1", DeviceID: "d1", LatencyMs: 6, CreatedAt: time.Now()}); err != nil || recorded {
		t.Fatalf("duplicate ack recorded=%v err=%v, want false nil", recorded, err)
	}
	if recorded, err := st.RecordAck(&model.NotificationAck{AckID: "ack-2", NotifyID: notif.NotifyID, UserID: "1001", MsgID: "m2", DeviceID: "d2", LatencyMs: 7, CreatedAt: time.Now()}); err != nil || !recorded {
		t.Fatalf("second device ack recorded=%v err=%v, want true nil", recorded, err)
	}
	acks, _ := st.AckCount()
	if acks != 2 {
		t.Fatalf("acks = %d, want 2", acks)
	}
	stats, err := st.PlatformStats(1, 1, 0)
	if err != nil {
		t.Fatalf("PlatformStats: %v", err)
	}
	if stats.AckPolicySatisfiedRate != 1 {
		t.Fatalf("AckPolicySatisfiedRate = %f, want 1", stats.AckPolicySatisfiedRate)
	}
}

func TestStoreDLQReplayResolveAndStats(t *testing.T) {
	st := newTestStore(t)
	order := sampleOrder("ord-1")
	notif := sampleNotification("ntf-1")
	outbox := sampleOutbox("obx-1", notif.NotifyID)
	if err := st.CreateOrderNotificationOutbox(order, notif, outbox, "", "", "", "", nil); err != nil {
		t.Fatalf("CreateOrderNotificationOutbox: %v", err)
	}
	if err := st.MoveOutboxToDLQ(outbox, &model.NotificationDLQ{
		DLQID: "dlq-1", NotifyID: notif.NotifyID, OutboxID: outbox.OutboxID, UserID: notif.UserID,
		Reason: "http_4xx", LastError: "bad request", PayloadJSON: outbox.PayloadJSON, RetryCount: 3, CreatedAt: time.Now(),
	}, nil, "dlq"); err != nil {
		t.Fatalf("MoveOutboxToDLQ: %v", err)
	}
	items, err := st.ListDLQ(10)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListDLQ len=%d err=%v", len(items), err)
	}
	stats, err := st.PlatformStats(0, 0, 0)
	if err != nil {
		t.Fatalf("PlatformStats: %v", err)
	}
	if stats.DLQCount != 1 {
		t.Fatalf("DLQCount = %d, want 1", stats.DLQCount)
	}
	if err := st.ReplayDLQ("dlq-1", "tester"); err != nil {
		t.Fatalf("ReplayDLQ: %v", err)
	}
	claimed, err := st.ClaimOutbox("worker-1", 10, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox after replay len=%d err=%v", len(claimed), err)
	}
	if err := st.ResolveDLQ("dlq-1", "tester", "checked"); err != nil {
		t.Fatalf("ResolveDLQ: %v", err)
	}
}
