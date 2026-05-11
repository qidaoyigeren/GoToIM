package service

import (
	"context"
	"fmt"
	"sync"
)

// ============ mockSessionDAO ============

type mockSessionDAO struct {
	mu             sync.RWMutex
	sessions       map[string]map[string]string // sid -> fields
	userSessions   map[int64]map[string]string  // uid -> {sid: device_info}
	deviceSessions map[string]string            // "uid:device_id" -> sid
	keySessions    map[string]string            // key -> sid (reverse index)
}

func newMockSessionDAO() *mockSessionDAO {
	return &mockSessionDAO{
		sessions:       make(map[string]map[string]string),
		userSessions:   make(map[int64]map[string]string),
		deviceSessions: make(map[string]string),
		keySessions:    make(map[string]string),
	}
}

func (m *mockSessionDAO) AddSession(_ context.Context, sid string, uid int64, key, deviceID, platform, server string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[sid] = map[string]string{
		"uid":       fmt.Sprintf("%d", uid),
		"key":       key,
		"device_id": deviceID,
		"platform":  platform,
		"server":    server,
	}
	if m.userSessions[uid] == nil {
		m.userSessions[uid] = make(map[string]string)
	}
	m.userSessions[uid][sid] = deviceID + ":" + platform
	m.deviceSessions[fmt.Sprintf("%d:%s", uid, deviceID)] = sid
	m.keySessions[key] = sid
	return nil
}

func (m *mockSessionDAO) GetSession(_ context.Context, sid string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.sessions[sid]; ok {
		return s, nil
	}
	return nil, nil
}

func (m *mockSessionDAO) GetUserSessions(_ context.Context, uid int64) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.userSessions[uid]; ok {
		return s, nil
	}
	return nil, nil
}

func (m *mockSessionDAO) GetDeviceSession(_ context.Context, uid int64, deviceID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := fmt.Sprintf("%d:%s", uid, deviceID)
	return m.deviceSessions[key], nil
}

func (m *mockSessionDAO) GetSessionByKey(_ context.Context, key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.keySessions[key], nil
}

func (m *mockSessionDAO) DelSession(_ context.Context, sid string, uid int64, deviceID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sid)
	if uid > 0 {
		if us, ok := m.userSessions[uid]; ok {
			delete(us, sid)
		}
	}
	if deviceID != "" {
		delete(m.deviceSessions, fmt.Sprintf("%d:%s", uid, deviceID))
	}
	if key != "" {
		delete(m.keySessions, key)
	}
	return nil
}

func (m *mockSessionDAO) ExpireSession(_ context.Context, sid string, uid int64) error {
	// no-op in mock
	return nil
}

// ============ mockMessageDAO ============

type mockMessageDAO struct {
	mu           sync.RWMutex
	msgStatus    map[string]map[string]string // msg_id -> fields
	offlineQueue map[int64][]offlineEntry     // uid -> entries
	userSeq      map[int64]int64              // uid -> seq
}

type offlineEntry struct {
	msgID string
	seq   float64
}

func newMockMessageDAO() *mockMessageDAO {
	return &mockMessageDAO{
		msgStatus:    make(map[string]map[string]string),
		offlineQueue: make(map[int64][]offlineEntry),
		userSeq:      make(map[int64]int64),
	}
}

func (m *mockMessageDAO) SetMessageStatus(_ context.Context, msgID string, fields map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := make(map[string]string)
	for k, v := range fields {
		s[k] = fmt.Sprintf("%v", v)
	}
	m.msgStatus[msgID] = s
	return nil
}

func (m *mockMessageDAO) SetMessageStatusNX(_ context.Context, msgID, field string, value interface{}) (bool, error) {
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

func (m *mockMessageDAO) GetMessageStatus(_ context.Context, msgID string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.msgStatus[msgID]; ok {
		return s, nil
	}
	return nil, nil
}

func (m *mockMessageDAO) UpdateMessageStatus(_ context.Context, msgID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.msgStatus[msgID]; ok {
		s["status"] = status
	} else {
		m.msgStatus[msgID] = map[string]string{"status": status}
	}
	return nil
}

func (m *mockMessageDAO) IncrUserSeq(_ context.Context, uid int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userSeq[uid]++
	return m.userSeq[uid], nil
}

func (m *mockMessageDAO) AddToOfflineQueue(_ context.Context, uid int64, msgID string, seq float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.offlineQueue[uid] = append(m.offlineQueue[uid], offlineEntry{msgID: msgID, seq: seq})
	return nil
}

func (m *mockMessageDAO) GetOfflineQueue(_ context.Context, uid int64, lastSeq float64, limit int) ([]string, error) {
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

func (m *mockMessageDAO) RemoveFromOfflineQueue(_ context.Context, uid int64, msgID string) error {
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

func (m *mockMessageDAO) GetOfflineQueueSize(_ context.Context, uid int64) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.offlineQueue[uid])), nil
}

// ============ mockPushDAO ============

type pushCall struct {
	op     int32
	server string
	keys   []string
	msg    []byte
}

type roomCall struct {
	op   int32
	room string
	msg  []byte
}

type broadcastCall struct {
	op    int32
	speed int32
	msg   []byte
}

type mockPushDAO struct {
	mu             sync.Mutex
	pushCalls      []pushCall
	roomCalls      []roomCall
	broadcastCalls []broadcastCall
	pushErr        error // inject error for PushMsg
}

func newMockPushDAO() *mockPushDAO {
	return &mockPushDAO{}
}

func (m *mockPushDAO) PushMsg(_ context.Context, op int32, server string, keys []string, msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pushCalls = append(m.pushCalls, pushCall{op: op, server: server, keys: keys, msg: msg})
	return m.pushErr
}

func (m *mockPushDAO) BroadcastRoomMsg(_ context.Context, op int32, room string, msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roomCalls = append(m.roomCalls, roomCall{op: op, room: room, msg: msg})
	return nil
}

func (m *mockPushDAO) BroadcastMsg(_ context.Context, op, speed int32, msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcastCalls = append(m.broadcastCalls, broadcastCall{op: op, speed: speed, msg: msg})
	return nil
}

func (m *mockPushDAO) PublishACK(_ context.Context, msgID string, uid int64, status string) error {
	return nil // no-op in mock
}

// ============ mockCometPusher ============

type mockCometPusher struct {
	mu    sync.Mutex
	calls []struct {
		server string
		keys   []string
		op     int32
		body   []byte
	}
	pushErr error // inject error to simulate failure
}

func newMockCometPusher() *mockCometPusher {
	return &mockCometPusher{}
}

func (m *mockCometPusher) PushMsg(_ context.Context, server string, keys []string, op int32, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, struct {
		server string
		keys   []string
		op     int32
		body   []byte
	}{server: server, keys: keys, op: op, body: body})
	return m.pushErr
}
