package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// NotifyPushConsumerConfig controls notify.push consumption.
type NotifyPushConsumerConfig struct {
	Enabled      bool
	WorkerCount  int
	MaxInflight  int
	BatchSize    int
	PollInterval time.Duration
	MaxRetries   int64
	Backoff      time.Duration
}

// NotifyPushStore is the persistence surface used by NotifyPushConsumer.
type NotifyPushStore interface {
	GetOutbox(outboxID string) (*model.NotificationOutbox, error)
	ClaimPublishedOutbox(outboxID, workerID string, lockTTL time.Duration) (*model.NotificationOutbox, bool, error)
	MarkOutboxSent(outboxID string, attempt *model.NotificationAttempt, at time.Time) error
	InsertAttempt(a *model.NotificationAttempt) error
	MoveOutboxToDLQ(outbox *model.NotificationOutbox, dlq *model.NotificationDLQ, attempt *model.NotificationAttempt, status string) error
	MarkOutboxExpired(outboxID, notifyID, lastError string, at time.Time) error
}

// NotifyPushPusher is implemented by PushClient.
type NotifyPushPusher interface {
	PushToRoomContext(ctx context.Context, op int32, roomType, roomID string, msg []byte) (string, error)
	PushJSONToUsersDetailedContext(ctx context.Context, op int32, mids []int64, data interface{}) ([]DeliveryResult, error)
	PushJSONToUserDetailedContext(ctx context.Context, op int32, keys []string, data interface{}) ([]DeliveryResult, error)
}

// NotifyConsumerFactory creates one MQ consumer instance.
type NotifyConsumerFactory func() (mq.Consumer, error)

// NotifyPushConsumer consumes notify.push and delivers messages through Logic.
type NotifyPushConsumer struct {
	store     NotifyPushStore
	pusher    NotifyPushPusher
	factory   NotifyConsumerFactory
	cfg       NotifyPushConsumerConfig
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
	consumers []mq.Consumer
	inflight  chan struct{}
	workerID  string
}

// NewNotifyPushConsumer creates a consumer group runner.
func NewNotifyPushConsumer(store NotifyPushStore, pusher NotifyPushPusher, factory NotifyConsumerFactory, cfg NotifyPushConsumerConfig) *NotifyPushConsumer {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 1
	}
	if cfg.MaxInflight <= 0 {
		cfg.MaxInflight = cfg.WorkerCount
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = int64(mq.MaxRetries)
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &NotifyPushConsumer{
		store:    store,
		pusher:   pusher,
		factory:  factory,
		cfg:      cfg,
		ctx:      ctx,
		cancel:   cancel,
		inflight: make(chan struct{}, cfg.MaxInflight),
		workerID: fmt.Sprintf("notify-push-%d", time.Now().UnixNano()),
	}
}

// Start begins consuming in the background.
func (c *NotifyPushConsumer) Start() error {
	if c == nil || !c.cfg.Enabled {
		return nil
	}
	if c.factory == nil {
		return fmt.Errorf("notify push consumer factory is nil")
	}
	for i := 0; i < c.cfg.WorkerCount; i++ {
		consumer, err := c.factory()
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.consumers = append(c.consumers, consumer)
		c.mu.Unlock()
		c.wg.Add(1)
		go func(worker int, cons mq.Consumer) {
			defer c.wg.Done()
			if err := cons.Consume(c.ctx, c.ProcessMessage); err != nil && c.ctx.Err() == nil {
				log.Printf("[notify_push_consumer] consume stopped worker=%d err=%v", worker, err)
			}
		}(i, consumer)
	}
	return nil
}

// Stop closes consumers and waits for workers to exit.
func (c *NotifyPushConsumer) Stop() {
	if c == nil {
		return
	}
	c.cancel()
	c.mu.Lock()
	consumers := append([]mq.Consumer(nil), c.consumers...)
	c.mu.Unlock()
	for _, cons := range consumers {
		_ = cons.Close()
	}
	c.wg.Wait()
}

// ProcessMessage handles one MQ message. It is public for focused tests.
func (c *NotifyPushConsumer) ProcessMessage(ctx context.Context, msg *mq.Message) error {
	c.inflight <- struct{}{}
	defer func() { <-c.inflight }()

	if msg == nil {
		return nil
	}
	if msg != nil && msg.Timestamp > 0 {
		if lag := time.Now().UnixMilli() - msg.Timestamp; lag >= 0 {
			metrics.NotifyMQLag.Set(float64(lag))
		}
	}

	var env mq.BizEnvelope
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		return &mq.DeadLetterError{Err: err, Reason: "invalid_notify_envelope", Retries: 0}
	}
	normalizeBizEnvelope(&env)
	metrics.NotifyPushConsumeTotal.WithLabelValues(env.BusinessType, env.EventType).Inc()

	claimedOutbox, claimed, err := c.claimEnvelope(&env)
	if err != nil {
		return err
	}
	if !claimed {
		metrics.NotifyDedupeHitTotal.Inc()
		log.Printf("[notify_push_consumer] dedupe skip trace_id=%s notify_id=%s business_type=%s event_type=%s outbox_id=%s",
			env.TraceID, env.NotifyID, env.BusinessType, env.EventType, env.EventID)
		return nil
	}
	if claimedOutbox != nil {
		env.BusinessID = nonEmptyString(env.BusinessID, claimedOutbox.OrderID)
		env.CompensationStrategy = nonEmptyString(env.CompensationStrategy, claimedOutbox.CompensationStrategy)
	}

	if c.isExpired(&env) {
		now := time.Now()
		metrics.NotifyPushFailedTotal.WithLabelValues("expired").Inc()
		log.Printf("[notify_push_consumer] ttl expired trace_id=%s notify_id=%s business_type=%s event_type=%s outbox_id=%s",
			env.TraceID, env.NotifyID, env.BusinessType, env.EventType, env.EventID)
		return c.store.MarkOutboxExpired(env.EventID, env.NotifyID, "ttl_expired", now)
	}

	started := time.Now()
	results, err := c.pushEnvelope(ctx, &env)
	finished := time.Now()
	latencyMs := float64(finished.Sub(started).Microseconds()) / 1000.0
	result := firstDeliveryResult(results)
	if result.LatencyMs == 0 {
		result.LatencyMs = latencyMs
	}
	if result.TraceID == "" {
		result.TraceID = env.TraceID
	}
	if result.AttemptNo == 0 {
		result.AttemptNo = 1
	}
	if err == nil && deliveryResultFailed(result) {
		err = pushErrorFromDeliveryResult(result)
	}
	if err == nil {
		return c.handlePushSuccess(&env, result, started, finished)
	}
	return c.handlePushFailure(&env, err, result, started, finished)
}

func (c *NotifyPushConsumer) claimEnvelope(env *mq.BizEnvelope) (*model.NotificationOutbox, bool, error) {
	if c.store == nil || env == nil || env.EventID == "" {
		return nil, true, nil
	}
	outbox, err := c.store.GetOutbox(env.EventID)
	if err != nil {
		return nil, false, err
	}
	switch outbox.Status {
	case "sent", "dlq", "expired", "dropped", "cancelled":
		return outbox, false, nil
	}
	claimed, ok, err := c.store.ClaimPublishedOutbox(env.EventID, c.workerID, c.cfg.LockTTL())
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, fmt.Errorf("outbox %s is already being delivered", env.EventID)
	}
	return claimed, true, nil
}

func (c NotifyPushConsumerConfig) LockTTL() time.Duration {
	if c.Backoff > 0 {
		return c.Backoff * 30
	}
	return 30 * time.Second
}

func (c *NotifyPushConsumer) pushEnvelope(ctx context.Context, env *mq.BizEnvelope) ([]DeliveryResult, error) {
	if c.pusher == nil {
		return nil, &PushError{Type: "connection_refused", Class: "transient", Message: "push client is not configured", Retryable: true}
	}
	payload := []byte(env.PayloadJSON)
	if len(payload) == 0 {
		payload = env.Payload
	}
	if !json.Valid(payload) {
		return nil, &PushError{Type: "invalid_payload", Class: "permanent", Message: "payload_json is not valid json", Retryable: false}
	}
	switch env.TargetType {
	case "room":
		msgID, err := c.pusher.PushToRoomContext(ctx, protocol.OpRaw, "live", env.TargetID, payload)
		if err != nil {
			return nil, err
		}
		return []DeliveryResult{{MsgID: msgID, Path: "logic_push", TraceID: env.TraceID}}, nil
	case "broadcast":
		return nil, &PushError{Type: "unsupported_target", Class: "permanent", Message: "broadcast target is not supported by NotifyPushConsumer phase 1", Retryable: false}
	default:
		var data json.RawMessage = payload
		if mid, parseErr := strconv.ParseInt(env.UserID, 10, 64); parseErr == nil {
			return c.pusher.PushJSONToUsersDetailedContext(ctx, protocol.OpRaw, []int64{mid}, data)
		}
		return c.pusher.PushJSONToUserDetailedContext(ctx, protocol.OpRaw, extractKeysFromMids([]string{env.UserID}), data)
	}
}

func (c *NotifyPushConsumer) handlePushSuccess(env *mq.BizEnvelope, result DeliveryResult, started, finished time.Time) error {
	if result.Path == "" {
		result.Path = "logic_push"
	}
	attempt := &model.NotificationAttempt{
		AttemptID:    generateAttemptID(),
		NotifyID:     env.NotifyID,
		Channel:      result.Path,
		Target:       nonEmptyString(env.UserID, env.TargetID),
		Status:       attemptStatusForPath(result.Path),
		Path:         result.Path,
		TargetNode:   result.TargetNode,
		ErrorCode:    result.ErrorCode,
		ErrorMessage: result.ErrorMessage,
		LatencyMs:    result.LatencyMs,
		AttemptNo:    result.AttemptNo,
		TraceID:      nonEmptyString(result.TraceID, env.TraceID),
		StartedAt:    started,
		FinishedAt:   &finished,
	}
	if err := c.store.MarkOutboxSent(env.EventID, attempt, finished); err != nil {
		return err
	}
	metrics.NotifyPushSuccessTotal.Inc()
	metrics.NotifyPushLatencyMS.Observe(result.LatencyMs)
	log.Printf("[notify_push_consumer] delivered trace_id=%s notify_id=%s business_type=%s event_type=%s outbox_id=%s",
		env.TraceID, env.NotifyID, env.BusinessType, env.EventType, env.EventID)
	return nil
}

func (c *NotifyPushConsumer) handlePushFailure(env *mq.BizEnvelope, err error, result DeliveryResult, started, finished time.Time) error {
	class := "transient"
	if IsPermanentPushError(err) {
		class = "permanent"
	}
	metrics.NotifyPushFailedTotal.WithLabelValues(class).Inc()
	attempt := &model.NotificationAttempt{
		AttemptID:    generateAttemptID(),
		NotifyID:     env.NotifyID,
		Channel:      "logic_push",
		Target:       nonEmptyString(env.UserID, env.TargetID),
		Status:       "push_failed",
		Path:         "failed",
		TargetNode:   result.TargetNode,
		ErrorCode:    pushFailureReason(err),
		ErrorMessage: err.Error(),
		LatencyMs:    result.LatencyMs,
		AttemptNo:    result.AttemptNo,
		TraceID:      env.TraceID,
		StartedAt:    started,
		FinishedAt:   &finished,
	}
	if class != "permanent" {
		if insertErr := c.store.InsertAttempt(attempt); insertErr != nil {
			return insertErr
		}
		log.Printf("[notify_push_consumer] transient failure trace_id=%s notify_id=%s business_type=%s event_type=%s outbox_id=%s err=%v",
			env.TraceID, env.NotifyID, env.BusinessType, env.EventType, env.EventID, err)
		return err
	}
	outbox, getErr := c.store.GetOutbox(env.EventID)
	if getErr != nil {
		return getErr
	}
	metrics.NotifyPushDLQTotal.Inc()
	log.Printf("[notify_push_consumer] permanent failure trace_id=%s notify_id=%s business_type=%s event_type=%s outbox_id=%s err=%v",
		env.TraceID, env.NotifyID, env.BusinessType, env.EventType, env.EventID, err)
	return c.store.MoveOutboxToDLQ(outbox, &model.NotificationDLQ{
		DLQID:                generateDLQID(),
		NotifyID:             env.NotifyID,
		OutboxID:             env.EventID,
		UserID:               env.UserID,
		OrderID:              env.BusinessID,
		BusinessType:         env.BusinessType,
		Reason:               pushFailureReason(err),
		LastError:            err.Error(),
		PayloadJSON:          env.PayloadJSON,
		RetryCount:           outbox.RetryCount,
		CompensationStrategy: env.CompensationStrategy,
		TraceID:              env.TraceID,
		CreatedAt:            finished,
	}, attempt, "dlq")
}

func (c *NotifyPushConsumer) isExpired(env *mq.BizEnvelope) bool {
	if env == nil || env.TTLSeconds <= 0 {
		return false
	}
	createdMS := env.CreatedAtMS
	if createdMS <= 0 && env.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, env.CreatedAt); err == nil {
			createdMS = t.UnixMilli()
		}
	}
	if createdMS <= 0 {
		return false
	}
	return time.Now().UnixMilli() > createdMS+int64(env.TTLSeconds)*1000
}
