package mq

import "context"

// Header keys for message metadata.
const (
	// HeaderDelayedUntil stores the Unix timestamp (ms) after which the message becomes deliverable.
	HeaderDelayedUntil = "goim_delayed_until"
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
