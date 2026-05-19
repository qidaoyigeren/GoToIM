package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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
		err := &PushError{Type: "connection_refused", Class: "transient", Message: "push client is not configured", Retryable: true}
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
			Path:         "failed",
			ErrorCode:    "invalid_payload",
			ErrorMessage: err.Error(),
			StartedAt:    started,
			FinishedAt:   ptrTime(time.Now()),
		})
	}

	var results []DeliveryResult
	var err error
	if strings.HasPrefix(outbox.UserID, "room:") {
		roomID := strings.TrimPrefix(outbox.UserID, "room:")
		msgID, pushErr := s.pushClient.PushToRoomContext(ctx, protocol.OpRaw, "live", roomID, []byte(outbox.PayloadJSON))
		err = pushErr
		if err == nil {
			results = []DeliveryResult{{
				MsgID:      msgID,
				Path:       "logic_push",
				TargetNode: outbox.UserID,
				LatencyMs:  float64(time.Since(started).Microseconds()) / 1000.0,
				AttemptNo:  outbox.RetryCount + 1,
			}}
		}
	} else if mid, parseErr := strconv.ParseInt(outbox.UserID, 10, 64); parseErr == nil {
		results, err = s.pushClient.PushJSONToUsersDetailedContext(ctx, protocol.OpRaw, []int64{mid}, payload)
	} else {
		results, err = s.pushClient.PushJSONToUserDetailedContext(ctx, protocol.OpRaw, extractKeysFromMids([]string{outbox.UserID}), payload)
	}
	finished := time.Now()
	if err == nil {
		result := firstDeliveryResult(results)
		if result.Path == "" {
			result.Path = "logic_push"
		}
		if result.AttemptNo == 0 {
			result.AttemptNo = outbox.RetryCount + 1
		}
		if result.LatencyMs == 0 {
			result.LatencyMs = float64(finished.Sub(started).Microseconds()) / 1000.0
		}
		if deliveryResultFailed(result) {
			return s.handleOutboxDeliveryResultFailure(outbox, result, maxRetries, started, finished)
		}
		err := s.store.MarkOutboxSent(outbox.OutboxID, &model.NotificationAttempt{
			AttemptID:    generateAttemptID(),
			NotifyID:     outbox.NotifyID,
			Channel:      result.Path,
			Target:       outbox.UserID,
			Status:       attemptStatusForPath(result.Path),
			Path:         result.Path,
			TargetNode:   result.TargetNode,
			ErrorCode:    result.ErrorCode,
			ErrorMessage: result.ErrorMessage,
			LatencyMs:    result.LatencyMs,
			AttemptNo:    result.AttemptNo,
			StartedAt:    started,
			FinishedAt:   &finished,
		}, finished)
		if err == nil {
			s.incrementScenarioRun(outbox.ScenarioRunID, 0, 0, 1, 0, 0, 0)
		}
		return err
	}
	return s.handleOutboxFailure(outbox, err, maxRetries)
}

func (s *OrderNotifyService) handleOutboxDeliveryResultFailure(outbox *model.NotificationOutbox, result DeliveryResult, maxRetries int64, started, finished time.Time) error {
	err := pushErrorFromDeliveryResult(result)
	nextRetry := outbox.RetryCount + 1
	attempt := &model.NotificationAttempt{
		AttemptID:    generateAttemptID(),
		NotifyID:     outbox.NotifyID,
		Channel:      nonEmptyString(result.Path, "logic_push"),
		Target:       outbox.UserID,
		Status:       "direct_failed",
		Path:         "failed",
		TargetNode:   result.TargetNode,
		ErrorCode:    pushFailureReason(err),
		ErrorMessage: err.Error(),
		LatencyMs:    result.LatencyMs,
		AttemptNo:    nonZeroInt64(result.AttemptNo, nextRetry),
		StartedAt:    started,
		FinishedAt:   &finished,
	}
	if IsRetryablePushError(err) && nextRetry <= maxRetries && !IsPermanentPushError(err) {
		delay := time.Duration(1<<minInt64(nextRetry-1, 6)) * time.Second
		s.incrementScenarioRun(outbox.ScenarioRunID, 0, 0, 0, 0, 1, 0)
		return s.store.MarkOutboxRetry(outbox.OutboxID, nextRetry, finished.Add(delay), err.Error(), attempt)
	}
	s.incrementScenarioRun(outbox.ScenarioRunID, 0, 0, 0, 0, 1, 1)
	return s.moveOutboxTerminal(outbox, "dlq", pushFailureReason(err), err.Error(), attempt)
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
		Path:         "failed",
		ErrorCode:    pushFailureReason(err),
		ErrorMessage: err.Error(),
		LatencyMs:    0,
		AttemptNo:    nextRetry,
		StartedAt:    finished,
		FinishedAt:   &finished,
	}
	if IsRetryablePushError(err) && nextRetry <= maxRetries && !IsPermanentPushError(err) {
		delay := time.Duration(1<<minInt64(nextRetry-1, 6)) * time.Second
		s.incrementScenarioRun(outbox.ScenarioRunID, 0, 0, 0, 0, 1, 0)
		return s.store.MarkOutboxRetry(outbox.OutboxID, nextRetry, finished.Add(delay), err.Error(), attempt)
	}
	s.incrementScenarioRun(outbox.ScenarioRunID, 0, 0, 0, 0, 1, 1)
	return s.moveOutboxTerminal(outbox, "dlq", pushFailureReason(err), err.Error(), attempt)
}

func deliveryResultFailed(result DeliveryResult) bool {
	return result.Path == "failed" || result.ErrorCode != ""
}

func pushErrorFromDeliveryResult(result DeliveryResult) error {
	errorCode := nonEmptyString(result.ErrorCode, "delivery_failed")
	message := result.ErrorMessage
	if message == "" {
		message = "logic returned failed delivery result"
	}
	class := "transient"
	retryable := true
	if errorCode == "http_4xx" || errorCode == "logic_error" || errorCode == "invalid_request" {
		class = "permanent"
		retryable = false
	}
	return &PushError{Type: errorCode, Class: class, Message: message, Retryable: retryable}
}

func firstDeliveryResult(results []DeliveryResult) DeliveryResult {
	if len(results) == 0 {
		return DeliveryResult{}
	}
	return results[0]
}

func attemptStatusForPath(path string) string {
	switch path {
	case "kafka_fallback", "offline_stored":
		return "fallback_sent"
	case "failed":
		return "direct_failed"
	default:
		return "direct_sent"
	}
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

func nonEmptyString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func nonZeroInt64(v, fallback int64) int64 {
	if v == 0 {
		return fallback
	}
	return v
}
