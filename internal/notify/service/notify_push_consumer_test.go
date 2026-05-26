package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/Terry-Mao/goim/internal/notify/model"
)

type fakePushStore struct {
	mu       sync.Mutex
	outbox   *model.NotificationOutbox
	attempts []*model.NotificationAttempt
	dlq      []*model.NotificationDLQ
	expired  bool
}

func (s *fakePushStore) GetOutbox(outboxID string) (*model.NotificationOutbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outbox == nil || s.outbox.OutboxID != outboxID {
		return nil, errors.New("outbox not found")
	}
	cp := *s.outbox
	return &cp, nil
}

func (s *fakePushStore) ClaimPublishedOutbox(outboxID, workerID string, lockTTL time.Duration) (*model.NotificationOutbox, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outbox == nil || s.outbox.OutboxID != outboxID {
		return nil, false, errors.New("outbox not found")
	}
	switch s.outbox.Status {
	case "published":
		s.outbox.Status = "delivering"
		s.outbox.LockedBy = workerID
		lockedUntil := time.Now().Add(lockTTL)
		s.outbox.LockedUntil = lockedUntil
		cp := *s.outbox
		return &cp, true, nil
	case "delivering":
		if s.outbox.LockedUntil.IsZero() || time.Now().After(s.outbox.LockedUntil) {
			s.outbox.LockedBy = workerID
			s.outbox.LockedUntil = time.Now().Add(lockTTL)
			cp := *s.outbox
			return &cp, true, nil
		}
		return nil, false, nil
	default:
		return nil, false, nil
	}
}

func (s *fakePushStore) MarkOutboxSent(outboxID string, attempt *model.NotificationAttempt, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outbox == nil || s.outbox.OutboxID != outboxID {
		return errors.New("outbox not found")
	}
	s.outbox.Status = "sent"
	s.attempts = append(s.attempts, attempt)
	return nil
}

func (s *fakePushStore) InsertAttempt(a *model.NotificationAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, a)
	return nil
}

func (s *fakePushStore) MoveOutboxToDLQ(outbox *model.NotificationOutbox, dlq *model.NotificationDLQ, attempt *model.NotificationAttempt, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outbox.Status = status
	s.dlq = append(s.dlq, dlq)
	s.attempts = append(s.attempts, attempt)
	return nil
}

func (s *fakePushStore) MarkOutboxExpired(outboxID, notifyID, lastError string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outbox != nil {
		s.outbox.Status = "expired"
		s.outbox.LastError = lastError
	}
	s.expired = true
	return nil
}

type fakeNotifyPusher struct {
	mu        sync.Mutex
	userCalls int
	roomCalls int
	err       error
	results   []DeliveryResult
}

func (p *fakeNotifyPusher) PushToRoomContext(ctx context.Context, op int32, roomType, roomID string, msg []byte) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.roomCalls++
	if p.err != nil {
		return "", p.err
	}
	return "msg-room-1", nil
}

func (p *fakeNotifyPusher) PushJSONToUsersDetailedContext(ctx context.Context, op int32, mids []int64, data interface{}) ([]DeliveryResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.userCalls++
	if p.err != nil {
		return nil, p.err
	}
	if len(p.results) > 0 {
		return p.results, nil
	}
	return []DeliveryResult{{MsgID: "msg-1", Path: "logic_push", LatencyMs: 2, AttemptNo: 1}}, nil
}

func (p *fakeNotifyPusher) PushJSONToUserDetailedContext(ctx context.Context, op int32, keys []string, data interface{}) ([]DeliveryResult, error) {
	return p.PushJSONToUsersDetailedContext(ctx, op, nil, data)
}

func TestNotifyPushConsumerDeliversEnvelopeAndMarksSent(t *testing.T) {
	outbox := testOutbox("obx-consume-1", "ntf-consume-1", time.Now())
	outbox.Status = "published"
	store := &fakePushStore{outbox: outbox}
	pusher := &fakeNotifyPusher{}
	consumer := NewNotifyPushConsumer(store, pusher, nil, NotifyPushConsumerConfig{Enabled: true, MaxInflight: 4})
	msg := notifyMessageFromOutbox(t, outbox)

	if err := consumer.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if pusher.userCalls != 1 {
		t.Fatalf("push calls = %d, want 1", pusher.userCalls)
	}
	if store.outbox.Status != "sent" {
		t.Fatalf("outbox status = %s, want sent", store.outbox.Status)
	}
	if len(store.attempts) != 1 || store.attempts[0].Status != "direct_sent" {
		t.Fatalf("attempts = %+v, want one direct_sent", store.attempts)
	}
}

func TestNotifyPushConsumerSkipsTerminalOutboxWithoutPush(t *testing.T) {
	outbox := testOutbox("obx-consume-terminal", "ntf-consume-terminal", time.Now())
	outbox.Status = "sent"
	store := &fakePushStore{outbox: outbox}
	pusher := &fakeNotifyPusher{}
	consumer := NewNotifyPushConsumer(store, pusher, nil, NotifyPushConsumerConfig{Enabled: true, MaxInflight: 1})
	msg := notifyMessageFromOutbox(t, outbox)

	if err := consumer.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if pusher.userCalls != 0 || pusher.roomCalls != 0 {
		t.Fatalf("push calls = user:%d room:%d, want none", pusher.userCalls, pusher.roomCalls)
	}
}

func TestNotifyPushConsumerReturnsErrorForActiveDeliveryLock(t *testing.T) {
	outbox := testOutbox("obx-consume-locked", "ntf-consume-locked", time.Now())
	outbox.Status = "delivering"
	outbox.LockedUntil = time.Now().Add(time.Minute)
	store := &fakePushStore{outbox: outbox}
	pusher := &fakeNotifyPusher{}
	consumer := NewNotifyPushConsumer(store, pusher, nil, NotifyPushConsumerConfig{Enabled: true, MaxInflight: 1})
	msg := notifyMessageFromOutbox(t, outbox)

	if err := consumer.ProcessMessage(context.Background(), msg); err == nil {
		t.Fatal("ProcessMessage returned nil, want active delivery lock error")
	}
	if pusher.userCalls != 0 {
		t.Fatalf("push calls = %d, want 0", pusher.userCalls)
	}
}

func TestNotifyPushConsumerExpiredEnvelopeDoesNotPush(t *testing.T) {
	outbox := testOutbox("obx-consume-2", "ntf-consume-2", time.Now().Add(-time.Minute))
	outbox.Status = "published"
	outbox.TTLSeconds = 1
	store := &fakePushStore{outbox: outbox}
	pusher := &fakeNotifyPusher{}
	consumer := NewNotifyPushConsumer(store, pusher, nil, NotifyPushConsumerConfig{Enabled: true, MaxInflight: 1})
	msg := notifyMessageFromOutbox(t, outbox)

	if err := consumer.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if pusher.userCalls != 0 || pusher.roomCalls != 0 {
		t.Fatalf("push calls = user:%d room:%d, want none", pusher.userCalls, pusher.roomCalls)
	}
	if !store.expired || store.outbox.Status != "expired" {
		t.Fatalf("expired=%v status=%s, want true/expired", store.expired, store.outbox.Status)
	}
}

func TestNotifyPushConsumerPermanentFailureMovesToDLQ(t *testing.T) {
	outbox := testOutbox("obx-consume-3", "ntf-consume-3", time.Now())
	outbox.Status = "published"
	store := &fakePushStore{outbox: outbox}
	pusher := &fakeNotifyPusher{err: &PushError{Type: "http_4xx", Class: "permanent", Message: "bad request", Retryable: false}}
	consumer := NewNotifyPushConsumer(store, pusher, nil, NotifyPushConsumerConfig{Enabled: true, MaxInflight: 1})
	msg := notifyMessageFromOutbox(t, outbox)

	if err := consumer.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if store.outbox.Status != "dlq" || len(store.dlq) != 1 {
		t.Fatalf("outbox status=%s dlq=%d, want dlq/1", store.outbox.Status, len(store.dlq))
	}
}

func notifyMessageFromOutbox(t *testing.T, outbox *model.NotificationOutbox) *mq.Message {
	t.Helper()
	env := buildNotifyEnvelope(outbox, &model.Notification{NotifyID: outbox.NotifyID, AckPolicy: "any_device"}, 3)
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return &mq.Message{
		Topic:   "notify.push",
		Key:     outbox.UserID,
		Value:   data,
		Headers: notifyEnvelopeHeaders(env),
	}
}
