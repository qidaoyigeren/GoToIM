package router

import (
	"context"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	"github.com/Terry-Mao/goim/internal/logic/service"
	"github.com/Terry-Mao/goim/internal/mq"
)

// IDGenerator generates unique message IDs.
type IDGenerator interface {
	GenerateString() (string, error)
}

// CometPusher pushes messages directly to Comet servers via gRPC.
type CometPusher interface {
	PushMsg(ctx context.Context, server string, keys []string, op int32, body []byte) error
}

// DispatchEngine routes messages to the correct delivery channel.
// It encapsulates the dual-channel push architecture:
//   - Online users: direct gRPC to Comet (fast path)
//   - Offline/failed: MQ reliable path
type DispatchEngine struct {
	producer   mq.Producer
	dao        dao.PushDAO
	msgDAO     dao.MessageDAO
	sessMgr    *service.SessionManager
	ackHandler *ACKHandler
	pusher     CometPusher
	idGen      IDGenerator
}

// NewDispatchEngine creates a new DispatchEngine.
func NewDispatchEngine(pd dao.PushDAO, md dao.MessageDAO, sm *service.SessionManager, p CometPusher) *DispatchEngine {
	e := &DispatchEngine{
		dao:     pd,
		msgDAO:  md,
		sessMgr: sm,
		pusher:  p,
	}
	e.ackHandler = NewACKHandler(md, pd)
	return e
}

// SetIDGenerator sets the ID generator for auto-generating message IDs.
func (e *DispatchEngine) SetIDGenerator(gen IDGenerator) {
	e.idGen = gen
}

// SetMQProducer sets the MQ producer for the reliable delivery path.
func (e *DispatchEngine) SetMQProducer(p mq.Producer) {
	e.producer = p
}

// HandleACK processes a client ACK for a delivered message.
func (e *DispatchEngine) HandleACK(ctx context.Context, uid int64, msgID string) error {
	return e.ackHandler.HandleACK(ctx, uid, msgID)
}

// TrackMessage stores message metadata for delivery tracking (idempotent).
func (e *DispatchEngine) TrackMessage(ctx context.Context, msgID string, fromUID, toUID int64, op int32, body []byte) error {
	return e.ackHandler.TrackMessage(ctx, msgID, fromUID, toUID, op, body)
}

// MarkDelivered marks a message as delivered.
func (e *DispatchEngine) MarkDelivered(ctx context.Context, msgID string) error {
	return e.ackHandler.MarkDelivered(ctx, msgID)
}

// GetMessageStatus returns the current status of a message.
func (e *DispatchEngine) GetMessageStatus(ctx context.Context, msgID string) (string, error) {
	return e.ackHandler.GetMessageStatus(ctx, msgID)
}

// DirectPush pushes a message directly to specific sessions via gRPC.
// Used by SyncService for offline message sync.
func (e *DispatchEngine) DirectPush(ctx context.Context, sessions []*service.Session, op int32, body []byte) error {
	return directPush(ctx, e.pusher, sessions, op, body)
}
