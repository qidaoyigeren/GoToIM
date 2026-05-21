package mq

import "context"

// Producer sends messages to the message queue.
// Dispatch types map to different topics/streams internally.
type Producer interface {
	// EnqueueToUser enqueues a per-user point-to-point message.
	// Implementation routes to the user push topic with the uid as partition key.
	EnqueueToUser(ctx context.Context, uid int64, msg *Message) error

	// EnqueueToTopic enqueues a per-user message to an explicit topic.
	// The uid is still used as the partition key when msg.Key is empty.
	EnqueueToTopic(ctx context.Context, topic string, uid int64, msg *Message) error

	// EnqueueToUsers enqueues a message targeted to multiple specific users.
	EnqueueToUsers(ctx context.Context, uids []int64, msg *Message) error

	// EnqueueToRoom enqueues a room-level broadcast.
	EnqueueToRoom(ctx context.Context, roomID string, msg *Message) error

	// EnqueueBroadcast enqueues a global broadcast.
	EnqueueBroadcast(ctx context.Context, msg *Message, speed int32) error

	// EnqueueACK publishes a delivery acknowledgment for async consumers.
	EnqueueACK(ctx context.Context, msgID string, uid int64, status, targetNode string) error

	// EnqueueDelayed enqueues a message with a delivery delay.
	// The message will not be visible to consumers until after delayMs milliseconds.
	EnqueueDelayed(ctx context.Context, uid int64, msg *Message, delayMs int64) error

	// Close releases producer resources.
	Close() error
}
