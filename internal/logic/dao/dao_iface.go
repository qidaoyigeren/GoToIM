package dao

import "context"

// SessionDAO is the interface for session-related Redis operations.
type SessionDAO interface {
	AddSession(ctx context.Context, sid string, uid int64, key, deviceID, platform, server string) error
	GetSession(ctx context.Context, sid string) (map[string]string, error)
	GetSessionByKey(ctx context.Context, key string) (string, error)
	GetUserSessions(ctx context.Context, uid int64) (map[string]string, error)
	GetDeviceSession(ctx context.Context, uid int64, deviceID string) (string, error)
	DelSession(ctx context.Context, sid string, uid int64, deviceID, key string) error
	ExpireSession(ctx context.Context, sid string, uid int64) error
}

// MessageDAO is the interface for message status and offline queue operations.
type MessageDAO interface {
	SetMessageStatus(ctx context.Context, msgID string, fields map[string]interface{}) error
	SetMessageStatusNX(ctx context.Context, msgID, field string, value interface{}) (bool, error)
	GetMessageStatus(ctx context.Context, msgID string) (map[string]string, error)
	UpdateMessageStatus(ctx context.Context, msgID, status string) error
	IncrUserSeqBy(ctx context.Context, uid int64, delta int64) (int64, error)
	GetUserMaxSeq(ctx context.Context, uid int64) (int64, error)
	AddUserMessage(ctx context.Context, uid int64, msgID string, seq int64) error
	GetUserMessagesAfterSeq(ctx context.Context, uid int64, lastSeq int64, limit int) ([]string, error)
	AddToOfflineQueue(ctx context.Context, uid int64, msgID string, seq float64) error
	RemoveFromOfflineQueue(ctx context.Context, uid int64, msgID string) error
	// RecordDeviceACK stores a device-level ACK record for a message.
	RecordDeviceACK(ctx context.Context, msgID, deviceID, sessionID string, ackTime int64) error
	// Phase 2: device-level cursor operations
	GetDeviceCursor(ctx context.Context, uid int64, deviceID string) (int64, error)
	GetOfflineMessagesByDeviceCursor(ctx context.Context, uid int64, deviceID string, limit int) ([]string, error)
	AdvanceDeviceCursor(ctx context.Context, uid int64, deviceID string, seq int64) error
}

// PushDAO is the interface for Kafka push operations.
type PushDAO interface {
	PushMsg(ctx context.Context, op int32, server string, keys []string, msg []byte) error
	BroadcastRoomMsg(ctx context.Context, op int32, room string, msg []byte) error
	BroadcastMsg(ctx context.Context, op, speed int32, msg []byte) error
	PublishACK(ctx context.Context, msgID string, uid int64, status, targetNode, deviceID, sessionID string) error
}
