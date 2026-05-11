package service

import (
	"context"
	"testing"
	"time"
)

func newTestSessionManager() (*SessionManager, *mockSessionDAO) {
	mock := newMockSessionDAO()
	mgr := NewSessionManager(mock, 10*time.Minute)
	return mgr, mock
}

func TestSessionManager_Create(t *testing.T) {
	mgr, mock := newTestSessionManager()
	ctx := context.Background()

	sess := &Session{
		SID:      "sid-1",
		UID:      1001,
		Key:      "key-1",
		DeviceID: "dev-1",
		Platform: "android",
		Server:   "comet-1",
	}
	if err := mgr.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify session was stored
	data, err := mock.GetSession(ctx, "sid-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if data["key"] != "key-1" {
		t.Errorf("key = %q, want %q", data["key"], "key-1")
	}
	if data["device_id"] != "dev-1" {
		t.Errorf("device_id = %q, want %q", data["device_id"], "dev-1")
	}
}

func TestSessionManager_Create_SameDeviceKick(t *testing.T) {
	mgr, mock := newTestSessionManager()
	ctx := context.Background()

	// Create first session
	sess1 := &Session{SID: "sid-1", UID: 1001, Key: "key-1", DeviceID: "dev-1", Platform: "android", Server: "comet-1"}
	if err := mgr.Create(ctx, sess1); err != nil {
		t.Fatalf("Create sess1: %v", err)
	}

	// Create second session on same device — should kick the first
	sess2 := &Session{SID: "sid-2", UID: 1001, Key: "key-2", DeviceID: "dev-1", Platform: "android", Server: "comet-1"}
	if err := mgr.Create(ctx, sess2); err != nil {
		t.Fatalf("Create sess2: %v", err)
	}

	// Old session should be gone
	data, _ := mock.GetSession(ctx, "sid-1")
	if data != nil {
		t.Error("old session sid-1 should have been kicked")
	}

	// New session should exist
	data, _ = mock.GetSession(ctx, "sid-2")
	if data == nil {
		t.Fatal("new session sid-2 should exist")
	}
	if data["key"] != "key-2" {
		t.Errorf("key = %q, want %q", data["key"], "key-2")
	}
}

func TestSessionManager_GetSessions(t *testing.T) {
	mgr, _ := newTestSessionManager()
	ctx := context.Background()

	// Create two sessions for the same user
	mgr.Create(ctx, &Session{SID: "s1", UID: 1001, Key: "k1", DeviceID: "d1", Platform: "android", Server: "c1"})
	mgr.Create(ctx, &Session{SID: "s2", UID: 1001, Key: "k2", DeviceID: "d2", Platform: "ios", Server: "c1"})

	sessions, err := mgr.GetSessions(ctx, 1001)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("session count = %d, want 2", len(sessions))
	}
}

func TestSessionManager_GetSessions_LocalCache(t *testing.T) {
	mgr, mock := newTestSessionManager()
	ctx := context.Background()

	mgr.Create(ctx, &Session{SID: "s1", UID: 1001, Key: "k1", DeviceID: "d1", Platform: "android", Server: "c1"})

	// First call populates cache
	sessions1, _ := mgr.GetSessions(ctx, 1001)

	// Delete from Redis directly — cache should still return data
	mock.mu.Lock()
	delete(mock.sessions, "s1")
	mock.mu.Unlock()

	sessions2, _ := mgr.GetSessions(ctx, 1001)
	if len(sessions2) != len(sessions1) {
		t.Error("local cache should have returned cached sessions")
	}
}

func TestSessionManager_Heartbeat(t *testing.T) {
	mgr, _ := newTestSessionManager()
	ctx := context.Background()

	mgr.Create(ctx, &Session{SID: "s1", UID: 1001, Key: "k1", DeviceID: "d1", Platform: "android", Server: "c1"})

	// Heartbeat by key
	if err := mgr.Heartbeat(ctx, "k1", 1001); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	// Heartbeat with unknown key should not error
	if err := mgr.Heartbeat(ctx, "unknown", 1001); err != nil {
		t.Fatalf("Heartbeat unknown key: %v", err)
	}
}

func TestSessionManager_Kick(t *testing.T) {
	mgr, mock := newTestSessionManager()
	ctx := context.Background()

	mgr.Create(ctx, &Session{SID: "s1", UID: 1001, Key: "k1", DeviceID: "d1", Platform: "android", Server: "c1"})

	if err := mgr.Kick(ctx, "s1", 1001, "d1"); err != nil {
		t.Fatalf("Kick: %v", err)
	}

	data, _ := mock.GetSession(ctx, "s1")
	if data != nil {
		t.Error("session should be deleted after kick")
	}
}

func TestSessionManager_Disconnect(t *testing.T) {
	mgr, mock := newTestSessionManager()
	ctx := context.Background()

	mgr.Create(ctx, &Session{SID: "s1", UID: 1001, Key: "k1", DeviceID: "d1", Platform: "android", Server: "c1"})

	if err := mgr.Disconnect(ctx, 1001, "k1", "c1"); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	data, _ := mock.GetSession(ctx, "s1")
	if data != nil {
		t.Error("session should be deleted after disconnect")
	}
}

func TestSessionManager_IsOnline(t *testing.T) {
	mgr, _ := newTestSessionManager()
	ctx := context.Background()

	// No sessions — offline
	online, sessions := mgr.IsOnline(ctx, 1001)
	if online {
		t.Error("should be offline with no sessions")
	}
	if len(sessions) != 0 {
		t.Error("sessions should be empty")
	}

	// Create a session — online
	mgr.Create(ctx, &Session{SID: "s1", UID: 1001, Key: "k1", DeviceID: "d1", Platform: "android", Server: "c1"})
	online, sessions = mgr.IsOnline(ctx, 1001)
	if !online {
		t.Error("should be online with active session")
	}
	if len(sessions) != 1 {
		t.Errorf("session count = %d, want 1", len(sessions))
	}
}
