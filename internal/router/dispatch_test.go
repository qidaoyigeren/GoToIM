package router

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/service"
	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/stretchr/testify/assert"
)

// mockProducer implements mq.Producer for testing.
type mockProducer struct {
	enqueued     []*mq.Message
	lastUID      int64
	lastTopic    string
	delayedMsgs  []*mq.Message
	delayedUID   int64
	delayedDelay int64
	// error injection
	userErr      error
	roomErr      error
	broadcastErr error
}

func (m *mockProducer) EnqueueToUser(ctx context.Context, uid int64, msg *mq.Message) error {
	if m.userErr != nil {
		return m.userErr
	}
	m.lastUID = uid
	m.enqueued = append(m.enqueued, msg)
	return nil
}

func (m *mockProducer) EnqueueToTopic(ctx context.Context, topic string, uid int64, msg *mq.Message) error {
	m.lastTopic = topic
	return m.EnqueueToUser(ctx, uid, msg)
}

func (m *mockProducer) EnqueueToUsers(ctx context.Context, uids []int64, msg *mq.Message) error {
	return nil
}

func (m *mockProducer) EnqueueToRoom(ctx context.Context, roomID string, msg *mq.Message) error {
	if m.roomErr != nil {
		return m.roomErr
	}
	return nil
}

func (m *mockProducer) EnqueueBroadcast(ctx context.Context, msg *mq.Message, speed int32) error {
	if m.broadcastErr != nil {
		return m.broadcastErr
	}
	return nil
}

func (m *mockProducer) EnqueueACK(ctx context.Context, msgID string, uid int64, status, targetNode string) error {
	return nil
}

func (m *mockProducer) EnqueueDelayed(ctx context.Context, uid int64, msg *mq.Message, delayMs int64) error {
	m.delayedUID = uid
	m.delayedMsgs = append(m.delayedMsgs, msg)
	m.delayedDelay = delayMs
	return nil
}

func (m *mockProducer) Close() error { return nil }

// mockDirectBroadcaster implements DirectBroadcaster for testing.
type mockDirectBroadcaster struct {
	rooms       []string
	alls        []int32 // speeds
	broadcasted bool
}

func (m *mockDirectBroadcaster) BroadcastRoom(ctx context.Context, op int32, roomKey string, body []byte) error {
	m.rooms = append(m.rooms, roomKey)
	m.broadcasted = true
	return nil
}

func (m *mockDirectBroadcaster) BroadcastAll(ctx context.Context, op, speed int32, body []byte) error {
	m.alls = append(m.alls, speed)
	m.broadcasted = true
	return nil
}

// mockSessionDAO implements dao.SessionDAO for testing.
type mockSessionDAO struct {
	sessions map[string]map[string]string // sid -> fields
	byKey    map[string]string            // key -> sid
	byUID    map[int64]map[string]string  // uid -> {sid: device_info}
}

func newMockSessionDAO() *mockSessionDAO {
	return &mockSessionDAO{
		sessions: make(map[string]map[string]string),
		byKey:    make(map[string]string),
		byUID:    make(map[int64]map[string]string),
	}
}

func (m *mockSessionDAO) AddSession(ctx context.Context, sid string, uid int64, key, deviceID, platform, server string) error {
	m.sessions[sid] = map[string]string{
		"uid":       fmt.Sprintf("%d", uid),
		"key":       key,
		"device_id": deviceID,
		"platform":  platform,
		"server":    server,
	}
	m.byKey[key] = sid
	if m.byUID[uid] == nil {
		m.byUID[uid] = make(map[string]string)
	}
	m.byUID[uid][sid] = deviceID + ":" + platform
	return nil
}

func (m *mockSessionDAO) GetSession(ctx context.Context, sid string) (map[string]string, error) {
	return m.sessions[sid], nil
}

func (m *mockSessionDAO) GetSessionByKey(ctx context.Context, key string) (string, error) {
	return m.byKey[key], nil
}

func (m *mockSessionDAO) GetUserSessions(ctx context.Context, uid int64) (map[string]string, error) {
	return m.byUID[uid], nil
}

func (m *mockSessionDAO) GetDeviceSession(ctx context.Context, uid int64, deviceID string) (string, error) {
	return "", nil
}

func (m *mockSessionDAO) DelSession(ctx context.Context, sid string, uid int64, deviceID, key string) error {
	delete(m.sessions, sid)
	delete(m.byKey, key)
	if m.byUID[uid] != nil {
		delete(m.byUID[uid], sid)
	}
	return nil
}

func (m *mockSessionDAO) ExpireSession(ctx context.Context, sid string, uid int64) error {
	return nil
}

// mockMessageDAO implements dao.MessageDAO for testing.
type mockMessageDAO struct {
	statuses     map[string]map[string]string
	offline      map[string][]string
	userMessages map[int64][]string
	seqs         map[int64]int64
	attemptLocks map[string]string
}

func newMockMessageDAO() *mockMessageDAO {
	return &mockMessageDAO{
		statuses:     make(map[string]map[string]string),
		offline:      make(map[string][]string),
		userMessages: make(map[int64][]string),
		seqs:         make(map[int64]int64),
		attemptLocks: make(map[string]string),
	}
}

func (m *mockMessageDAO) SetMessageStatus(ctx context.Context, msgID string, fields map[string]interface{}) error {
	s := make(map[string]string)
	for k, v := range fields {
		s[k] = fmt.Sprintf("%v", v)
	}
	m.statuses[msgID] = s
	return nil
}

func (m *mockMessageDAO) SetMessageStatusNX(ctx context.Context, msgID, field string, value interface{}) (bool, error) {
	if _, ok := m.statuses[msgID]; ok {
		return false, nil
	}
	m.statuses[msgID] = map[string]string{field: fmt.Sprintf("%v", value)}
	return true, nil
}

func (m *mockMessageDAO) GetMessageStatus(ctx context.Context, msgID string) (map[string]string, error) {
	return m.statuses[msgID], nil
}

func (m *mockMessageDAO) UpdateMessageStatus(ctx context.Context, msgID, status string) error {
	if m.statuses[msgID] == nil {
		m.statuses[msgID] = make(map[string]string)
	}
	m.statuses[msgID]["status"] = status
	return nil
}

func (m *mockMessageDAO) IncrUserSeqBy(ctx context.Context, uid int64, delta int64) (int64, error) {
	if delta <= 0 {
		delta = 1
	}
	m.seqs[uid] += delta
	return m.seqs[uid], nil
}

func (m *mockMessageDAO) GetUserMaxSeq(ctx context.Context, uid int64) (int64, error) {
	return m.seqs[uid], nil
}

func (m *mockMessageDAO) AddUserMessage(ctx context.Context, uid int64, msgID string, seq int64) error {
	if m.statuses[msgID] == nil {
		m.statuses[msgID] = make(map[string]string)
	}
	m.statuses[msgID]["seq"] = fmt.Sprintf("%d", seq)
	m.userMessages[uid] = append(m.userMessages[uid], msgID)
	return nil
}

func (m *mockMessageDAO) GetUserMessagesAfterSeq(ctx context.Context, uid int64, lastSeq int64, limit int) ([]string, error) {
	msgs := m.userMessages[uid]
	if len(msgs) > limit {
		msgs = msgs[:limit]
	}
	return msgs, nil
}

func (m *mockMessageDAO) AddToOfflineQueue(ctx context.Context, uid int64, msgID string, seq float64) error {
	key := string(rune(uid))
	m.offline[key] = append(m.offline[key], msgID)
	return nil
}

func (m *mockMessageDAO) RemoveFromOfflineQueue(ctx context.Context, uid int64, msgID string) error {
	return nil
}

func (m *mockMessageDAO) RecordDeviceACK(ctx context.Context, msgID, deviceID, sessionID string, ackTime int64) error {
	return nil
}
func (m *mockMessageDAO) AcquireDeliveryAttemptLock(ctx context.Context, msgID, token string, ttl time.Duration) (bool, error) {
	if m.attemptLocks[msgID] != "" {
		return false, nil
	}
	m.attemptLocks[msgID] = token
	return true, nil
}
func (m *mockMessageDAO) ReleaseDeliveryAttemptLock(ctx context.Context, msgID, token string) error {
	if m.attemptLocks[msgID] == token {
		delete(m.attemptLocks, msgID)
	}
	return nil
}

// Phase 2: device cursor mocks
func (m *mockMessageDAO) GetDeviceCursor(ctx context.Context, uid int64, deviceID string) (int64, error) {
	return 0, nil
}
func (m *mockMessageDAO) GetOfflineMessagesByDeviceCursor(ctx context.Context, uid int64, deviceID string, limit int) ([]string, error) {
	return nil, nil
}
func (m *mockMessageDAO) AdvanceDeviceCursor(ctx context.Context, uid int64, deviceID string, seq int64) error {
	return nil
}

// mockPushDAO implements dao.PushDAO for testing.
type mockPushDAO struct{}

func (m *mockPushDAO) PushMsg(ctx context.Context, op int32, server string, keys []string, msg []byte) error {
	return nil
}
func (m *mockPushDAO) BroadcastRoomMsg(ctx context.Context, op int32, room string, msg []byte) error {
	return nil
}
func (m *mockPushDAO) BroadcastMsg(ctx context.Context, op, speed int32, msg []byte) error {
	return nil
}
func (m *mockPushDAO) PublishACK(ctx context.Context, msgID string, uid int64, status, targetNode, deviceID, sessionID string) error {
	return nil
}

// mockCometPusher implements CometPusher for testing.
// pushErr is returned for all keys when perKeyErrs is not set.
// perKeyErrs allows simulating partial failures: keys in the map fail, others succeed.
type mockCometPusher struct {
	pushErr    error
	perKeyErrs map[string]error
	calls      int
}

func (m *mockCometPusher) PushMsg(ctx context.Context, server string, keys []string, op int32, body []byte) error {
	m.calls++
	if len(keys) > 0 && m.perKeyErrs != nil {
		if err, ok := m.perKeyErrs[keys[0]]; ok {
			return err
		}
		return nil
	}
	return m.pushErr
}

func TestRouteByUserUsesUIDAsPartitionKey(t *testing.T) {
	prod := &mockProducer{}
	msgDAO := newMockMessageDAO()
	sessDAO := newMockSessionDAO()
	pusher := &mockCometPusher{}

	// Add a session for uid=5001
	sessDAO.AddSession(context.Background(), "sid1", 5001, "key1", "dev1", "web", "comet-1")

	sessMgr := service.NewSessionManager(sessDAO, 30*time.Minute)

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, sessMgr, pusher)
	engine.SetMQProducer(prod)

	// Push to offline user (uid=9999, no session) - should use uid as partition key
	err := engine.RouteByUser(context.Background(), "msg-001", 9999, 9, []byte("hello"), 1)
	assert.Nil(t, err)

	// Verify producer was called with uid-based key
	assert.Len(t, prod.enqueued, 1)
	assert.Equal(t, int64(9999), prod.lastUID)
	// The message Key should be the uid string, not a connection key
	assert.Equal(t, "9999", prod.enqueued[0].Key)
	assert.Equal(t, DeliveryStats{Kafka: 1}, engine.Stats())
}

func TestBuildMQHeadersExtractsTraceFromWrappedBizEnvelope(t *testing.T) {
	env := mq.BizEnvelope{
		MsgID:        "msg-1",
		BizID:        "ord-1",
		BusinessType: "order",
		EventType:    "paid",
		Priority:     "high",
		TTLSeconds:   30,
		TraceID:      "trace-wrapped",
		CreatedAtMS:  time.Now().UnixMilli(),
	}
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	body, err := protocol.MarshalMsgBody(&protocol.MsgBody{MsgID: "msg-1", ToUID: 1001, Content: payload})
	if err != nil {
		t.Fatalf("marshal msg body: %v", err)
	}
	parsed := parseBizEnvelope(body, "msg-1")
	headers := buildMQHeaders(parsed)
	if headers[mq.HeaderTraceID] != "trace-wrapped" || headers[mq.HeaderBusinessType] != "order" || headers[mq.HeaderPriority] != "high" {
		t.Fatalf("headers = %+v", headers)
	}
}

func TestRouteByUserDirectPushSuccess(t *testing.T) {
	prod := &mockProducer{}
	msgDAO := newMockMessageDAO()
	sessDAO := newMockSessionDAO()
	pusher := &mockCometPusher{pushErr: nil}

	// Add a session for uid=5001
	sessDAO.AddSession(context.Background(), "sid1", 5001, "key1", "dev1", "web", "comet-1")
	sessMgr := service.NewSessionManager(sessDAO, 30*time.Minute)

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, sessMgr, pusher)
	engine.SetMQProducer(prod)

	// Push to online user - should use direct push, not enqueue to MQ
	err := engine.RouteByUser(context.Background(), "msg-002", 5001, 9, []byte("hello online"), 1)
	assert.Nil(t, err)

	// Direct push succeeded, so producer should NOT be called
	assert.Len(t, prod.enqueued, 0)
	assert.Equal(t, DeliveryStats{Direct: 1}, engine.Stats())
}

func TestRouteByUserSkipsDuplicateAfterDelivered(t *testing.T) {
	prod := &mockProducer{}
	msgDAO := newMockMessageDAO()
	sessDAO := newMockSessionDAO()
	pusher := &mockCometPusher{pushErr: nil}

	sessDAO.AddSession(context.Background(), "sid1", 5001, "key1", "dev1", "web", "comet-1")
	sessMgr := service.NewSessionManager(sessDAO, 30*time.Minute)

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, sessMgr, pusher)
	engine.SetMQProducer(prod)

	err := engine.RouteByUser(context.Background(), "msg-duplicate", 5001, 9, []byte("hello online"), 1)
	assert.Nil(t, err)
	err = engine.RouteByUser(context.Background(), "msg-duplicate", 5001, 9, []byte("hello online"), 1)
	assert.Nil(t, err)

	assert.Equal(t, 1, pusher.calls)
	assert.Len(t, prod.enqueued, 0)
	assert.Equal(t, DeliveryStats{Direct: 1}, engine.Stats())
}

func TestRouteByUserAllowsRetryWhenMessageStillPending(t *testing.T) {
	prod := &mockProducer{userErr: assert.AnError}
	msgDAO := newMockMessageDAO()
	sessDAO := newMockSessionDAO()
	pusher := &mockCometPusher{pushErr: assert.AnError}

	sessDAO.AddSession(context.Background(), "sid1", 5004, "key1", "dev1", "web", "comet-1")
	sessMgr := service.NewSessionManager(sessDAO, 30*time.Minute)

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, sessMgr, pusher)
	engine.SetMQProducer(prod)

	err := engine.RouteByUser(context.Background(), "msg-pending-retry", 5004, 9, []byte("retry me"), 1)
	assert.Error(t, err)
	err = engine.RouteByUser(context.Background(), "msg-pending-retry", 5004, 9, []byte("retry me"), 1)
	assert.Error(t, err)

	assert.Equal(t, 2, pusher.calls)
	assert.Equal(t, "pending", msgDAO.statuses["msg-pending-retry"]["status"])
}

func TestRouteByUserSkipsConcurrentDeliveryAttempt(t *testing.T) {
	prod := &mockProducer{}
	msgDAO := newMockMessageDAO()
	sessDAO := newMockSessionDAO()
	pusher := &mockCometPusher{pushErr: nil}

	sessDAO.AddSession(context.Background(), "sid1", 5005, "key1", "dev1", "web", "comet-1")
	sessMgr := service.NewSessionManager(sessDAO, 30*time.Minute)

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, sessMgr, pusher)
	engine.SetMQProducer(prod)
	locked, err := msgDAO.AcquireDeliveryAttemptLock(context.Background(), "msg-in-flight", "other-worker", time.Second)
	assert.NoError(t, err)
	assert.True(t, locked)

	err = engine.RouteByUser(context.Background(), "msg-in-flight", 5005, 9, []byte("already running"), 1)
	assert.NoError(t, err)

	assert.Equal(t, 0, pusher.calls)
	assert.Len(t, prod.enqueued, 0)
}

func TestRouteByUserFallsBackToMQ(t *testing.T) {
	prod := &mockProducer{}
	msgDAO := newMockMessageDAO()
	sessDAO := newMockSessionDAO()
	pusher := &mockCometPusher{pushErr: assert.AnError}

	// Add a session for uid=5002
	sessDAO.AddSession(context.Background(), "sid2", 5002, "key2", "dev2", "ios", "comet-1")
	sessMgr := service.NewSessionManager(sessDAO, 30*time.Minute)

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, sessMgr, pusher)
	engine.SetMQProducer(prod)

	// Direct push will fail, should fallback to MQ
	err := engine.RouteByUser(context.Background(), "msg-003", 5002, 9, []byte("fallback test"), 1)
	assert.Nil(t, err)

	// Verify fallback used uid as partition key
	assert.Len(t, prod.enqueued, 1)
	assert.Equal(t, "5002", prod.enqueued[0].Key)
	assert.Equal(t, DeliveryStats{Kafka: 1}, engine.Stats())
}

func TestReliableEnqueueSelectsOnlineOfflineTopic(t *testing.T) {
	prod := &mockProducer{}
	msgDAO := newMockMessageDAO()
	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, nil, &mockCometPusher{})
	engine.SetMQProducer(prod)
	engine.SetPushTopics("push-online", "push-offline")

	err := engine.reliableEnqueue(context.Background(), "msg-offline", 6001, 9, []byte("offline"), 1, nil)
	assert.Nil(t, err)
	assert.Equal(t, "push-offline", prod.lastTopic)

	err = engine.reliableEnqueue(context.Background(), "msg-online", 6001, 9, []byte("online"), 2, []*service.Session{
		{Key: "key1", Server: "comet-1"},
	})
	assert.Nil(t, err)
	assert.Equal(t, "push-online", prod.lastTopic)
}

func TestRouteByUserDirectPushPartialFailure(t *testing.T) {
	prod := &mockProducer{}
	msgDAO := newMockMessageDAO()
	sessDAO := newMockSessionDAO()

	// Simulate 3 devices: key1(success), key2(fail), key3(success)
	pusher := &mockCometPusher{
		perKeyErrs: map[string]error{
			"key2": assert.AnError,
		},
	}

	sessDAO.AddSession(context.Background(), "sid1", 5003, "key1", "dev1", "web", "comet-1")
	sessDAO.AddSession(context.Background(), "sid2", 5003, "key2", "dev2", "ios", "comet-2")
	sessDAO.AddSession(context.Background(), "sid3", 5003, "key3", "dev3", "android", "comet-1")
	sessMgr := service.NewSessionManager(sessDAO, 30*time.Minute)

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, sessMgr, pusher)
	engine.SetMQProducer(prod)

	err := engine.RouteByUser(context.Background(), "msg-004", 5003, 9, []byte("partial test"), 1)
	assert.Nil(t, err)

	// Reliable path should be triggered for the failed session only
	assert.Len(t, prod.enqueued, 1)
	assert.Equal(t, int64(5003), prod.lastUID)
	// The message Key should be the uid string
	assert.Equal(t, "5003", prod.enqueued[0].Key)
	assert.Equal(t, DeliveryStats{Direct: 1, Kafka: 1}, engine.Stats())
}

func TestRouteByRoomKafkaFailsFallbackToBroadcaster(t *testing.T) {
	prod := &mockProducer{roomErr: assert.AnError}
	broadcaster := &mockDirectBroadcaster{}
	msgDAO := newMockMessageDAO()
	pusher := &mockCometPusher{}

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, nil, pusher)
	engine.SetMQProducer(prod)
	engine.SetBroadcastFallback(broadcaster)

	err := engine.RouteByRoom(context.Background(), 9, "room-1", []byte("room fallback test"))
	assert.Nil(t, err)

	// Broadcaster should have been called as fallback
	assert.True(t, broadcaster.broadcasted)
	assert.Contains(t, broadcaster.rooms, "room-1")
	assert.Equal(t, DeliveryStats{Direct: 1}, engine.Stats())
}

func TestRouteByRoomKafkaFailsNoBroadcasterReturnsError(t *testing.T) {
	prod := &mockProducer{roomErr: assert.AnError}
	msgDAO := newMockMessageDAO()
	pusher := &mockCometPusher{}

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, nil, pusher)
	engine.SetMQProducer(prod)
	// No broadcaster set

	err := engine.RouteByRoom(context.Background(), 9, "room-2", []byte("room no fallback test"))
	assert.Error(t, err)
}

func TestRouteBroadcastKafkaFailsFallbackToBroadcaster(t *testing.T) {
	prod := &mockProducer{broadcastErr: assert.AnError}
	broadcaster := &mockDirectBroadcaster{}
	msgDAO := newMockMessageDAO()
	pusher := &mockCometPusher{}

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, nil, pusher)
	engine.SetMQProducer(prod)
	engine.SetBroadcastFallback(broadcaster)

	err := engine.RouteBroadcast(context.Background(), 9, 100, []byte("broadcast fallback test"))
	assert.Nil(t, err)

	// Broadcaster should have been called as fallback
	assert.True(t, broadcaster.broadcasted)
	assert.Contains(t, broadcaster.alls, int32(100))
	assert.Equal(t, DeliveryStats{Direct: 1}, engine.Stats())
}

func TestRouteBroadcastNoProducerUsesBroadcaster(t *testing.T) {
	broadcaster := &mockDirectBroadcaster{}
	msgDAO := newMockMessageDAO()
	pusher := &mockCometPusher{}

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, nil, pusher)
	// No producer set
	engine.SetBroadcastFallback(broadcaster)

	err := engine.RouteBroadcast(context.Background(), 9, 50, []byte("broadcast no kafka test"))
	assert.Nil(t, err)
	assert.True(t, broadcaster.broadcasted)
	assert.Equal(t, DeliveryStats{Direct: 1}, engine.Stats())
}

func TestRouteByRoomNoProducerUsesBroadcaster(t *testing.T) {
	broadcaster := &mockDirectBroadcaster{}
	msgDAO := newMockMessageDAO()
	pusher := &mockCometPusher{}

	engine := NewDispatchEngine(&mockPushDAO{}, msgDAO, nil, pusher)
	// No producer set
	engine.SetBroadcastFallback(broadcaster)

	err := engine.RouteByRoom(context.Background(), 9, "room-3", []byte("room no kafka test"))
	assert.Nil(t, err)
	assert.True(t, broadcaster.broadcasted)
	assert.Contains(t, broadcaster.rooms, "room-3")
	assert.Equal(t, DeliveryStats{Direct: 1}, engine.Stats())
}
