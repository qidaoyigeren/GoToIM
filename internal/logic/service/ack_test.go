package service

import (
	"context"
	"testing"
)

func newTestAckService() (*AckService, *mockMessageDAO) {
	mock := newMockMessageDAO()
	svc := NewAckService(mock, newMockPushDAO())
	return svc, mock
}

func TestAckService_HandleAck(t *testing.T) {
	svc, mock := newTestAckService()
	ctx := context.Background()

	// Track a message first
	svc.TrackMessage(ctx, "msg-1", 100, 200, 4, []byte("body"))

	// Handle ACK
	if err := svc.HandleAck(ctx, 200, "msg-1"); err != nil {
		t.Fatalf("HandleAck: %v", err)
	}

	// Verify status updated to acked
	status, _ := svc.GetMessageStatus(ctx, "msg-1")
	if status != MsgStatusAcked {
		t.Errorf("status = %q, want %q", status, MsgStatusAcked)
	}

	// Verify removed from offline queue
	size, _ := mock.GetOfflineQueueSize(ctx, 200)
	if size != 0 {
		t.Errorf("offline queue size = %d, want 0", size)
	}
}

func TestAckService_BatchHandleAck(t *testing.T) {
	svc, _ := newTestAckService()
	ctx := context.Background()

	svc.TrackMessage(ctx, "msg-1", 100, 200, 4, []byte("b1"))
	svc.TrackMessage(ctx, "msg-2", 100, 200, 4, []byte("b2"))

	acks := []struct {
		UID   int64
		MsgID string
	}{
		{UID: 200, MsgID: "msg-1"},
		{UID: 200, MsgID: "msg-2"},
	}
	if err := svc.BatchHandleAck(ctx, acks); err != nil {
		t.Fatalf("BatchHandleAck: %v", err)
	}

	s1, _ := svc.GetMessageStatus(ctx, "msg-1")
	s2, _ := svc.GetMessageStatus(ctx, "msg-2")
	if s1 != MsgStatusAcked {
		t.Errorf("msg-1 status = %q, want %q", s1, MsgStatusAcked)
	}
	if s2 != MsgStatusAcked {
		t.Errorf("msg-2 status = %q, want %q", s2, MsgStatusAcked)
	}
}

func TestAckService_TrackMessage(t *testing.T) {
	svc, _ := newTestAckService()
	ctx := context.Background()

	if err := svc.TrackMessage(ctx, "msg-1", 100, 200, 4, []byte("body")); err != nil {
		t.Fatalf("TrackMessage: %v", err)
	}

	status, _ := svc.GetMessageStatus(ctx, "msg-1")
	if status != MsgStatusPending {
		t.Errorf("status = %q, want %q", status, MsgStatusPending)
	}
}

func TestAckService_MarkDelivered(t *testing.T) {
	svc, _ := newTestAckService()
	ctx := context.Background()

	svc.TrackMessage(ctx, "msg-1", 100, 200, 4, []byte("body"))
	svc.MarkDelivered(ctx, "msg-1")

	status, _ := svc.GetMessageStatus(ctx, "msg-1")
	if status != MsgStatusDelivered {
		t.Errorf("status = %q, want %q", status, MsgStatusDelivered)
	}
}

func TestAckService_MarkFailed(t *testing.T) {
	svc, _ := newTestAckService()
	ctx := context.Background()

	svc.TrackMessage(ctx, "msg-1", 100, 200, 4, []byte("body"))
	svc.MarkFailed(ctx, "msg-1")

	status, _ := svc.GetMessageStatus(ctx, "msg-1")
	if status != MsgStatusFailed {
		t.Errorf("status = %q, want %q", status, MsgStatusFailed)
	}
}

func TestAckService_GetMessageStatus_NotFound(t *testing.T) {
	svc, _ := newTestAckService()
	ctx := context.Background()

	status, err := svc.GetMessageStatus(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetMessageStatus: %v", err)
	}
	if status != "" {
		t.Errorf("status = %q, want empty", status)
	}
}
