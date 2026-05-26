package service

import (
	"encoding/json"
	"testing"

	"github.com/IBM/sarama"
	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeAckBridgeMsgIDs(t *testing.T) {
	got := normalizeAckBridgeMsgIDs("m2", []string{"m1", "m2", "m1"})
	assert.Equal(t, []string{"m1", "m2"}, got)
}

func TestAckBridgeDuplicateDoesNotDoubleAckCount(t *testing.T) {
	svc := newTestOrderService(t)
	_, notif, err := svc.CreateOrder("ack-bridge-user", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"notify_id":  notif.NotifyID,
		"msg_id":     "msg-bridge-1",
		"status":     "acked",
		"device_id":  "dev-1",
		"session_id": "sess-1",
		"trace_id":   notif.TraceID,
	})
	handler := &ackBridgeHandler{svc: svc}
	handler.processBatch([]*sarama.ConsumerMessage{
		{Value: body, Offset: 1},
		{Value: body, Offset: 2},
	})

	stored, err := svc.Store().GetNotification(notif.NotifyID)
	if err != nil {
		t.Fatalf("GetNotification returned error: %v", err)
	}
	if stored.AckedCount != 1 {
		t.Fatalf("acked_count = %d, want 1", stored.AckedCount)
	}
	acks, err := svc.Store().ListACKsByNotifyID(notif.NotifyID)
	if err != nil {
		t.Fatalf("ListACKsByNotifyID returned error: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("ack rows = %d, want 1", len(acks))
	}
}

func TestAckBridgeProcessBatchReturnsWriteError(t *testing.T) {
	svc := newTestOrderService(t)
	_, notif, err := svc.CreateOrder("ack-bridge-error-user", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"notify_id": notif.NotifyID,
		"msg_id":    "msg-bridge-error",
		"status":    "acked",
	})
	handler := &ackBridgeHandler{svc: svc}
	if err := handler.processBatch([]*sarama.ConsumerMessage{{Value: body, Offset: 1}}); err == nil {
		t.Fatal("processBatch returned nil, want write error")
	}
}
