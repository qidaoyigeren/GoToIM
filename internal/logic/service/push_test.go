package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestPushService() (*PushService, *mockPushDAO, *mockMessageDAO, *mockSessionDAO, *mockCometPusher) {
	pushDAO := newMockPushDAO()
	msgDAO := newMockMessageDAO()
	sessDAO := newMockSessionDAO()
	pusher := newMockCometPusher()

	sessMgr := NewSessionManager(sessDAO, 10*time.Minute)
	ackSvc := NewAckService(msgDAO, pushDAO)
	pushSvc := NewPushService(pushDAO, msgDAO, sessMgr, ackSvc, pusher)
	return pushSvc, pushDAO, msgDAO, sessDAO, pusher
}

func TestPushService_PushToUser_OnlineDirect(t *testing.T) {
	pushSvc, pushDAO, msgDAO, sessDAO, pusher := newTestPushService()
	ctx := context.Background()

	// Set up online user
	sessDAO.AddSession(ctx, "s1", 1001, "k1", "d1", "android", "comet-1")

	err := pushSvc.PushToUser(ctx, "msg-1", 1001, 4, []byte("hello"), 1)
	if err != nil {
		t.Fatalf("PushToUser: %v", err)
	}

	// Direct push should have been called
	pusher.mu.Lock()
	if len(pusher.calls) != 1 {
		t.Errorf("direct push calls = %d, want 1", len(pusher.calls))
	}
	pusher.mu.Unlock()

	// No Kafka calls (direct push succeeded)
	pushDAO.mu.Lock()
	if len(pushDAO.pushCalls) != 0 {
		t.Errorf("kafka push calls = %d, want 0", len(pushDAO.pushCalls))
	}
	pushDAO.mu.Unlock()

	// Message should be marked delivered
	status, _ := msgDAO.GetMessageStatus(ctx, "msg-1")
	if status["status"] != MsgStatusDelivered {
		t.Errorf("status = %q, want %q", status["status"], MsgStatusDelivered)
	}
}

func TestPushService_PushToUser_OnlineDirectFail_FallbackKafka(t *testing.T) {
	pushSvc, pushDAO, _, sessDAO, pusher := newTestPushService()
	ctx := context.Background()

	// Set up online user but make direct push fail
	sessDAO.AddSession(ctx, "s1", 1001, "k1", "d1", "android", "comet-1")
	pusher.pushErr = errors.New("connection refused")

	err := pushSvc.PushToUser(ctx, "msg-1", 1001, 4, []byte("hello"), 1)
	if err != nil {
		t.Fatalf("PushToUser: %v", err)
	}

	// Direct push was attempted
	pusher.mu.Lock()
	if len(pusher.calls) != 1 {
		t.Errorf("direct push calls = %d, want 1", len(pusher.calls))
	}
	pusher.mu.Unlock()

	// Should have fallen back to Kafka
	pushDAO.mu.Lock()
	if len(pushDAO.pushCalls) != 1 {
		t.Errorf("kafka push calls = %d, want 1", len(pushDAO.pushCalls))
	}
	pushDAO.mu.Unlock()
}

func TestPushService_PushToUser_Offline(t *testing.T) {
	pushSvc, pushDAO, msgDAO, _, _ := newTestPushService()
	ctx := context.Background()

	// No sessions — user is offline
	err := pushSvc.PushToUser(ctx, "msg-1", 1001, 4, []byte("hello"), 1)
	if err != nil {
		t.Fatalf("PushToUser: %v", err)
	}

	// Should go to Kafka
	pushDAO.mu.Lock()
	if len(pushDAO.pushCalls) != 1 {
		t.Errorf("kafka push calls = %d, want 1", len(pushDAO.pushCalls))
	}
	pushDAO.mu.Unlock()

	// Should be in offline queue
	size, _ := msgDAO.GetOfflineQueueSize(ctx, 1001)
	if size != 1 {
		t.Errorf("offline queue size = %d, want 1", size)
	}
}

func TestPushService_PushToUser_AlreadyDelivered(t *testing.T) {
	pushSvc, pushDAO, msgDAO, _, pusher := newTestPushService()
	ctx := context.Background()

	// Pre-set message as acked
	msgDAO.SetMessageStatus(ctx, "msg-1", map[string]interface{}{"status": MsgStatusAcked})

	err := pushSvc.PushToUser(ctx, "msg-1", 1001, 4, []byte("hello"), 1)
	if err != nil {
		t.Fatalf("PushToUser: %v", err)
	}

	// No pushes should have happened
	pusher.mu.Lock()
	if len(pusher.calls) != 0 {
		t.Errorf("direct push calls = %d, want 0", len(pusher.calls))
	}
	pusher.mu.Unlock()

	pushDAO.mu.Lock()
	if len(pushDAO.pushCalls) != 0 {
		t.Errorf("kafka push calls = %d, want 0", len(pushDAO.pushCalls))
	}
	pushDAO.mu.Unlock()
}

func TestPushService_PushToRoom(t *testing.T) {
	pushSvc, pushDAO, _, _, _ := newTestPushService()
	ctx := context.Background()

	err := pushSvc.PushToRoom(ctx, 4, "room-1", []byte("room msg"))
	if err != nil {
		t.Fatalf("PushToRoom: %v", err)
	}

	pushDAO.mu.Lock()
	if len(pushDAO.roomCalls) != 1 {
		t.Errorf("room calls = %d, want 1", len(pushDAO.roomCalls))
	}
	if pushDAO.roomCalls[0].room != "room-1" {
		t.Errorf("room = %q, want %q", pushDAO.roomCalls[0].room, "room-1")
	}
	pushDAO.mu.Unlock()
}

func TestPushService_PushAll(t *testing.T) {
	pushSvc, pushDAO, _, _, _ := newTestPushService()
	ctx := context.Background()

	err := pushSvc.PushAll(ctx, 4, 100, []byte("broadcast"))
	if err != nil {
		t.Fatalf("PushAll: %v", err)
	}

	pushDAO.mu.Lock()
	if len(pushDAO.broadcastCalls) != 1 {
		t.Errorf("broadcast calls = %d, want 1", len(pushDAO.broadcastCalls))
	}
	if pushDAO.broadcastCalls[0].speed != 100 {
		t.Errorf("speed = %d, want 100", pushDAO.broadcastCalls[0].speed)
	}
	pushDAO.mu.Unlock()
}
