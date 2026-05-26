package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/Terry-Mao/goim/internal/notify/model"
)

type fakeMQProducer struct {
	mu       sync.Mutex
	err      error
	messages []*mq.Message
	topic    string
	uid      int64
}

func (p *fakeMQProducer) EnqueueToUser(ctx context.Context, uid int64, msg *mq.Message) error {
	return p.EnqueueToTopic(ctx, msg.Topic, uid, msg)
}

func (p *fakeMQProducer) EnqueueToTopic(ctx context.Context, topic string, uid int64, msg *mq.Message) error {
	if p.err != nil {
		return p.err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := *msg
	cp.Topic = topic
	p.messages = append(p.messages, &cp)
	p.topic = topic
	p.uid = uid
	return nil
}

func (p *fakeMQProducer) EnqueueToUsers(ctx context.Context, uids []int64, msg *mq.Message) error {
	return nil
}

func (p *fakeMQProducer) EnqueueToRoom(ctx context.Context, roomID string, msg *mq.Message) error {
	return nil
}

func (p *fakeMQProducer) EnqueueBroadcast(ctx context.Context, msg *mq.Message, speed int32) error {
	return nil
}

func (p *fakeMQProducer) EnqueueACK(ctx context.Context, msgID string, uid int64, status, targetNode string) error {
	return nil
}

func (p *fakeMQProducer) EnqueueDelayed(ctx context.Context, uid int64, msg *mq.Message, delayMs int64) error {
	return nil
}

func (p *fakeMQProducer) Close() error { return nil }

type fakeRelayStore struct {
	mu            sync.Mutex
	outboxes      map[string]*model.NotificationOutbox
	notifications map[string]*model.Notification
	attempts      []*model.NotificationAttempt
	dlq           []*model.NotificationDLQ
}

func newFakeRelayStore(outboxes ...*model.NotificationOutbox) *fakeRelayStore {
	s := &fakeRelayStore{
		outboxes:      make(map[string]*model.NotificationOutbox),
		notifications: make(map[string]*model.Notification),
	}
	for _, o := range outboxes {
		cp := *o
		s.outboxes[o.OutboxID] = &cp
		s.notifications[o.NotifyID] = &model.Notification{NotifyID: o.NotifyID, AckPolicy: "any_device"}
	}
	return s
}

func (s *fakeRelayStore) ClaimOutbox(workerID string, batchSize int, lockTTL time.Duration) ([]*model.NotificationOutbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.NotificationOutbox
	now := time.Now()
	for _, o := range s.outboxes {
		if len(out) >= batchSize {
			break
		}
		if o.Status != "pending" && o.Status != "failed" {
			continue
		}
		o.Status = "processing"
		o.LockedBy = workerID
		o.LockedUntil = now.Add(lockTTL)
		cp := *o
		out = append(out, &cp)
	}
	return out, nil
}

func (s *fakeRelayStore) GetNotification(notifyID string) (*model.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.notifications[notifyID]; n != nil {
		cp := *n
		return &cp, nil
	}
	return nil, nil
}

func (s *fakeRelayStore) MarkOutboxPublished(outboxID, workerID string, attempt *model.NotificationAttempt, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.outboxes[outboxID]
	if o == nil {
		return errors.New("outbox not found")
	}
	o.Status = "published"
	o.LockedBy = ""
	o.LockedUntil = time.Time{}
	s.attempts = append(s.attempts, attempt)
	return nil
}

func (s *fakeRelayStore) MarkOutboxPublishRetry(outboxID, workerID string, retryCount int64, nextRetryAt time.Time, lastError string, attempt *model.NotificationAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.outboxes[outboxID]
	if o == nil {
		return errors.New("outbox not found")
	}
	o.Status = "failed"
	o.RetryCount = retryCount
	o.NextRetryAt = nextRetryAt
	o.LastError = lastError
	o.LockedBy = ""
	o.LockedUntil = time.Time{}
	s.attempts = append(s.attempts, attempt)
	return nil
}

func (s *fakeRelayStore) MoveOutboxToDLQ(outbox *model.NotificationOutbox, dlq *model.NotificationDLQ, attempt *model.NotificationAttempt, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o := s.outboxes[outbox.OutboxID]; o != nil {
		o.Status = status
	}
	s.dlq = append(s.dlq, dlq)
	s.attempts = append(s.attempts, attempt)
	return nil
}

func (s *fakeRelayStore) MarkOutboxExpired(outboxID, notifyID, lastError string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o := s.outboxes[outboxID]; o != nil {
		o.Status = "expired"
		o.LastError = lastError
	}
	return nil
}

func TestOutboxRelayPublishesAndMarksPublished(t *testing.T) {
	outbox := testOutbox("obx-1", "ntf-1", time.Now())
	st := newFakeRelayStore(outbox)
	producer := &fakeMQProducer{}
	relay := NewOutboxRelay(st, NewNotifyEventProducer(producer, "notify.push"), OutboxRelayConfig{
		Enabled: true, BatchSize: 10, WorkerCount: 2, MaxRetries: 3, LockTTL: time.Minute,
	})

	if err := relay.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if got := st.outboxes[outbox.OutboxID].Status; got != "published" {
		t.Fatalf("outbox status = %q, want published", got)
	}
	if len(producer.messages) != 1 {
		t.Fatalf("published messages = %d, want 1", len(producer.messages))
	}
	if producer.topic != "notify.push" || producer.messages[0].Key != "1001" {
		t.Fatalf("published topic/key = %s/%s, want notify.push/1001", producer.topic, producer.messages[0].Key)
	}
}

func TestOutboxRelayPublishFailureSchedulesRetry(t *testing.T) {
	outbox := testOutbox("obx-2", "ntf-2", time.Now())
	st := newFakeRelayStore(outbox)
	producer := &fakeMQProducer{err: errors.New("kafka unavailable")}
	relay := NewOutboxRelay(st, NewNotifyEventProducer(producer, "notify.push"), OutboxRelayConfig{
		Enabled: true, BatchSize: 10, WorkerCount: 1, MaxRetries: 3, LockTTL: time.Minute,
	})

	if err := relay.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	got := st.outboxes[outbox.OutboxID]
	if got.Status != "failed" || got.RetryCount != 1 {
		t.Fatalf("outbox status/retry = %s/%d, want failed/1", got.Status, got.RetryCount)
	}
	if len(st.attempts) != 1 || st.attempts[0].Status != "publish_failed" {
		t.Fatalf("attempts = %+v, want one publish_failed", st.attempts)
	}
}

func TestClaimOutboxConcurrentDoesNotDuplicateRows(t *testing.T) {
	st := openMySQLTestStore(t)
	t.Cleanup(func() { _ = st.Close() })
	svc := NewOrderNotifyServiceWithStore(nil, st)
	t.Cleanup(func() { _ = svc.Close() })
	for i := 0; i < 6; i++ {
		if _, _, err := svc.CreateOrderIdempotent("claim-user", []model.OrderItem{{ProductName: "sku", Quantity: 1, Price: 1}}, 1, "claim-key-"+time.Now().Format(time.RFC3339Nano)+string(rune('a'+i))); err != nil {
			t.Fatalf("CreateOrderIdempotent: %v", err)
		}
	}

	var wg sync.WaitGroup
	claimed := make(chan string, 20)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			rows, err := st.ClaimOutbox(worker, 20, time.Minute)
			if err != nil {
				t.Errorf("ClaimOutbox(%s): %v", worker, err)
				return
			}
			for _, row := range rows {
				claimed <- row.OutboxID
			}
		}("relay-worker-" + string(rune('a'+i)))
	}
	wg.Wait()
	close(claimed)

	seen := map[string]bool{}
	for id := range claimed {
		if seen[id] {
			t.Fatalf("outbox %s claimed more than once", id)
		}
		seen[id] = true
	}
}

func testOutbox(outboxID, notifyID string, createdAt time.Time) *model.NotificationOutbox {
	return &model.NotificationOutbox{
		OutboxID:             outboxID,
		NotifyID:             notifyID,
		UserID:               "1001",
		OrderID:              "order-1",
		BusinessType:         "order",
		EventType:            "created",
		PayloadJSON:          `{"notify_id":"` + notifyID + `"}`,
		Priority:             "normal",
		TTLSeconds:           600,
		Status:               "pending",
		TraceID:              "trace-1",
		CompensationStrategy: "retry_then_dlq",
		CreatedAt:            createdAt,
		UpdatedAt:            createdAt,
	}
}
