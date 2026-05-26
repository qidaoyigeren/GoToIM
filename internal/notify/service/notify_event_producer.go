package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/Terry-Mao/goim/internal/notify/model"
)

// NotifyEventProducer publishes standardized Notify business envelopes to MQ.
type NotifyEventProducer struct {
	producer mq.Producer
	topic    string
}

// NewNotifyEventProducer creates a producer for the notify push topic.
func NewNotifyEventProducer(producer mq.Producer, topic string) *NotifyEventProducer {
	return &NotifyEventProducer{producer: producer, topic: topic}
}

// Publish serializes the envelope as stable JSON and publishes it with business headers.
func (p *NotifyEventProducer) Publish(ctx context.Context, env *mq.BizEnvelope) error {
	if p == nil || p.producer == nil {
		return errors.New("notify event producer is not configured")
	}
	if env == nil {
		return errors.New("notify envelope is nil")
	}
	topic := p.topic
	if topic == "" {
		return errors.New("notify topic is empty")
	}
	normalizeBizEnvelope(env)
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	uid, _ := strconv.ParseInt(env.UserID, 10, 64)
	return p.producer.EnqueueToTopic(ctx, topic, uid, &mq.Message{
		Topic:   topic,
		Key:     notifyEnvelopeKey(env),
		Value:   body,
		Headers: notifyEnvelopeHeaders(env),
	})
}

func buildNotifyEnvelope(outbox *model.NotificationOutbox, notif *model.Notification, maxRetries int64) *mq.BizEnvelope {
	if outbox == nil {
		return nil
	}
	createdAt := outbox.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	targetType, targetID := targetFromOutbox(outbox)
	ackPolicy := ""
	if notif != nil {
		ackPolicy = notif.AckPolicy
	}
	payload := []byte(outbox.PayloadJSON)
	env := &mq.BizEnvelope{
		EventID:              outbox.OutboxID,
		NotifyID:             outbox.NotifyID,
		BusinessID:           outbox.OrderID,
		BusinessType:         outbox.BusinessType,
		EventType:            outbox.EventType,
		UserID:               outbox.UserID,
		TargetType:           targetType,
		TargetID:             targetID,
		Priority:             outbox.Priority,
		TTLSeconds:           int32(outbox.TTLSeconds),
		DedupeKey:            nonEmptyString(outbox.NotifyID, outbox.OutboxID),
		TraceID:              nonEmptyString(outbox.TraceID, outbox.NotifyID),
		CreatedAt:            createdAt.UTC().Format(time.RFC3339Nano),
		PayloadJSON:          outbox.PayloadJSON,
		AckPolicy:            ackPolicy,
		RetryPolicy:          &mq.BizRetryPolicy{MaxRetries: maxRetries, Backoff: "exponential"},
		CompensationStrategy: nonEmptyString(outbox.CompensationStrategy, "retry_then_dlq"),
		MsgID:                outbox.NotifyID,
		BizID:                outbox.OrderID,
		DeliveryMode:         mq.DeliveryReliable,
		Payload:              payload,
		CreatedAtMS:          createdAt.UnixMilli(),
	}
	normalizeBizEnvelope(env)
	return env
}

func normalizeBizEnvelope(env *mq.BizEnvelope) {
	if env == nil {
		return
	}
	if env.BusinessID == "" {
		env.BusinessID = env.BizID
	}
	if env.BizID == "" {
		env.BizID = env.BusinessID
	}
	if env.MsgID == "" {
		env.MsgID = env.NotifyID
	}
	if env.DedupeKey == "" {
		env.DedupeKey = nonEmptyString(env.NotifyID, env.EventID)
	}
	if env.TargetType == "" || env.TargetID == "" {
		targetType, targetID := targetFromUserID(env.UserID)
		if env.TargetType == "" {
			env.TargetType = targetType
		}
		if env.TargetID == "" {
			env.TargetID = targetID
		}
	}
	if env.PayloadJSON == "" && len(env.Payload) > 0 {
		env.PayloadJSON = string(env.Payload)
	}
	if len(env.Payload) == 0 && env.PayloadJSON != "" {
		env.Payload = []byte(env.PayloadJSON)
	}
	if env.CreatedAtMS == 0 {
		if env.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, env.CreatedAt); err == nil {
				env.CreatedAtMS = t.UnixMilli()
			}
		}
		if env.CreatedAtMS == 0 {
			env.CreatedAtMS = time.Now().UnixMilli()
		}
	}
	if env.CreatedAt == "" {
		env.CreatedAt = time.UnixMilli(env.CreatedAtMS).UTC().Format(time.RFC3339Nano)
	}
}

func notifyEnvelopeKey(env *mq.BizEnvelope) string {
	if env == nil {
		return ""
	}
	if env.TargetType == "user" && env.UserID != "" {
		return env.UserID
	}
	if env.TargetID != "" {
		return env.TargetID
	}
	return nonEmptyString(env.NotifyID, env.EventID)
}

func notifyEnvelopeHeaders(env *mq.BizEnvelope) map[string]string {
	headers := map[string]string{
		mq.HeaderPriority:        env.Priority,
		mq.HeaderTTLSeconds:      strconv.FormatInt(int64(env.TTLSeconds), 10),
		mq.HeaderCreatedAtUnixMS: strconv.FormatInt(env.CreatedAtMS, 10),
		mq.HeaderTraceID:         env.TraceID,
		mq.HeaderBusinessType:    env.BusinessType,
		mq.HeaderEventType:       env.EventType,
		mq.HeaderDedupeKey:       env.DedupeKey,
		mq.HeaderBizID:           env.BusinessID,
		mq.HeaderDeliveryMode:    strconv.Itoa(int(env.DeliveryMode)),
	}
	if env.TTLSeconds > 0 && env.CreatedAtMS > 0 {
		headers[mq.HeaderExpireAtUnixMS] = strconv.FormatInt(env.CreatedAtMS+int64(env.TTLSeconds)*1000, 10)
	}
	for k, v := range headers {
		if v == "" {
			delete(headers, k)
		}
	}
	return headers
}

func targetFromOutbox(outbox *model.NotificationOutbox) (string, string) {
	if outbox == nil {
		return "", ""
	}
	return targetFromUserID(outbox.UserID)
}

func targetFromUserID(userID string) (string, string) {
	if strings.HasPrefix(userID, "room:") {
		return "room", strings.TrimPrefix(userID, "room:")
	}
	if userID == "broadcast" || userID == "all" {
		return "broadcast", userID
	}
	return "user", userID
}
