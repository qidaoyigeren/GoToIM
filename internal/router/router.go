package router

import (
	"context"
	"sync/atomic"

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

// DirectBroadcaster broadcasts messages directly to all Comet servers via gRPC.
// Used as fallback when Kafka is unavailable for room/broadcast messages.
type DirectBroadcaster interface {
	BroadcastRoom(ctx context.Context, op int32, roomKey string, body []byte) error
	BroadcastAll(ctx context.Context, op, speed int32, body []byte) error
}

// DispatchEngine routes messages to the correct delivery channel.
// It encapsulates the dual-channel push architecture:
//   - Online users: direct gRPC to Comet (fast path)
//   - Offline/failed: MQ reliable path
type DispatchEngine struct {
	producer    mq.Producer
	broadcaster DirectBroadcaster // optional: direct broadcast fallback when Kafka is down
	dao         dao.PushDAO
	msgDAO      dao.MessageDAO
	sessMgr     *service.SessionManager
	ackHandler  *ACKHandler
	pusher      CometPusher
	idGen       IDGenerator
	limiter     *RateLimiter         // optional: per-user + global rate limiter
	limiterV2   *MultiDimRateLimiter // optional: multi-dimensional rate limiter (Phase 2)
	attemptRec  *AttemptRecorder     // optional: delivery attempt logger (Phase 1)
	stateRec    *StateRecorder       // optional: delivery state machine (Phase 2)
	directTotal atomic.Int64
	kafkaTotal  atomic.Int64
}

// DeliveryStats is a snapshot of delivery path counters.
type DeliveryStats struct {
	Direct int64
	Kafka  int64
}

// DeliveryResult explains the concrete path selected for one push request.
type DeliveryResult struct {
	MsgID        string  `json:"msg_id"`
	Path         string  `json:"path"`
	TargetNode   string  `json:"target_node,omitempty"`
	ErrorCode    string  `json:"error_code,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	LatencyMs    float64 `json:"latency_ms"`
	AttemptNo    int64   `json:"attempt_no"`
	TraceID      string  `json:"trace_id,omitempty"`
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

// SetBroadcastFallback sets the direct broadcast fallback for when Kafka is unavailable.
func (e *DispatchEngine) SetBroadcastFallback(b DirectBroadcaster) {
	e.broadcaster = b
}

// SetRateLimiter sets the rate limiter for the dispatch engine.
func (e *DispatchEngine) SetRateLimiter(l *RateLimiter) {
	e.limiter = l
}

// SetAttemptRecorder sets the delivery attempt recorder for the dispatch engine.
func (e *DispatchEngine) SetAttemptRecorder(r *AttemptRecorder) {
	e.attemptRec = r
}

// SetStateRecorder sets the delivery state machine recorder on both engine and ACK handler.
func (e *DispatchEngine) SetStateRecorder(r *StateRecorder) {
	e.stateRec = r
	if e.ackHandler != nil {
		e.ackHandler.stateRec = r
	}
}

// SetMultiDimRateLimiter sets the multi-dimensional rate limiter (Phase 2).
func (e *DispatchEngine) SetMultiDimRateLimiter(l *MultiDimRateLimiter) {
	e.limiterV2 = l
}

// Stats returns delivery path counters accumulated by the dispatch engine.
func (e *DispatchEngine) Stats() DeliveryStats {
	return DeliveryStats{
		Direct: e.directTotal.Load(),
		Kafka:  e.kafkaTotal.Load(),
	}
}

// HandleACK processes a client ACK for a delivered message.
func (e *DispatchEngine) HandleACK(ctx context.Context, uid int64, msgID string) error {
	return e.ackHandler.HandleACK(ctx, uid, msgID)
}

// HandleACKWithDevice processes an ACK with device-level tracking.
// deviceID and sessionID are optional (empty string for legacy clients).
func (e *DispatchEngine) HandleACKWithDevice(ctx context.Context, uid int64, msgID, deviceID, sessionID string) error {
	return e.ackHandler.HandleACKWithDevice(ctx, uid, msgID, deviceID, sessionID)
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
// Returns the list of sessions that failed (nil if all succeeded).
// Used by SyncService for offline message sync.
func (e *DispatchEngine) DirectPush(ctx context.Context, sessions []*service.Session, op int32, body []byte) ([]*service.Session, error) {
	return directPush(ctx, e.pusher, sessions, op, body)
}
