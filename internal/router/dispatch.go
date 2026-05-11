package router

import (
	"context"
	"fmt"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/service"
	"github.com/Terry-Mao/goim/internal/mq"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// RouteByUser pushes a message to a specific user via dual-channel dispatch.
func (e *DispatchEngine) RouteByUser(ctx context.Context, msgID string, toUID int64, op int32, body []byte, seq int64) error {
	if msgID == "" && e.idGen != nil {
		if id, err := e.idGen.GenerateString(); err == nil {
			msgID = id
		}
	}

	// Idempotency check
	if status, _ := e.ackHandler.GetMessageStatus(ctx, msgID); status == service.MsgStatusAcked || status == service.MsgStatusDelivered {
		log.V(1).Infof("msg already delivered: msg_id=%s status=%s", msgID, status)
		return nil
	}

	start := time.Now()
	// Track message for delivery tracking
	if err := e.ackHandler.TrackMessage(ctx, msgID, 0, toUID, op, body); err != nil {
		log.Warningf("track message failed: %v", err)
	}

	// Check if user is online
	online, sessions := e.sessMgr.IsOnline(ctx, toUID)

	if online {
		if err := directPush(ctx, e.pusher, sessions, op, body); err == nil {
			if err := e.ackHandler.MarkDelivered(ctx, msgID); err != nil {
				log.Warningf("mark delivered failed: %v", err)
			}
			metrics.PushTotal.WithLabelValues("direct", "success").Inc()
			metrics.PushLatency.WithLabelValues("direct").Observe(time.Since(start).Seconds())
			return nil
		}
		metrics.PushTotal.WithLabelValues("direct", "failed").Inc()
		log.Warningf("direct push failed, falling back to reliable path: uid=%d msg_id=%s", toUID, msgID)
	}

	// Reliable path: offline queue + MQ/Kafka
	err := e.reliableEnqueue(ctx, msgID, toUID, op, body, seq, sessions)
	if err == nil {
		metrics.PushTotal.WithLabelValues("kafka", "success").Inc()
		metrics.PushLatency.WithLabelValues("kafka").Observe(time.Since(start).Seconds())
	} else {
		metrics.PushTotal.WithLabelValues("kafka", "failed").Inc()
	}
	return err
}

// RouteByRoom pushes a message to a room.
func (e *DispatchEngine) RouteByRoom(ctx context.Context, op int32, roomKey string, body []byte) error {
	if e.producer != nil {
		pushMsg := pushMsgBytes(pbPush, op, "", nil, roomKey, body, 0)
		return e.producer.EnqueueToRoom(ctx, roomKey, &mq.Message{Key: roomKey, Value: pushMsg})
	}
	return e.dao.BroadcastRoomMsg(ctx, op, roomKey, body)
}

// RouteBroadcast broadcasts a message to all connected users.
func (e *DispatchEngine) RouteBroadcast(ctx context.Context, op, speed int32, body []byte) error {
	if e.producer != nil {
		pushMsg := pushMsgBytes(pbBroadcast, op, "", nil, "", body, speed)
		return e.producer.EnqueueBroadcast(ctx, &mq.Message{Value: pushMsg}, speed)
	}
	return e.dao.BroadcastMsg(ctx, op, speed, body)
}

// directPush pushes a message directly to the user's sessions via gRPC.
// Returns nil if at least one push succeeds.
func directPush(ctx context.Context, pusher CometPusher, sessions []*service.Session, op int32, body []byte) error {
	if pusher == nil {
		return fmt.Errorf("comet pusher not configured")
	}
	var lastErr error
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		pushCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := pusher.PushMsg(pushCtx, sess.Server, []string{sess.Key}, op, body)
		cancel()

		if err != nil {
			lastErr = err
			log.Warningf("direct push to server=%s key=%s failed: %v", sess.Server, sess.Key, err)
			continue
		}
		return nil
	}
	return fmt.Errorf("all direct pushes failed: %w", lastErr)
}

// reliableEnqueue stores the message in the offline queue and sends to MQ/Kafka.
func (e *DispatchEngine) reliableEnqueue(ctx context.Context, msgID string, toUID int64, op int32, body []byte, seq int64, sessions []*service.Session) error {
	if err := e.msgDAO.AddToOfflineQueue(ctx, toUID, msgID, float64(seq)); err != nil {
		log.Warningf("add to offline queue failed: uid=%d msg_id=%s err=%v", toUID, msgID, err)
	}

	// Extract actual connection keys from sessions
	server := ""
	var keys []string
	for _, sess := range sessions {
		if sess != nil && sess.Key != "" {
			keys = append(keys, sess.Key)
			if server == "" && sess.Server != "" {
				server = sess.Server
			}
		}
	}
	// Fallback for offline users: store in offline queue for sync retrieval
	if len(keys) == 0 {
		keys = []string{fmt.Sprintf("uid:%d", toUID)}
	}
	if e.producer != nil {
		pushMsg := pushMsgBytes(pbPush, op, server, keys, "", body, 0)
		return e.producer.EnqueueToUser(ctx, toUID, &mq.Message{Key: keys[0], Value: pushMsg})
	}
	return e.dao.PushMsg(ctx, op, server, keys, body)
}

// pb message type constants matching api/logic PushMsg_Type.
const (
	pbPush      = 0
	pbRoom      = 1
	pbBroadcast = 2
)
