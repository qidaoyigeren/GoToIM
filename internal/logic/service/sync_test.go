package service

import (
	"context"
	"testing"
	"time"
)

func newTestSyncService() (*SyncService, *mockMessageDAO, *mockSessionDAO, *mockPushDAO, *mockCometPusher) {
	msgDAO := newMockMessageDAO()
	sessDAO := newMockSessionDAO()
	pushDAO := newMockPushDAO()
	pusher := newMockCometPusher()

	sessMgr := NewSessionManager(sessDAO, 10*time.Minute)
	ackSvc := NewAckService(msgDAO, pushDAO)
	pushSvc := NewPushService(pushDAO, msgDAO, sessMgr, ackSvc, pusher)
	syncSvc := NewSyncService(msgDAO, sessMgr, pushSvc)
	return syncSvc, msgDAO, sessDAO, pushDAO, pusher
}

func TestSyncService_OnUserOnline_NoOffline(t *testing.T) {
	syncSvc, _, _, pushDAO, pusher := newTestSyncService()
	ctx := context.Background()

	err := syncSvc.OnUserOnline(ctx, 1001, 0)
	if err != nil {
		t.Fatalf("OnUserOnline: %v", err)
	}

	// No pushes should happen
	pusher.mu.Lock()
	if len(pusher.calls) != 0 {
		t.Errorf("pusher calls = %d, want 0", len(pusher.calls))
	}
	pusher.mu.Unlock()

	pushDAO.mu.Lock()
	if len(pushDAO.pushCalls) != 0 {
		t.Errorf("kafka calls = %d, want 0", len(pushDAO.pushCalls))
	}
	pushDAO.mu.Unlock()
}

func TestSyncService_OnUserOnline_WithOffline(t *testing.T) {
	syncSvc, msgDAO, sessDAO, _, pusher := newTestSyncService()
	ctx := context.Background()

	// Add offline messages
	msgDAO.AddToOfflineQueue(ctx, 1001, "msg-1", 1.0)
	msgDAO.AddToOfflineQueue(ctx, 1001, "msg-2", 2.0)
	msgDAO.SetMessageStatus(ctx, "msg-1", map[string]interface{}{"status": MsgStatusDelivered, "from_uid": 100})
	msgDAO.SetMessageStatus(ctx, "msg-2", map[string]interface{}{"status": MsgStatusDelivered, "from_uid": 101})

	// User must have sessions for push to happen
	sessDAO.AddSession(ctx, "s1", 1001, "k1", "d1", "android", "comet-1")

	err := syncSvc.OnUserOnline(ctx, 1001, 0)
	if err != nil {
		t.Fatalf("OnUserOnline: %v", err)
	}

	// Should have pushed sync messages
	pusher.mu.Lock()
	if len(pusher.calls) == 0 {
		t.Error("expected sync pushes, got 0")
	}
	pusher.mu.Unlock()
}

func TestSyncService_GetOfflineMessages(t *testing.T) {
	syncSvc, msgDAO, _, _, _ := newTestSyncService()
	ctx := context.Background()

	msgDAO.AddToOfflineQueue(ctx, 1001, "msg-1", 1.0)
	msgDAO.AddToOfflineQueue(ctx, 1001, "msg-2", 2.0)
	msgDAO.SetMessageStatus(ctx, "msg-1", map[string]interface{}{"status": MsgStatusDelivered, "from_uid": 100})
	msgDAO.SetMessageStatus(ctx, "msg-2", map[string]interface{}{"status": MsgStatusDelivered, "from_uid": 101})

	reply, err := syncSvc.GetOfflineMessages(ctx, 1001, 0, 100)
	if err != nil {
		t.Fatalf("GetOfflineMessages: %v", err)
	}

	if len(reply.Messages) != 2 {
		t.Errorf("message count = %d, want 2", len(reply.Messages))
	}
}

func TestSyncService_GetOfflineMessages_AckedSkipped(t *testing.T) {
	syncSvc, msgDAO, _, _, _ := newTestSyncService()
	ctx := context.Background()

	msgDAO.AddToOfflineQueue(ctx, 1001, "msg-1", 1.0)
	msgDAO.AddToOfflineQueue(ctx, 1001, "msg-2", 2.0)
	msgDAO.SetMessageStatus(ctx, "msg-1", map[string]interface{}{"status": MsgStatusAcked, "from_uid": 100})
	msgDAO.SetMessageStatus(ctx, "msg-2", map[string]interface{}{"status": MsgStatusDelivered, "from_uid": 101})

	reply, err := syncSvc.GetOfflineMessages(ctx, 1001, 0, 100)
	if err != nil {
		t.Fatalf("GetOfflineMessages: %v", err)
	}

	// msg-1 is acked, should be skipped
	if len(reply.Messages) != 1 {
		t.Errorf("message count = %d, want 1 (acked msg skipped)", len(reply.Messages))
	}
	if len(reply.Messages) > 0 && reply.Messages[0].MsgID != "msg-2" {
		t.Errorf("remaining msg = %q, want %q", reply.Messages[0].MsgID, "msg-2")
	}
}
