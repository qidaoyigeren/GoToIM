package router

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/service"
	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/stretchr/testify/assert"
)

// mockProducer implements mq.Producer for testing.
type mockProducer struct {
	enqueued     []*mq.Message
	lastUID      int64
	delayedMsgs  []*mq.Message
	delayedUID   int64
	delayedDelay int64
}

func (m *mockProducer) EnqueueToUser(ctx context.Context, uid int64, msg *mq.Message) error {
	m.lastUID = uid
	m.enqueued = append(m.enqueued, msg)
	return nil
}

func (m *mockProducer) EnqueueToUsers(ctx context.Context, uids []int64, msg *mq.Message) error {
	return nil
}

func (m *mockProducer) EnqueueToRoom(ctx context.Context, roomID string, msg *mq.Message) error {
	return nil
}

func (m *mockProducer) EnqueueBroadcast(ctx context.Context, msg *mq.Message, speed int32) error {
	return nil
}

func (m *mockProducer) EnqueueACK(ctx context.Context, msgID string, uid int64, status string) error {
	return nil
}

func (m *mockProducer) EnqueueDelayed(ctx context.Context, uid int64, msg *mq.Message, delayMs int64) error {
	m.delayedUID = uid
	m.delayedMsgs = append(m.delayedMsgs, msg)
	m.delayedDelay = delayMs
	return nil
}

func (m *mockProducer) Close() error { return nil }

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
	statuses map[string]map[string]string
	offline  map[string][]string
	seqs     map[int64]int64
}

func newMockMessageDAO() *mockMessageDAO {
	return &mockMessageDAO{
		statuses: make(map[string]map[string]string),
		offline:  make(map[string][]string),
		seqs:     make(map[int64]int64),
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

func (m *mockMessageDAO) IncrUserSeq(ctx context.Context, uid int64) (int64, error) {
	m.seqs[uid]++
	return m.seqs[uid], nil
}

func (m *mockMessageDAO) AddToOfflineQueue(ctx context.Context, uid int64, msgID string, seq float64) error {
	key := string(rune(uid))
	m.offline[key] = append(m.offline[key], msgID)
	return nil
}

func (m *mockMessageDAO) GetOfflineQueue(ctx context.Context, uid int64, lastSeq float64, limit int) ([]string, error) {
	return nil, nil
}

func (m *mockMessageDAO) RemoveFromOfflineQueue(ctx context.Context, uid int64, msgID string) error {
	return nil
}

func (m *mockMessageDAO) GetOfflineQueueSize(ctx context.Context, uid int64) (int64, error) {
	return 0, nil
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
func (m *mockPushDAO) PublishACK(ctx context.Context, msgID string, uid int64, status string) error {
	return nil
}

// mockCometPusher implements CometPusher for testing.
// pushErr is returned for all keys when perKeyErrs is not set.
// perKeyErrs allows simulating partial failures: keys in the map fail, others succeed.
type mockCometPusher struct {
	pushErr    error
	perKeyErrs map[string]error
}

func (m *mockCometPusher) PushMsg(ctx context.Context, server string, keys []string, op int32, body []byte) error {
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
}
