package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// IDGenerator generates unique message IDs.
type IDGenerator interface {
	GenerateString() (string, error)
}

// Ensure Dao satisfies the interfaces at compile time.
var _ dao.PushDAO = (*dao.Dao)(nil)

// CometPusher is the interface for pushing messages to Comet servers.
// In production, this would be a gRPC client pool to Comet servers.
type CometPusher interface {
	// PushMsg pushes a message to specific keys on a Comet server.
	PushMsg(ctx context.Context, server string, keys []string, op int32, body []byte) error
}

// PushService implements the dual-channel push architecture:
// - Online users: direct gRPC push to Comet (fast path)
// - Offline/failed: Kafka + offline queue (reliable path)
type PushService struct {
	dao        dao.PushDAO
	msgDAO     dao.MessageDAO
	sessMgr    *SessionManager
	ackService *AckService
	pusher     CometPusher
	idGen      IDGenerator
}

// NewPushService creates a new PushService.
func NewPushService(pd dao.PushDAO, md dao.MessageDAO, sessMgr *SessionManager, ackSvc *AckService, pusher CometPusher) *PushService {
	return &PushService{
		dao:        pd,
		msgDAO:     md,
		sessMgr:    sessMgr,
		ackService: ackSvc,
		pusher:     pusher,
	}
}

// SetIDGenerator sets the ID generator for auto-generating message IDs.
func (s *PushService) SetIDGenerator(gen IDGenerator) {
	s.idGen = gen
}

// DirectPush pushes a message directly to specific sessions via gRPC.
// Exported for use by SyncService and other external callers.
func (s *PushService) DirectPush(ctx context.Context, sessions []*Session, op int32, body []byte) error {
	return s.directPush(ctx, sessions, op, body)
}

// PushToUser pushes a message to a specific user using dual-channel architecture.
func (s *PushService) PushToUser(ctx context.Context, msgID string, toUID int64, op int32, body []byte, seq int64) error {
	// Auto-generate msgID if empty
	if msgID == "" && s.idGen != nil {
		if id, err := s.idGen.GenerateString(); err == nil {
			msgID = id
		}
	}

	// 1. Check if message was already delivered (idempotency)
	status, _ := s.ackService.GetMessageStatus(ctx, msgID)
	if status == MsgStatusAcked || status == MsgStatusDelivered {
		log.V(1).Infof("msg already delivered: msg_id=%s status=%s", msgID, status)
		return nil
	}

	start := time.Now()
	// 2. Track message for delivery tracking
	if err := s.ackService.TrackMessage(ctx, msgID, 0, toUID, op, body); err != nil {
		log.Warningf("track message failed: %v", err)
	}

	// 3. Check if user is online
	online, sessions := s.sessMgr.IsOnline(ctx, toUID)

	if online {
		// Fast path: direct push
		if err := s.directPush(ctx, sessions, op, body); err == nil {
			// Direct push succeeded
			if err := s.ackService.MarkDelivered(ctx, msgID); err != nil {
				log.Warningf("mark delivered failed: %v", err)
			}
			metrics.PushTotal.WithLabelValues("direct", "success").Inc()
			metrics.PushLatency.WithLabelValues("direct").Observe(time.Since(start).Seconds())
			return nil
		}
		// Direct push failed, fall through to reliable path
		metrics.PushTotal.WithLabelValues("direct", "failed").Inc()
		log.Warningf("direct push failed, falling back to kafka: uid=%d msg_id=%s", toUID, msgID)
	}

	// 4. Reliable path: offline queue + Kafka
	err := s.offlineAndEnqueue(ctx, msgID, toUID, op, body, seq)
	if err == nil {
		metrics.PushTotal.WithLabelValues("kafka", "success").Inc()
		metrics.PushLatency.WithLabelValues("kafka").Observe(time.Since(start).Seconds())
	} else {
		metrics.PushTotal.WithLabelValues("kafka", "failed").Inc()
	}
	return err
}

// directPush pushes a message directly to the user's Comet server(s) via gRPC.
func (s *PushService) directPush(ctx context.Context, sessions []*Session, op int32, body []byte) error {
	if s.pusher == nil {
		return fmt.Errorf("comet pusher not configured")
	}
	var lastErr error
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		// Create a timeout context for direct push
		pushCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := s.pusher.PushMsg(pushCtx, sess.Server, []string{sess.Key}, op, body)
		cancel()

		if err != nil {
			lastErr = err
			log.Warningf("direct push to server=%s key=%s failed: %v",
				sess.Server, sess.Key, err)
			continue
		}
		// At least one push succeeded
		return nil
	}
	return fmt.Errorf("all direct pushes failed: %w", lastErr)
}

// offlineAndEnqueue stores the message in the offline queue and sends to Kafka.
func (s *PushService) offlineAndEnqueue(ctx context.Context, msgID string, toUID int64, op int32, body []byte, seq int64) error {
	// 1. Add to offline queue for later sync
	if err := s.msgDAO.AddToOfflineQueue(ctx, toUID, msgID, float64(seq)); err != nil {
		log.Warningf("add to offline queue failed: uid=%d msg_id=%s err=%v", toUID, msgID, err)
	}

	// 2. Send to Kafka for async delivery
	// Look up the user's Comet server and connection keys from their session
	server := ""
	var keys []string
	if sessions, err := s.sessMgr.GetSessions(ctx, toUID); err == nil && len(sessions) > 0 {
		for _, sess := range sessions {
			if sess.Key != "" {
				keys = append(keys, sess.Key)
				if server == "" && sess.Server != "" {
					server = sess.Server
				}
			}
		}
	}
	// Fallback for offline users: the message goes to the offline queue
	// and will be picked up on next sync; Kafka delivery is a best-effort.
	if len(keys) == 0 {
		keys = []string{fmt.Sprintf("uid:%d", toUID)}
	}
	if err := s.dao.PushMsg(ctx, op, server, keys, body); err != nil {
		return fmt.Errorf("kafka push failed: %w", err)
	}

	log.Infof("message enqueued: msg_id=%s uid=%d seq=%d server=%s", msgID, toUID, seq, server)
	return nil
}

// PushToRoom pushes a message to a room via Kafka.
func (s *PushService) PushToRoom(ctx context.Context, op int32, roomKey string, body []byte) error {
	return s.dao.BroadcastRoomMsg(ctx, op, roomKey, body)
}

// PushAll broadcasts a message to all connected users via Kafka.
func (s *PushService) PushAll(ctx context.Context, op, speed int32, body []byte) error {
	return s.dao.BroadcastMsg(ctx, op, speed, body)
}
