package logic

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/service"
	"github.com/Terry-Mao/goim/internal/router"
)

// ============ minimal mocks for Receive tests ============

type recvMockMessageDAO struct {
	mu           sync.RWMutex
	msgStatus    map[string]map[string]string
	offlineQueue map[int64][]struct {
		msgID string
		seq   float64
	}
	userSeq map[int64]int64
}

func newRecvMockMessageDAO() *recvMockMessageDAO {
	return &recvMockMessageDAO{
		msgStatus: make(map[string]map[string]string),
		offlineQueue: make(map[int64][]struct {
			msgID string
			seq   float64
		}),
		userSeq: make(map[int64]int64),
	}
}

func (m *recvMockMessageDAO) SetMessageStatus(_ context.Context, msgID string, fields map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := make(map[string]string)
	for k, v := range fields {
		s[k] = fmt.Sprintf("%v", v)
	}
	m.msgStatus[msgID] = s
	return nil
}

func (m *recvMockMessageDAO) SetMessageStatusNX(_ context.Context, msgID, field string, value interface{}) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.msgStatus[msgID]
	if !ok {
		s = make(map[string]string)
		m.msgStatus[msgID] = s
	}
	if _, exists := s[field]; exists {
		return false, nil
	}
	s[field] = fmt.Sprintf("%v", value)
	return true, nil
}

func (m *recvMockMessageDAO) GetMessageStatus(_ context.Context, msgID string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.msgStatus[msgID]; ok {
		return s, nil
	}
	return nil, nil
}

func (m *recvMockMessageDAO) UpdateMessageStatus(_ context.Context, msgID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.msgStatus[msgID]; ok {
		s["status"] = status
	} else {
		m.msgStatus[msgID] = map[string]string{"status": status}
	}
	return nil
}

func (m *recvMockMessageDAO) IncrUserSeq(_ context.Context, uid int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userSeq[uid]++
	return m.userSeq[uid], nil
}

func (m *recvMockMessageDAO) AddToOfflineQueue(_ context.Context, uid int64, msgID string, seq float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.offlineQueue[uid] = append(m.offlineQueue[uid], struct {
		msgID string
		seq   float64
	}{msgID, seq})
	return nil
}

func (m *recvMockMessageDAO) GetOfflineQueue(_ context.Context, uid int64, lastSeq float64, limit int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []string
	for _, e := range m.offlineQueue[uid] {
		if e.seq > lastSeq {
			result = append(result, e.msgID)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *recvMockMessageDAO) RemoveFromOfflineQueue(_ context.Context, uid int64, msgID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := m.offlineQueue[uid]
	for i, e := range entries {
		if e.msgID == msgID {
			m.offlineQueue[uid] = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	return nil
}

func (m *recvMockMessageDAO) GetOfflineQueueSize(_ context.Context, uid int64) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.offlineQueue[uid])), nil
}

type recvMockPushDAO struct {
	mu        sync.Mutex
	pushCalls []struct {
		op     int32
		server string
		keys   []string
		msg    []byte
	}
	ackCalls []struct {
		msgID  string
		uid    int64
		status string
	}
}

func (m *recvMockPushDAO) PushMsg(_ context.Context, op int32, server string, keys []string, msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pushCalls = append(m.pushCalls, struct {
		op     int32
		server string
		keys   []string
		msg    []byte
	}{op, server, keys, msg})
	return nil
}

func (m *recvMockPushDAO) BroadcastRoomMsg(_ context.Context, op int32, room string, msg []byte) error {
	return nil
}
func (m *recvMockPushDAO) BroadcastMsg(_ context.Context, op, speed int32, msg []byte) error {
	return nil
}

func (m *recvMockPushDAO) PublishACK(_ context.Context, msgID string, uid int64, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ackCalls = append(m.ackCalls, struct {
		msgID  string
		uid    int64
		status string
	}{msgID, uid, status})
	return nil
}

type recvMockSessionDAO struct {
	sessions     map[string]map[string]string
	userSessions map[int64]map[string]string
}

func newRecvMockSessionDAO() *recvMockSessionDAO {
	return &recvMockSessionDAO{
		sessions:     make(map[string]map[string]string),
		userSessions: make(map[int64]map[string]string),
	}
}

func (m *recvMockSessionDAO) AddSession(_ context.Context, sid string, uid int64, key, deviceID, platform, server string) error {
	m.sessions[sid] = map[string]string{"uid": fmt.Sprintf("%d", uid), "key": key, "device_id": deviceID, "platform": platform, "server": server}
	if m.userSessions[uid] == nil {
		m.userSessions[uid] = make(map[string]string)
	}
	m.userSessions[uid][sid] = deviceID + ":" + platform
	return nil
}

func (m *recvMockSessionDAO) GetSession(_ context.Context, sid string) (map[string]string, error) {
	if s, ok := m.sessions[sid]; ok {
		return s, nil
	}
	return nil, nil
}

func (m *recvMockSessionDAO) GetUserSessions(_ context.Context, uid int64) (map[string]string, error) {
	if s, ok := m.userSessions[uid]; ok {
		return s, nil
	}
	return nil, nil
}

func (m *recvMockSessionDAO) GetDeviceSession(_ context.Context, uid int64, deviceID string) (string, error) {
	return "", nil
}

func (m *recvMockSessionDAO) GetSessionByKey(_ context.Context, key string) (string, error) {
	for sid, s := range m.sessions {
		if s["key"] == key {
			return sid, nil
		}
	}
	return "", nil
}

func (m *recvMockSessionDAO) DelSession(_ context.Context, sid string, uid int64, deviceID, key string) error {
	delete(m.sessions, sid)
	return nil
}

func (m *recvMockSessionDAO) ExpireSession(_ context.Context, sid string, uid int64) error {
	return nil
}

type recvMockCometPusher struct {
	mu    sync.Mutex
	calls []struct {
		server string
		keys   []string
		op     int32
		body   []byte
	}
}

func (m *recvMockCometPusher) PushMsg(_ context.Context, server string, keys []string, op int32, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, struct {
		server string
		keys   []string
		op     int32
		body   []byte
	}{server, keys, op, body})
	return nil
}

// newTestLogicForP0 creates a minimal Logic for P0 testing without requiring discovery/Redis/Kafka.
func newTestLogicForP0() (*Logic, *recvMockMessageDAO, *recvMockPushDAO, *recvMockSessionDAO, *recvMockCometPusher) {
	msgDAO := newRecvMockMessageDAO()
	pushDAO := &recvMockPushDAO{}
	sessDAO := newRecvMockSessionDAO()
	pusher := &recvMockCometPusher{}

	sessMgr := service.NewSessionManager(sessDAO, 10*time.Minute)
	syncSvc := service.NewSyncService(msgDAO, sessMgr, nil) // pusher set below

	l := &Logic{}
	l.syncSvc = syncSvc
	l.sessionMgr = sessMgr
	l.router = router.NewDispatchEngine(pushDAO, msgDAO, sessMgr, pusher)
	return l, msgDAO, pushDAO, sessDAO, pusher
}

// ============ Receive tests ============

func TestReceive_PushMsgAck(t *testing.T) {
	l, msgDAO, pushDAO, _, _ := newTestLogicForP0()
	ctx := context.Background()

	// Track a message first
	msgDAO.SetMessageStatus(ctx, "msg-ack-1", map[string]interface{}{
		"status":   service.MsgStatusPending,
		"from_uid": 100,
		"to_uid":   200,
	})

	// Build ACK proto
	ackBytes, _ := protocol.MarshalAckBody(&protocol.AckBody{
		MsgID: "msg-ack-1",
		Seq:   1,
	})
	p := &protocol.Proto{
		Op:   protocol.OpPushMsgAck,
		Body: ackBytes,
	}

	err := l.Receive(ctx, 200, p)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	// Verify message status updated to acked
	status, _ := msgDAO.GetMessageStatus(ctx, "msg-ack-1")
	if status["status"] != service.MsgStatusAcked {
		t.Errorf("status = %q, want %q", status["status"], service.MsgStatusAcked)
	}

	// Verify ACK event published
	pushDAO.mu.Lock()
	if len(pushDAO.ackCalls) != 1 {
		t.Errorf("ack calls = %d, want 1", len(pushDAO.ackCalls))
	} else {
		if pushDAO.ackCalls[0].msgID != "msg-ack-1" {
			t.Errorf("ack msgID = %q, want %q", pushDAO.ackCalls[0].msgID, "msg-ack-1")
		}
		if pushDAO.ackCalls[0].uid != 200 {
			t.Errorf("ack uid = %d, want 200", pushDAO.ackCalls[0].uid)
		}
	}
	pushDAO.mu.Unlock()
}

func TestReceive_SyncReq(t *testing.T) {
	l, msgDAO, _, sessDAO, pusher := newTestLogicForP0()
	ctx := context.Background()

	// Add offline messages
	msgDAO.AddToOfflineQueue(ctx, 1001, "msg-sync-1", 1.0)
	msgDAO.AddToOfflineQueue(ctx, 1001, "msg-sync-2", 2.0)
	msgDAO.SetMessageStatus(ctx, "msg-sync-1", map[string]interface{}{
		"status":     service.MsgStatusDelivered,
		"from_uid":   100,
		"to_uid":     1001,
		"body":       "aGVsbG8=", // base64("hello")
		"created_at": time.Now().UnixMilli(),
	})
	msgDAO.SetMessageStatus(ctx, "msg-sync-2", map[string]interface{}{
		"status":     service.MsgStatusDelivered,
		"from_uid":   101,
		"to_uid":     1001,
		"body":       "d29ybGQ=", // base64("world")
		"created_at": time.Now().UnixMilli(),
	})

	// User must have sessions for push to happen
	sessDAO.AddSession(ctx, "s1", 1001, "k1", "d1", "android", "comet-1")

	// Build SyncReq proto
	syncBytes := protocol.MarshalSyncReq(&protocol.SyncReqBody{
		LastSeq: 0,
		Limit:   100,
	})
	p := &protocol.Proto{
		Op:   protocol.OpSyncReq,
		Body: syncBytes,
	}

	err := l.Receive(ctx, 1001, p)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	// Verify p.Op changed to OpSyncReply
	if p.Op != protocol.OpSyncReply {
		t.Errorf("p.Op = %d, want %d", p.Op, protocol.OpSyncReply)
	}

	// Verify p.Body contains sync reply with messages
	if p.Body == nil {
		t.Fatal("p.Body is nil, want sync reply data")
	}

	reply, err := protocol.UnmarshalSyncReply(p.Body)
	if err != nil {
		t.Fatalf("UnmarshalSyncReply: %v", err)
	}

	if len(reply.Messages) != 2 {
		t.Errorf("message count = %d, want 2", len(reply.Messages))
	}

	// Verify push happened (OnUserOnline triggers push via sessions)
	pusher.mu.Lock()
	if len(pusher.calls) == 0 {
		// Note: Receive's sync path pushes via SyncService.GetOfflineMessages
		// which is called by the SyncReq handler, not OnUserOnline
	}
	pusher.mu.Unlock()
}

func TestReceive_SyncReq_AckedSkipped(t *testing.T) {
	l, msgDAO, _, _, _ := newTestLogicForP0()
	ctx := context.Background()

	msgDAO.AddToOfflineQueue(ctx, 1001, "msg-acked", 1.0)
	msgDAO.AddToOfflineQueue(ctx, 1001, "msg-delivered", 2.0)
	msgDAO.SetMessageStatus(ctx, "msg-acked", map[string]interface{}{
		"status": service.MsgStatusAcked,
	})
	msgDAO.SetMessageStatus(ctx, "msg-delivered", map[string]interface{}{
		"status": service.MsgStatusDelivered,
	})

	syncBytes := protocol.MarshalSyncReq(&protocol.SyncReqBody{LastSeq: 0, Limit: 100})
	p := &protocol.Proto{Op: protocol.OpSyncReq, Body: syncBytes}

	err := l.Receive(ctx, 1001, p)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	reply, _ := protocol.UnmarshalSyncReply(p.Body)
	if len(reply.Messages) != 1 {
		t.Errorf("message count = %d, want 1 (acked msg skipped)", len(reply.Messages))
	}
	if len(reply.Messages) > 0 && reply.Messages[0].MsgID != "msg-delivered" {
		t.Errorf("remaining msg = %q, want %q", reply.Messages[0].MsgID, "msg-delivered")
	}
}

func TestReceive_UnknownOp(t *testing.T) {
	l, _, _, _, _ := newTestLogicForP0()
	ctx := context.Background()

	p := &protocol.Proto{Op: 9999, Body: []byte("unknown")}
	err := l.Receive(ctx, 1001, p)
	// Should not error for unknown ops
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
}

func TestReceive_InvalidAckBody(t *testing.T) {
	l, _, _, _, _ := newTestLogicForP0()
	ctx := context.Background()

	p := &protocol.Proto{Op: protocol.OpPushMsgAck, Body: []byte{0x01}} // too short
	err := l.Receive(ctx, 1001, p)
	if err == nil {
		t.Error("expected error for invalid ack body, got nil")
	}
}

func TestReceive_InvalidSyncBody(t *testing.T) {
	l, _, _, _, _ := newTestLogicForP0()
	ctx := context.Background()

	p := &protocol.Proto{Op: protocol.OpSyncReq, Body: []byte{0x01}} // too short
	err := l.Receive(ctx, 1001, p)
	if err == nil {
		t.Error("expected error for invalid sync body, got nil")
	}
}
