package mq

import "context"

// Header keys for message metadata (Phase 2 — priority / TTL / trace / business context).
const (
	// HeaderDelayedUntil stores the Unix timestamp (ms) after which the message becomes deliverable.
	HeaderDelayedUntil = "goim_delayed_until"
	// HeaderPriority stores the message priority: critical / high / normal / low.
	HeaderPriority = "goim_priority"
	// HeaderTTLSeconds stores the message time-to-live in seconds.
	HeaderTTLSeconds = "goim_ttl_seconds"
	// HeaderCreatedAtUnixMS stores the message creation time as Unix milliseconds.
	HeaderCreatedAtUnixMS = "goim_created_at_unix_ms"
	// HeaderTraceID stores the distributed trace identifier.
	HeaderTraceID = "goim_trace_id"
	// HeaderBusinessType stores the business category (order / marketing / system).
	HeaderBusinessType = "goim_business_type"
	// HeaderEventType stores the business event (created / paid / shipped).
	HeaderEventType = "goim_event_type"
	// HeaderDedupeKey stores the idempotency deduplication key.
	HeaderDedupeKey = "goim_dedupe_key"
	// HeaderBizID stores the business object ID (e.g. order_id).
	HeaderBizID = "goim_biz_id"
	// HeaderExpireAtUnixMS stores the absolute expiry time as Unix milliseconds.
	HeaderExpireAtUnixMS = "goim_expire_at_unix_ms"
	// HeaderDeliveryMode stores the delivery mode: reliable / transient / persist-only.
	HeaderDeliveryMode = "goim_delivery_mode"
)

// MsgDeliveryMode describes how a message should move through storage and push.
type MsgDeliveryMode int

const (
	DeliveryReliable MsgDeliveryMode = iota
	DeliveryTransient
	DeliveryPersistOnly
)

// Message represents a single message envelope in the message queue.
type Message struct {
	Topic     string
	Key       string
	Value     []byte
	Headers   map[string]string
	Partition int32
	Offset    int64
	Timestamp int64
}

// MessageHandler is the callback invoked for each consumed message.
// Return nil to commit offset; return error to trigger redelivery.
type MessageHandler func(ctx context.Context, msg *Message) error

// DLQProducer sends undeliverable messages to a dead-letter queue for
// manual inspection and possible replay.
type DLQProducer interface {
	Send(ctx context.Context, msg *Message, reason string) error
}

// DeliveryStatus reports delivery outcome after dispatch to gateway.
type DeliveryStatus int

const (
	StatusPending DeliveryStatus = iota
	StatusDispatched
	StatusDelivered
	StatusFailed
)

// MaxRetries is the maximum number of times a message will be retried
// before being routed to the dead-letter queue.
const MaxRetries = 3

// AckEvent is published to Kafka when a client ACKs a message.
// Notify Server consumes these events to synchronize its ACK state.
type AckEvent struct {
	MsgID        string   `json:"msg_id"`
	MsgIDs       []string `json:"msg_ids,omitempty"`
	UserID       string   `json:"user_id"`
	UID          int64    `json:"uid,omitempty"`
	DeviceID     string   `json:"device_id"`
	SessionID    string   `json:"session_id"`
	AckTime      int64    `json:"ack_time"`
	FirstAckTime int64    `json:"first_ack_time,omitempty"`
	Status       string   `json:"status,omitempty"`
	TargetNode   string   `json:"target_node,omitempty"`
	LatencyMs    float64  `json:"latency_ms,omitempty"`
	TraceID      string   `json:"trace_id,omitempty"`
	Count        int      `json:"count,omitempty"`
	Batched      bool     `json:"batched,omitempty"`
	// NotifyID maps to the business notification when available.
	// When empty, Notify Server maps via msg_id lookup.
	NotifyID string `json:"notify_id,omitempty"`
}

// BizEnvelope is the standard business message envelope used between
// Notify Server and the IM core layer (Router / Kafka / Worker).
//
// Phase 2 introduces this envelope to carry priority, TTL, trace, and
// business context through the entire delivery pipeline without modifying
// the protobuf wire format. The envelope is JSON-serialized and placed
// inside pb.PushMsg.Msg (the body field).
//
// Backward compatibility: old clients that don't produce BizEnvelope
// still work — the Router falls back to raw body interpretation.
type BizEnvelope struct {
	MsgID        string          `json:"msg_id,omitempty"`
	BizID        string          `json:"biz_id,omitempty"`        // e.g. order_id, campaign_id
	BusinessType string          `json:"business_type,omitempty"` // order / marketing / system
	EventType    string          `json:"event_type,omitempty"`    // created / paid / shipped
	UserID       string          `json:"user_id,omitempty"`
	Priority     string          `json:"priority,omitempty"`    // critical / high / normal / low
	TTLSeconds   int32           `json:"ttl_seconds,omitempty"` // 0 = no expiry
	DedupeKey    string          `json:"dedupe_key,omitempty"`
	TraceID      string          `json:"trace_id,omitempty"`
	DeliveryMode MsgDeliveryMode `json:"delivery_mode,omitempty"`
	Payload      []byte          `json:"payload,omitempty"` // original business payload
	CreatedAtMS  int64           `json:"created_at_unix_ms,omitempty"`
}

// DLQMessage is written to the DLQ topic when a message expires or exhausts retries.
type DLQMessage struct {
	MsgID         string `json:"msg_id"`
	UserID        string `json:"user_id"`
	OriginalTopic string `json:"original_topic"`
	Reason        string `json:"reason"`
	Payload       []byte `json:"payload"`
	TraceID       string `json:"trace_id"`
	CreatedAtMS   int64  `json:"created_at_unix_ms"`
	ExpiredAtMS   int64  `json:"expired_at_unix_ms"`
}

// RetryCounter atomically tracks retry attempts for a message identified by
// its Kafka coordinates (topic, partition, offset).
type RetryCounter interface {
	// Incr increments and returns the retry count for the given message.
	Incr(ctx context.Context, topic string, partition int32, offset int64) (int64, error)
}

// DeadLetterError wraps an error and signals that the message should be
// routed to the dead-letter queue rather than retried.
type DeadLetterError struct {
	Err     error
	Reason  string
	Retries int64
}

func (e *DeadLetterError) Error() string {
	return "dead-letter: " + e.Reason + ": " + e.Err.Error()
}

func (e *DeadLetterError) DeadLetter() bool { return true }

func (e *DeadLetterError) Unwrap() error { return e.Err }
