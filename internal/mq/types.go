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

// DeliveryStatus reports delivery outcome after dispatch to gateway.
type DeliveryStatus int

const (
	StatusPending DeliveryStatus = iota
	StatusDispatched
	StatusDelivered
	StatusFailed
)
