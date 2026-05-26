package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// OutboxRelayConfig controls publishing notification_outbox rows into MQ.
type OutboxRelayConfig struct {
	Enabled      bool
	BatchSize    int
	WorkerCount  int
	PollInterval time.Duration
	MaxRetries   int64
	LockTTL      time.Duration
}

// DefaultOutboxRelayConfig returns production-shaped local defaults.
func DefaultOutboxRelayConfig() OutboxRelayConfig {
	return OutboxRelayConfig{
		Enabled:      true,
		BatchSize:    100,
		WorkerCount:  4,
		PollInterval: time.Second,
		MaxRetries:   5,
		LockTTL:      30 * time.Second,
	}
}

// OutboxRelayStore is the persistence surface used by OutboxRelay.
type OutboxRelayStore interface {
	ClaimOutbox(workerID string, batchSize int, lockTTL time.Duration) ([]*model.NotificationOutbox, error)
	GetNotification(notifyID string) (*model.Notification, error)
	MarkOutboxPublished(outboxID, workerID string, attempt *model.NotificationAttempt, at time.Time) error
	MarkOutboxPublishRetry(outboxID, workerID string, retryCount int64, nextRetryAt time.Time, lastError string, attempt *model.NotificationAttempt) error
	MoveOutboxToDLQ(outbox *model.NotificationOutbox, dlq *model.NotificationDLQ, attempt *model.NotificationAttempt, status string) error
	MarkOutboxExpired(outboxID, notifyID, lastError string, at time.Time) error
}

// NotifyEnvelopePublisher publishes one business envelope.
type NotifyEnvelopePublisher interface {
	Publish(ctx context.Context, env *mq.BizEnvelope) error
}

// OutboxRelay turns durable MySQL outbox rows into MQ business events.
type OutboxRelay struct {
	store     OutboxRelayStore
	publisher NotifyEnvelopePublisher
	cfg       OutboxRelayConfig
	workerID  string
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// NewOutboxRelay creates an outbox relay.
func NewOutboxRelay(store OutboxRelayStore, publisher NotifyEnvelopePublisher, cfg OutboxRelayConfig) *OutboxRelay {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = 30 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 5
	}
	return &OutboxRelay{
		store:     store,
		publisher: publisher,
		cfg:       cfg,
		workerID:  fmt.Sprintf("notify-relay-%d", time.Now().UnixNano()),
		stopCh:    make(chan struct{}),
	}
}

// Start begins polling in the background.
func (r *OutboxRelay) Start() {
	if r == nil || !r.cfg.Enabled {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(r.cfg.PollInterval)
		defer ticker.Stop()
		for {
			if err := r.ProcessOnce(context.Background()); err != nil {
				log.Printf("[outbox_relay] process_once failed worker_id=%s err=%v", r.workerID, err)
			}
			select {
			case <-ticker.C:
			case <-r.stopCh:
				return
			}
		}
	}()
}

// Stop gracefully stops the relay.
func (r *OutboxRelay) Stop() {
	if r == nil {
		return
	}
	close(r.stopCh)
	r.wg.Wait()
}

// ProcessOnce claims and publishes one batch.
func (r *OutboxRelay) ProcessOnce(ctx context.Context) error {
	outboxes, err := r.store.ClaimOutbox(r.workerID, r.cfg.BatchSize, r.cfg.LockTTL)
	if err != nil {
		return err
	}
	if len(outboxes) == 0 {
		return nil
	}
	workerCount := r.cfg.WorkerCount
	if workerCount > len(outboxes) {
		workerCount = len(outboxes)
	}
	jobs := make(chan *model.NotificationOutbox)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for outbox := range jobs {
				if err := r.publishOutbox(ctx, outbox); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, outbox := range outboxes {
		jobs <- outbox
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

func (r *OutboxRelay) publishOutbox(ctx context.Context, outbox *model.NotificationOutbox) error {
	now := time.Now()
	traceID := nonEmptyString(outbox.TraceID, outbox.NotifyID)
	if outbox.TTLSeconds > 0 && now.After(outbox.CreatedAt.Add(time.Duration(outbox.TTLSeconds)*time.Second)) {
		log.Printf("[outbox_relay] ttl expired trace_id=%s notify_id=%s business_type=%s event_type=%s outbox_id=%s",
			traceID, outbox.NotifyID, outbox.BusinessType, outbox.EventType, outbox.OutboxID)
		return r.store.MarkOutboxExpired(outbox.OutboxID, outbox.NotifyID, "ttl_expired", now)
	}

	notif, _ := r.store.GetNotification(outbox.NotifyID)
	env := buildNotifyEnvelope(outbox, notif, r.cfg.MaxRetries)
	if err := r.publisher.Publish(ctx, env); err != nil {
		return r.handlePublishFailure(outbox, err)
	}
	finished := time.Now()
	attempt := &model.NotificationAttempt{
		AttemptID:  generateAttemptID(),
		NotifyID:   outbox.NotifyID,
		Channel:    "notify_mq",
		Target:     outbox.UserID,
		Status:     "published",
		Path:       "notify_mq",
		AttemptNo:  outbox.RetryCount + 1,
		TraceID:    traceID,
		StartedAt:  now,
		FinishedAt: &finished,
	}
	if err := r.store.MarkOutboxPublished(outbox.OutboxID, r.workerID, attempt, finished); err != nil {
		return err
	}
	metrics.NotifyOutboxPublishedTotal.Inc()
	log.Printf("[outbox_relay] published trace_id=%s notify_id=%s business_type=%s event_type=%s outbox_id=%s",
		traceID, outbox.NotifyID, outbox.BusinessType, outbox.EventType, outbox.OutboxID)
	return nil
}

func (r *OutboxRelay) handlePublishFailure(outbox *model.NotificationOutbox, err error) error {
	finished := time.Now()
	nextRetry := outbox.RetryCount + 1
	traceID := nonEmptyString(outbox.TraceID, outbox.NotifyID)
	attempt := &model.NotificationAttempt{
		AttemptID:    generateAttemptID(),
		NotifyID:     outbox.NotifyID,
		Channel:      "notify_mq",
		Target:       outbox.UserID,
		Status:       "publish_failed",
		Path:         "notify_mq",
		ErrorCode:    "mq_publish_failed",
		ErrorMessage: err.Error(),
		AttemptNo:    nextRetry,
		TraceID:      traceID,
		StartedAt:    finished,
		FinishedAt:   &finished,
	}
	metrics.NotifyOutboxPublishFailedTotal.Inc()
	log.Printf("[outbox_relay] publish failed trace_id=%s notify_id=%s business_type=%s event_type=%s outbox_id=%s retry=%d err=%v",
		traceID, outbox.NotifyID, outbox.BusinessType, outbox.EventType, outbox.OutboxID, nextRetry, err)
	if nextRetry <= r.cfg.MaxRetries {
		delay := time.Duration(1<<minInt64(nextRetry-1, 6)) * time.Second
		return r.store.MarkOutboxPublishRetry(outbox.OutboxID, r.workerID, nextRetry, finished.Add(delay), err.Error(), attempt)
	}
	return r.store.MoveOutboxToDLQ(outbox, &model.NotificationDLQ{
		DLQID:                generateDLQID(),
		NotifyID:             outbox.NotifyID,
		OutboxID:             outbox.OutboxID,
		UserID:               outbox.UserID,
		OrderID:              outbox.OrderID,
		BusinessType:         outbox.BusinessType,
		Reason:               "mq_publish_failed",
		LastError:            err.Error(),
		PayloadJSON:          outbox.PayloadJSON,
		RetryCount:           outbox.RetryCount,
		CompensationStrategy: outbox.CompensationStrategy,
		TraceID:              traceID,
		CreatedAt:            finished,
	}, attempt, "dlq")
}
