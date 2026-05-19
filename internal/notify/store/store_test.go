package store

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

func openMySQLTest(t *testing.T) (*SQLStore, func()) {
	t.Helper()
	dsn := os.Getenv("GOIM_NOTIFY_MYSQL_DSN")
	if dsn == "" {
		t.Skip("GOIM_NOTIFY_MYSQL_DSN not set, skipping MySQL test")
	}
	st, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st, func() { _ = st.Close() }
}

func newTestStore(t *testing.T) *SQLStore {
	t.Helper()
	st, cleanup := openMySQLTest(t)
	t.Cleanup(cleanup)
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

func TestStoreCampaignTargetStatusTransitions(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	if err := st.InsertCampaign("camp-1", "promo", "desc", "flash_sale", 1, "", now); err != nil {
		t.Fatalf("InsertCampaign: %v", err)
	}
	notif := sampleNotification("ntf-campaign-1")
	notif.Type = model.NotifyFlashSale
	notif.BusinessType = "flash_sale"
	notif.EventType = "notify"
	notif.OrderID = "camp-1"
	notif.AckPolicy = "any_device"
	notif.ExpectedAckCount = 1
	outbox := sampleOutbox("obx-campaign-1", notif.NotifyID)
	outbox.OrderID = "camp-1"
	outbox.BusinessType = "flash_sale"
	outbox.EventType = "notify"
	if err := st.CreateNotificationOutbox(notif, outbox, "", "", "", "", nil); err != nil {
		t.Fatalf("CreateNotificationOutbox: %v", err)
	}
	if err := st.InsertCampaignTarget("camp-1", "1001", notif.NotifyID, "pending", now); err != nil {
		t.Fatalf("InsertCampaignTarget: %v", err)
	}
	if status, err := st.GetCampaignTargetStatus("camp-1", "1001"); err != nil || status != "pending" {
		t.Fatalf("initial campaign target status=%q err=%v, want pending", status, err)
	}
	attemptAt := time.Now()
	if err := st.MarkOutboxRetry(outbox.OutboxID, 1, attemptAt.Add(-time.Second), "temporary", &model.NotificationAttempt{
		AttemptID: "atm-campaign-1", NotifyID: notif.NotifyID, Channel: "logic_push", Target: "1001",
		Status: "direct_failed", Path: "failed", ErrorCode: "http_5xx", ErrorMessage: "temporary",
		StartedAt: attemptAt, FinishedAt: &attemptAt,
	}); err != nil {
		t.Fatalf("MarkOutboxRetry: %v", err)
	}
	if status, err := st.GetCampaignTargetStatus("camp-1", "1001"); err != nil || status != "retrying" {
		t.Fatalf("retry campaign target status=%q err=%v, want retrying", status, err)
	}
	finished := time.Now()
	if err := st.MarkOutboxSent(outbox.OutboxID, &model.NotificationAttempt{
		AttemptID: "atm-campaign-2", NotifyID: notif.NotifyID, Channel: "grpc_direct", Target: "1001",
		Status: "direct_sent", Path: "grpc_direct", StartedAt: finished, FinishedAt: &finished,
	}, finished); err != nil {
		t.Fatalf("MarkOutboxSent: %v", err)
	}
	if status, err := st.GetCampaignTargetStatus("camp-1", "1001"); err != nil || status != "sent" {
		t.Fatalf("sent campaign target status=%q err=%v, want sent", status, err)
	}
	if recorded, err := st.RecordAck(&model.NotificationAck{
		AckID: "ack-campaign-1", NotifyID: notif.NotifyID, UserID: "1001", MsgID: "msg-1",
		DeviceID: "dev-1", LatencyMs: 1, CreatedAt: time.Now(),
	}); err != nil || !recorded {
		t.Fatalf("RecordAck recorded=%v err=%v", recorded, err)
	}
	if status, err := st.GetCampaignTargetStatus("camp-1", "1001"); err != nil || status != "acked" {
		t.Fatalf("acked campaign target status=%q err=%v, want acked", status, err)
	}

	dlqNotif := sampleNotification("ntf-campaign-dlq")
	dlqNotif.Type = model.NotifyFlashSale
	dlqNotif.BusinessType = "flash_sale"
	dlqNotif.EventType = "notify"
	dlqNotif.OrderID = "camp-1"
	dlqOutbox := sampleOutbox("obx-campaign-dlq", dlqNotif.NotifyID)
	dlqOutbox.OrderID = "camp-1"
	dlqOutbox.BusinessType = "flash_sale"
	dlqOutbox.EventType = "notify"
	if err := st.CreateNotificationOutbox(dlqNotif, dlqOutbox, "", "", "", "", nil); err != nil {
		t.Fatalf("CreateNotificationOutbox dlq: %v", err)
	}
	if err := st.InsertCampaignTarget("camp-1", "1002", dlqNotif.NotifyID, "pending", now); err != nil {
		t.Fatalf("InsertCampaignTarget dlq: %v", err)
	}
	if err := st.MoveOutboxToDLQ(dlqOutbox, &model.NotificationDLQ{
		DLQID: "dlq-campaign-1", NotifyID: dlqNotif.NotifyID, OutboxID: dlqOutbox.OutboxID, UserID: "1002",
		Reason: "http_4xx", LastError: "bad request", PayloadJSON: dlqOutbox.PayloadJSON, RetryCount: 1, CreatedAt: time.Now(),
	}, nil, "dlq"); err != nil {
		t.Fatalf("MoveOutboxToDLQ: %v", err)
	}
	if status, err := st.GetCampaignTargetStatus("camp-1", "1002"); err != nil || status != "dlq" {
		t.Fatalf("dlq campaign target status=%q err=%v, want dlq", status, err)
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

func TestStoreACKPolicies(t *testing.T) {
	st := newTestStore(t)
	cases := []struct {
		name      string
		policy    string
		targets   []string
		primary   string
		acks      []string
		wantState string
	}{
		{name: "none", policy: "none", wantState: "satisfied"},
		{name: "best", policy: "best_effort", wantState: "satisfied"},
		{name: "any", policy: "any_device", acks: []string{"d2"}, wantState: "satisfied"},
		{name: "all", policy: "all_devices", targets: []string{"d1", "d2"}, acks: []string{"d1", "d2"}, wantState: "satisfied"},
		{name: "primary", policy: "primary_device", primary: "d1", acks: []string{"d2", "d1"}, wantState: "satisfied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notif := sampleNotification("ntf-policy-" + tc.name)
			notif.AckPolicy = tc.policy
			notif.TargetDeviceIDs = tc.targets
			notif.PrimaryDeviceID = tc.primary
			notif.ExpectedAckCount = int64(len(tc.targets))
			if notif.ExpectedAckCount == 0 {
				notif.ExpectedAckCount = 1
			}
			if err := st.InsertNotification(notif); err != nil {
				t.Fatalf("InsertNotification: %v", err)
			}
			for i, dev := range tc.acks {
				if _, err := st.RecordAck(&model.NotificationAck{
					AckID: "ack-policy-" + tc.name + "-" + string(rune('a'+i)), NotifyID: notif.NotifyID,
					UserID: notif.UserID, MsgID: "msg-" + tc.name + "-" + string(rune('a'+i)),
					DeviceID: dev, LatencyMs: 1, CreatedAt: time.Now(),
				}); err != nil {
					t.Fatalf("RecordAck: %v", err)
				}
			}
			got, err := st.GetNotification(notif.NotifyID)
			if err != nil {
				t.Fatalf("GetNotification: %v", err)
			}
			if got.BusinessAckStatus != tc.wantState {
				t.Fatalf("BusinessAckStatus = %s, want %s", got.BusinessAckStatus, tc.wantState)
			}
		})
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

func TestMySQLIntegrationNotifyStore(t *testing.T) {
	st, cleanup := openMySQLTest(t)
	t.Cleanup(cleanup)

	suffix := time.Now().Format("20060102150405.000000000")
	order := sampleOrder("mysql-ord-" + suffix)
	notif := sampleNotification("mysql-ntf-" + suffix)
	notif.OrderID = order.OrderID
	notif.AckPolicy = "any_device"
	notif.ExpectedAckCount = 1
	outbox := sampleOutbox("mysql-obx-"+suffix, notif.NotifyID)
	outbox.OrderID = order.OrderID
	if err := st.CreateOrderNotificationOutbox(order, notif, outbox, "mysql_create", "key-"+suffix, "order", order.OrderID, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("CreateOrderNotificationOutbox: %v", err)
	}
	claimed, err := st.ClaimOutbox("mysql-worker", 10, time.Minute)
	if err != nil {
		t.Fatalf("ClaimOutbox: %v", err)
	}
	if len(claimed) == 0 {
		t.Fatal("ClaimOutbox returned no rows")
	}
	finished := time.Now()
	if err := st.MarkOutboxSent(outbox.OutboxID, &model.NotificationAttempt{
		AttemptID: "mysql-atm-" + suffix, NotifyID: notif.NotifyID, Channel: "grpc_direct", Target: notif.UserID,
		Status: "direct_sent", Path: "grpc_direct", TargetNode: "comet-1", LatencyMs: 1.2, AttemptNo: 1,
		StartedAt: finished, FinishedAt: &finished,
	}, finished); err != nil {
		t.Fatalf("MarkOutboxSent: %v", err)
	}
	if recorded, err := st.RecordAck(&model.NotificationAck{
		AckID: "mysql-ack-" + suffix, NotifyID: notif.NotifyID, UserID: notif.UserID,
		MsgID: "msg-" + suffix, DeviceID: "dev-1", LatencyMs: 2.3, CreatedAt: time.Now(),
	}); err != nil || !recorded {
		t.Fatalf("RecordAck recorded=%v err=%v", recorded, err)
	}

	dlqNotif := sampleNotification("mysql-ntf-dlq-" + suffix)
	dlqNotif.OrderID = order.OrderID
	dlqOutbox := sampleOutbox("mysql-obx-dlq-"+suffix, dlqNotif.NotifyID)
	if err := st.CreateNotificationOutbox(dlqNotif, dlqOutbox, "", "", "", "", nil); err != nil {
		t.Fatalf("CreateNotificationOutbox: %v", err)
	}
	if err := st.MoveOutboxToDLQ(dlqOutbox, &model.NotificationDLQ{
		DLQID: "mysql-dlq-" + suffix, NotifyID: dlqNotif.NotifyID, OutboxID: dlqOutbox.OutboxID, UserID: dlqNotif.UserID,
		Reason: "http_4xx", LastError: "bad request", PayloadJSON: dlqOutbox.PayloadJSON, RetryCount: 1, CreatedAt: time.Now(),
	}, nil, "dlq"); err != nil {
		t.Fatalf("MoveOutboxToDLQ: %v", err)
	}
	if err := st.ReplayDLQ("mysql-dlq-"+suffix, "mysql-test"); err != nil {
		t.Fatalf("ReplayDLQ: %v", err)
	}

	run := &model.ScenarioRun{RunID: "mysql-scn-" + suffix, Mode: "normal", Status: "running", QPS: 1, Users: 1, StartedAt: time.Now()}
	if err := st.CreateScenarioRun(run); err != nil {
		t.Fatalf("CreateScenarioRun: %v", err)
	}
	if err := st.IncrementScenarioRunCounters(run.RunID, 1, 2, 1, 1, 1, 1); err != nil {
		t.Fatalf("IncrementScenarioRunCounters: %v", err)
	}
	gotRun, err := st.GetScenarioRun(run.RunID)
	if err != nil {
		t.Fatalf("GetScenarioRun: %v", err)
	}
	if gotRun.GeneratedOrders != 1 || gotRun.GeneratedNotifications != 2 || gotRun.DLQCount != 1 {
		t.Fatalf("scenario counters = %+v", gotRun)
	}
}
