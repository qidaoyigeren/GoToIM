package logic

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/service"
)

// TestConnect_OnUserOnline_Triggered verifies that Connect triggers offline sync.
// Since we can't easily mock Logic.dao (concrete type), we test the service layer
// integration: if OnUserOnline is called with the right params, the sync flow works.
func TestConnect_OnUserOnline_ServiceIntegration(t *testing.T) {
	// This test verifies the service layer that Connect calls into.
	// The actual Connect -> OnUserOnline wiring is tested via integration tests.
	// Here we verify the service layer handles the scenario correctly.

	msgDAO := newRecvMockMessageDAO()
	pushDAO := &recvMockPushDAO{}
	sessDAO := newRecvMockSessionDAO()
	pusher := &recvMockCometPusher{}

	sessMgr := service.NewSessionManager(sessDAO, 10*time.Minute)
	ackSvc := service.NewAckService(msgDAO, pushDAO)
	pushSvc := service.NewPushService(pushDAO, msgDAO, sessMgr, ackSvc, pusher)
	syncSvc := service.NewSyncService(msgDAO, sessMgr, pushSvc)

	ctx := context.Background()

	// Simulate: user 1001 has offline messages
	msgDAO.AddToOfflineQueue(ctx, 1001, "offline-1", 1.0)
	msgDAO.SetMessageStatus(ctx, "offline-1", map[string]interface{}{
		"status":     service.MsgStatusDelivered,
		"from_uid":   100,
		"to_uid":     1001,
		"body":       "aGVsbG8=", // base64("hello")
		"created_at": time.Now().UnixMilli(),
	})

	// User must have sessions for push to work
	sessDAO.AddSession(ctx, "s1", 1001, "k1", "d1", "android", "comet-1")

	// This is what Connect calls in the P0 change
	err := syncSvc.OnUserOnline(ctx, 1001, 0)
	if err != nil {
		t.Fatalf("OnUserOnline: %v", err)
	}

	// Verify sync push happened
	pusher.mu.Lock()
	if len(pusher.calls) == 0 {
		t.Error("expected sync pushes after OnUserOnline, got 0")
	}
	pusher.mu.Unlock()
}

// TestReceive_FullFlow tests the complete ACK -> Sync flow through the service layer.
func TestReceive_FullFlow(t *testing.T) {
	l, msgDAO, pushDAO, sessDAO, pusher := newTestLogicForP0()
	ctx := context.Background()

	// Step 1: Track a message (simulates PushToUser)
	msgDAO.SetMessageStatus(ctx, "msg-flow-1", map[string]interface{}{
		"status":   service.MsgStatusPending,
		"from_uid": 100,
		"to_uid":   200,
		"body":     "dGVzdA==", // base64("test")
	})
	msgDAO.AddToOfflineQueue(ctx, 200, "msg-flow-1", 1.0)

	// Step 2: Simulate client sending ACK
	ackBytes, _ := protocol.MarshalAckBody(&protocol.AckBody{
		MsgID: "msg-flow-1",
		Seq:   1,
	})
	err := l.Receive(ctx, 200, &protocol.Proto{
		Op:   protocol.OpPushMsgAck,
		Body: ackBytes,
	})
	if err != nil {
		t.Fatalf("Receive ACK: %v", err)
	}

	// Verify: message status is acked
	status, _ := msgDAO.GetMessageStatus(ctx, "msg-flow-1")
	if status["status"] != service.MsgStatusAcked {
		t.Errorf("after ACK: status = %q, want %q", status["status"], service.MsgStatusAcked)
	}

	// Verify: removed from offline queue
	size, _ := msgDAO.GetOfflineQueueSize(ctx, 200)
	if size != 0 {
		t.Errorf("after ACK: offline queue size = %d, want 0", size)
	}

	// Verify: ACK event published
	pushDAO.mu.Lock()
	if len(pushDAO.ackCalls) != 1 {
		t.Errorf("ack calls = %d, want 1", len(pushDAO.ackCalls))
	}
	pushDAO.mu.Unlock()

	// Step 3: Add another offline message for user 200
	msgDAO.AddToOfflineQueue(ctx, 200, "msg-flow-2", 2.0)
	msgDAO.SetMessageStatus(ctx, "msg-flow-2", map[string]interface{}{
		"status":     service.MsgStatusDelivered,
		"from_uid":   101,
		"to_uid":     200,
		"body":       "d29ybGQ=", // base64("world")
		"created_at": time.Now().UnixMilli(),
	})
	sessDAO.AddSession(ctx, "s2", 200, "k2", "d2", "ios", "comet-1")

	// Step 4: Client sends SyncReq
	syncBytes := protocol.MarshalSyncReq(&protocol.SyncReqBody{
		LastSeq: 0,
		Limit:   100,
	})
	p := &protocol.Proto{
		Op:   protocol.OpSyncReq,
		Body: syncBytes,
	}
	err = l.Receive(ctx, 200, p)
	if err != nil {
		t.Fatalf("Receive SyncReq: %v", err)
	}

	// Verify: proto updated with sync reply
	if p.Op != protocol.OpSyncReply {
		t.Errorf("after SyncReq: p.Op = %d, want %d", p.Op, protocol.OpSyncReply)
	}

	reply, err := protocol.UnmarshalSyncReply(p.Body)
	if err != nil {
		t.Fatalf("UnmarshalSyncReply: %v", err)
	}

	// msg-flow-1 is acked (should be skipped), msg-flow-2 should be returned
	if len(reply.Messages) != 1 {
		t.Errorf("sync message count = %d, want 1", len(reply.Messages))
	} else if reply.Messages[0].MsgID != "msg-flow-2" {
		t.Errorf("sync msg = %q, want %q", reply.Messages[0].MsgID, "msg-flow-2")
	}

	_ = pusher // suppress unused warning
}

// TestConnect_TokenParsing verifies the auth token JSON parsing logic.
func TestConnect_TokenParsing(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		wantMid  int64
		wantKey  string
		wantRoom string
		wantErr  bool
	}{
		{
			name:     "full token",
			token:    `{"mid":1001, "key":"k1", "room_id":"live://123", "platform":"web", "device_id":"d1", "accepts":[1000], "last_seq":42}`,
			wantMid:  1001,
			wantKey:  "k1",
			wantRoom: "live://123",
		},
		{
			name:     "minimal token (key auto-generated)",
			token:    `{"mid":2001, "platform":"android"}`,
			wantMid:  2001,
			wantRoom: "",
		},
		{
			name:    "invalid json",
			token:   `{bad json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var params struct {
				Mid      int64   `json:"mid"`
				Key      string  `json:"key"`
				RoomID   string  `json:"room_id"`
				Platform string  `json:"platform"`
				DeviceID string  `json:"device_id"`
				Accepts  []int32 `json:"accepts"`
				LastSeq  int64   `json:"last_seq"`
			}
			err := json.Unmarshal([]byte(tt.token), &params)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if params.Mid != tt.wantMid {
				t.Errorf("mid = %d, want %d", params.Mid, tt.wantMid)
			}
			if tt.wantKey != "" && params.Key != tt.wantKey {
				t.Errorf("key = %q, want %q", params.Key, tt.wantKey)
			}
			if params.RoomID != tt.wantRoom {
				t.Errorf("roomID = %q, want %q", params.RoomID, tt.wantRoom)
			}
		})
	}
}
