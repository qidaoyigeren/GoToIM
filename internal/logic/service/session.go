package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	log "github.com/Terry-Mao/goim/pkg/log"
)

// Ensure Dao satisfies the interface at compile time.
var _ dao.SessionDAO = (*dao.Dao)(nil)

// CometKicker kicks a connection on a Comet server.
type CometKicker interface {
	KickConnection(ctx context.Context, server, key string) error
}

// Session represents an active user connection session.
type Session struct {
	SID       string
	UID       int64
	Key       string
	DeviceID  string
	Platform  string
	Server    string
	CreatedAt time.Time
	LastHBAt  time.Time
	cachedAt  time.Time
}

// SessionManager manages user sessions with Redis persistence and local caching.
type SessionManager struct {
	dao    dao.SessionDAO
	local  sync.Map // uid -> []*Session
	ttl    time.Duration
	kicker CometKicker
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(d dao.SessionDAO, ttl time.Duration) *SessionManager {
	return &SessionManager{
		dao: d,
		ttl: ttl,
	}
}

// SetKicker sets the Comet kicker for notifying Comet to close connections.
func (m *SessionManager) SetKicker(k CometKicker) {
	m.kicker = k
}

// Create creates a new session. If the same device already has a session, the old one is invalidated.
func (m *SessionManager) Create(ctx context.Context, sess *Session) error {
	// Check for existing session on the same device
	oldSID, _ := m.dao.GetDeviceSession(ctx, sess.UID, sess.DeviceID)
	if oldSID != "" && oldSID != sess.SID {
		log.Errorf("session kick: uid=%d device=%s old_sid=%s new_sid=%s",
			sess.UID, sess.DeviceID, oldSID, sess.SID)
		m.Kick(ctx, oldSID, sess.UID, sess.DeviceID)
	}

	if err := m.dao.AddSession(ctx, sess.SID, sess.UID, sess.Key, sess.DeviceID, sess.Platform, sess.Server); err != nil {
		return fmt.Errorf("add session: %w", err)
	}

	// Invalidate local cache
	m.local.Delete(sess.UID)
	return nil
}

// GetSessions returns all active sessions for a user.
func (m *SessionManager) GetSessions(ctx context.Context, uid int64) ([]*Session, error) {
	// Check local cache first
	if cached, ok := m.local.Load(uid); ok {
		sessions := cached.([]*Session)
		if len(sessions) > 0 && time.Since(sessions[0].cachedAt) < time.Minute {
			return sessions, nil
		}
	}

	// Query Redis
	sidMap, err := m.dao.GetUserSessions(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("get user sessions: %w", err)
	}

	var sessions []*Session
	for sid := range sidMap {
		sess, err := m.GetSession(ctx, sid)
		if err != nil || sess == nil {
			continue
		}
		sessions = append(sessions, sess)
	}

	// Update local cache
	for _, s := range sessions {
		s.cachedAt = time.Now()
	}
	m.local.Store(uid, sessions)
	return sessions, nil
}

// GetSession returns a single session by SID.
func (m *SessionManager) GetSession(ctx context.Context, sid string) (*Session, error) {
	data, err := m.dao.GetSession(ctx, sid)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	sess := &Session{
		SID:      sid,
		Key:      data["key"],
		DeviceID: data["device_id"],
		Platform: data["platform"],
		Server:   data["server"],
	}
	if v, ok := data["uid"]; ok {
		fmt.Sscanf(v, "%d", &sess.UID)
	}
	if v, ok := data["created_at"]; ok {
		var ts int64
		fmt.Sscanf(v, "%d", &ts)
		sess.CreatedAt = time.UnixMilli(ts)
	}
	if v, ok := data["last_hb"]; ok {
		var ts int64
		fmt.Sscanf(v, "%d", &ts)
		sess.LastHBAt = time.UnixMilli(ts)
	}
	return sess, nil
}

// Heartbeat refreshes the session TTL by connection key.
func (m *SessionManager) Heartbeat(ctx context.Context, key string, uid int64) error {
	sid, err := m.dao.GetSessionByKey(ctx, key)
	if err != nil {
		return err
	}
	if sid == "" {
		return nil
	}
	return m.dao.ExpireSession(ctx, sid, uid)
}

// Kick forcibly removes a session and notifies Comet to close the connection.
func (m *SessionManager) Kick(ctx context.Context, sid string, uid int64, deviceID string) error {
	// Get session info before deleting (need server + key for Comet notification)
	sess, _ := m.GetSession(ctx, sid)

	key := ""
	if sess != nil {
		key = sess.Key
	}
	if err := m.dao.DelSession(ctx, sid, uid, deviceID, key); err != nil {
		return fmt.Errorf("del session: %w", err)
	}
	// Invalidate local cache
	m.local.Delete(uid)

	// Notify Comet to close the TCP/WS connection
	if m.kicker != nil && sess != nil && sess.Server != "" && sess.Key != "" {
		if err := m.kicker.KickConnection(ctx, sess.Server, sess.Key); err != nil {
			log.Warningf("kick comet connection failed: server=%s key=%s err=%v", sess.Server, sess.Key, err)
		}
	}
	return nil
}

// Disconnect removes a session by its connection key.
func (m *SessionManager) Disconnect(ctx context.Context, uid int64, key, server string) error {
	sid, err := m.dao.GetSessionByKey(ctx, key)
	if err != nil {
		return err
	}
	if sid == "" {
		return nil
	}
	sess, _ := m.GetSession(ctx, sid)
	deviceID := ""
	if sess != nil {
		deviceID = sess.DeviceID
	}
	return m.Kick(ctx, sid, uid, deviceID)
}

// IsOnline checks if a user has any active sessions.
func (m *SessionManager) IsOnline(ctx context.Context, uid int64) (bool, []*Session) {
	sessions, err := m.GetSessions(ctx, uid)
	if err != nil || len(sessions) == 0 {
		return false, nil
	}
	return true, sessions
}
