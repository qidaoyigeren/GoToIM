package mq

import "context"

// Consumer subscribes to message topics and invokes a handler per message.
// Implementations use consumer groups for horizontal scaling and
// manual offset commit for at-least-once delivery.
type Consumer interface {
	// Consume starts consuming messages. Blocks until ctx is cancelled.
	// The handler is invoked per message; returning an error skips offset
	// commit so the message will be redelivered.
	Consume(ctx context.Context, handler MessageHandler) error

	// Close stops consumption and releases consumer group membership.
	Close() error
}
