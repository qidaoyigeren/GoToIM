package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/policy"
)

// OutboxWorkerConfig controls durable delivery polling.
type OutboxWorkerConfig struct {
	Enabled      bool
	BatchSize    int
	PollInterval time.Duration
	MaxRetries   int64
	LockTTL      time.Duration
}

// DefaultOutboxWorkerConfig returns production-shaped local defaults.
func DefaultOutboxWorkerConfig() OutboxWorkerConfig {
	return OutboxWorkerConfig{
		Enabled:      true,
		BatchSize:    100,
		PollInterval: time.Second,
		MaxRetries:   5,
		LockTTL:      30 * time.Second,
	}
}

// OutboxWorker asynchronously delivers notification_outbox rows.
type OutboxWorker struct {
	svc      *OrderNotifyService
	cfg      OutboxWorkerConfig
	workerID string
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewOutboxWorker creates a worker for the order notification service.
func NewOutboxWorker(svc *OrderNotifyService, cfg OutboxWorkerConfig) *OutboxWorker {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
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
	return &OutboxWorker{
		svc:      svc,
		cfg:      cfg,
		workerID: fmt.Sprintf("notify-outbox-%d", time.Now().UnixNano()),
		stopCh:   make(chan struct{}),
	}
}

// Start begins polling in the background.
func (w *OutboxWorker) Start() {
	if !w.cfg.Enabled {
		return
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.cfg.PollInterval)
		defer ticker.Stop()
		for {
			_ = w.ProcessOnce(context.Background())
			select {
			case <-ticker.C:
			case <-w.stopCh:
				return
			}
		}
	}()
}

// Stop gracefully stops the worker.
func (w *OutboxWorker) Stop() {
	close(w.stopCh)
	w.wg.Wait()
}

// ProcessOnce claims and handles one batch. It is intentionally public for tests.
func (w *OutboxWorker) ProcessOnce(ctx context.Context) error {
	outboxes, err := w.svc.store.ClaimOutbox(w.workerID, w.cfg.BatchSize, w.cfg.LockTTL)
	if err != nil {
		return err
	}
	for _, outbox := range outboxes {
		if err := w.svc.processOutbox(ctx, outbox, w.cfg.MaxRetries); err != nil {
			return err
		}
	}
	return nil
}

func (s *OrderNotifyService) processOutbox(ctx context.Context, outbox *model.NotificationOutbox, workerMaxRetries int64) error {
	now := time.Now()
	maxRetries := workerMaxRetries
	p := policy.Resolve(outbox.BusinessType, outbox.EventType)
	if p.MaxRetries > 0 && p.MaxRetries < maxRetries {
		maxRetries = p.MaxRetries
	}
	if outbox.TTLSeconds > 0 && now.After(outbox.CreatedAt.Add(time.Duration(outbox.TTLSeconds)*time.Second)) {
		return s.moveOutboxTerminal(outbox, "expired", "ttl_expired", "notification ttl expired", nil)
	}
	if s.pushClient == nil {
		err := &PushError{Type: "connection_refused", Message: "push client is not configured", Retryable: true}
		return s.handleOutboxFailure(outbox, err, maxRetries)
	}

	started := time.Now()
	var payload any
	if err := json.Unmarshal([]byte(outbox.PayloadJSON), &payload); err != nil {
		return s.moveOutboxTerminal(outbox, "dlq", "invalid_payload", err.Error(), &model.NotificationAttempt{
			AttemptID:    generateAttemptID(),
			NotifyID:     outbox.NotifyID,
			Channel:      "logic_push",
			Target:       outbox.UserID,
			Status:       "dlq",
			ErrorMessage: err.Error(),
			StartedAt:    started,
			FinishedAt:   ptrTime(time.Now()),
		})
	}

	var err error
	if mid, parseErr := strconv.ParseInt(outbox.UserID, 10, 64); parseErr == nil {
		_, err = s.pushClient.PushJSONToUsersContext(ctx, protocol.OpRaw, []int64{mid}, payload)
	} else {
		_, err = s.pushClient.PushJSONToUserContext(ctx, protocol.OpRaw, extractKeysFromMids([]string{outbox.UserID}), payload)
	}
	finished := time.Now()
	if err == nil {
		return s.store.MarkOutboxSent(outbox.OutboxID, &model.NotificationAttempt{
			AttemptID:  generateAttemptID(),
			NotifyID:   outbox.NotifyID,
			Channel:    "logic_push",
			Target:     outbox.UserID,
			Status:     "direct_sent",
			StartedAt:  started,
			FinishedAt: &finished,
		}, finished)
	}
	return s.handleOutboxFailure(outbox, err, maxRetries)
}

func (s *OrderNotifyService) handleOutboxFailure(outbox *model.NotificationOutbox, err error, maxRetries int64) error {
	finished := time.Now()
	nextRetry := outbox.RetryCount + 1
	attempt := &model.NotificationAttempt{
		AttemptID:    generateAttemptID(),
		NotifyID:     outbox.NotifyID,
		Channel:      "logic_push",
		Target:       outbox.UserID,
		Status:       "direct_failed",
		ErrorMessage: err.Error(),
		StartedAt:    finished,
		FinishedAt:   &finished,
	}
	if IsRetryablePushError(err) && nextRetry <= maxRetries {
		delay := time.Duration(1<<minInt64(nextRetry-1, 6)) * time.Second
		return s.store.MarkOutboxRetry(outbox.OutboxID, nextRetry, finished.Add(delay), err.Error(), attempt)
	}
	return s.moveOutboxTerminal(outbox, "dlq", pushFailureReason(err), err.Error(), attempt)
}

func (s *OrderNotifyService) moveOutboxTerminal(outbox *model.NotificationOutbox, status, reason, lastError string, attempt *model.NotificationAttempt) error {
	return s.store.MoveOutboxToDLQ(outbox, &model.NotificationDLQ{
		DLQID:       generateDLQID(),
		NotifyID:    outbox.NotifyID,
		OutboxID:    outbox.OutboxID,
		UserID:      outbox.UserID,
		OrderID:     outbox.OrderID,
		Reason:      reason,
		LastError:   lastError,
		PayloadJSON: outbox.PayloadJSON,
		RetryCount:  outbox.RetryCount,
		CreatedAt:   time.Now(),
	}, attempt, status)
}

func pushFailureReason(err error) string {
	var pe *PushError
	if errors.As(err, &pe) {
		return pe.Type
	}
	return "push_failed"
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
