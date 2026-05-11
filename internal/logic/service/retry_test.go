package service

import (
	"testing"
	"time"
)

func TestRetryWorker_StartStop(t *testing.T) {
	msgDAO := newMockMessageDAO()
	pushDAO := newMockPushDAO()
	sessDAO := newMockSessionDAO()
	pusher := newMockCometPusher()
	retryDAO := newMockRetryDAO()

	sessMgr := NewSessionManager(sessDAO, 10*time.Minute)
	ackSvc := NewAckService(msgDAO, pushDAO)
	pushSvc := NewPushService(pushDAO, msgDAO, sessMgr, ackSvc, pusher)

	w := NewRetryWorker(ackSvc, pushSvc, retryDAO, msgDAO)
	w.Start()

	// Let it run briefly
	time.Sleep(50 * time.Millisecond)

	w.Stop()
	// If we reach here without hanging, Start/Stop works correctly
}

func TestRetryWorker_EnqueueForRetry(t *testing.T) {
	msgDAO := newMockMessageDAO()
	pushDAO := newMockPushDAO()
	sessDAO := newMockSessionDAO()
	pusher := newMockCometPusher()
	retryDAO := newMockRetryDAO()

	sessMgr := NewSessionManager(sessDAO, 10*time.Minute)
	ackSvc := NewAckService(msgDAO, pushDAO)
	pushSvc := NewPushService(pushDAO, msgDAO, sessMgr, ackSvc, pusher)

	w := NewRetryWorker(ackSvc, pushSvc, retryDAO, msgDAO)

	// Should not panic
	w.EnqueueForRetry(nil, "msg-1", 1001)
}
