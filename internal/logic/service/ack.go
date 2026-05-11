package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// Ensure Dao satisfies the interface at compile time.
var _ dao.MessageDAO = (*dao.Dao)(nil)

// Message status constants
const (
	MsgStatusPending   = "pending"
	MsgStatusDelivered = "delivered"
	MsgStatusAcked     = "acked"
	MsgStatusFailed    = "failed"
)

// AckService handles message acknowledgment and delivery tracking.
type AckService struct {
	dao     dao.MessageDAO
	pushDAO dao.PushDAO
}

// NewAckService creates a new AckService.
func NewAckService(d dao.MessageDAO, pd dao.PushDAO) *AckService {
	return &AckService{dao: d, pushDAO: pd}
}

// HandleAck processes an ACK from a client.
func (s *AckService) HandleAck(ctx context.Context, uid int64, msgID string) error {
	// Update message status to acked
	if err := s.dao.UpdateMessageStatus(ctx, msgID, MsgStatusAcked); err != nil {
		return fmt.Errorf("update msg status: %w", err)
	}

	// Remove from offline queue
	if err := s.dao.RemoveFromOfflineQueue(ctx, uid, msgID); err != nil {
		log.Warningf("remove from offline queue: uid=%d msg_id=%s err=%v", uid, msgID, err)
	}

	// Publish ACK event to Kafka for async consumers
	if s.pushDAO != nil {
		if err := s.pushDAO.PublishACK(ctx, msgID, uid, MsgStatusAcked); err != nil {
			log.Warningf("publish ack event: uid=%d msg_id=%s err=%v", uid, msgID, err)
		}
	}

	metrics.MsgAckTotal.Inc()
	log.Infof("ack processed: uid=%d msg_id=%s", uid, msgID)
	return nil
}

// BatchHandleAck processes multiple ACKs in a single pipeline.
func (s *AckService) BatchHandleAck(ctx context.Context, acks []struct {
	UID   int64
	MsgID string
}) error {
	for _, ack := range acks {
		if err := s.HandleAck(ctx, ack.UID, ack.MsgID); err != nil {
			log.Warningf("batch ack failed: uid=%d msg_id=%s err=%v", ack.UID, ack.MsgID, err)
		}
	}
	return nil
}

// TrackMessage stores message metadata for delivery tracking.
// Skips if the message is already tracked (idempotent).
func (s *AckService) TrackMessage(ctx context.Context, msgID string, fromUID, toUID int64, op int32, body []byte) error {
	// Check if already tracked
	existing, _ := s.dao.GetMessageStatus(ctx, msgID)
	if len(existing) > 0 {
		return nil
	}
	fields := map[string]interface{}{
		"status":     MsgStatusPending,
		"from_uid":   fromUID,
		"to_uid":     toUID,
		"op":         op,
		"body":       base64.StdEncoding.EncodeToString(body),
		"retry_cnt":  0,
		"created_at": time.Now().UnixMilli(),
		"updated_at": time.Now().UnixMilli(),
	}
	if err := s.dao.SetMessageStatus(ctx, msgID, fields); err != nil {
		return fmt.Errorf("set msg status: %w", err)
	}
	return nil
}

// MarkDelivered marks a message as delivered.
func (s *AckService) MarkDelivered(ctx context.Context, msgID string) error {
	return s.dao.UpdateMessageStatus(ctx, msgID, MsgStatusDelivered)
}

// MarkFailed marks a message as failed.
func (s *AckService) MarkFailed(ctx context.Context, msgID string) error {
	return s.dao.UpdateMessageStatus(ctx, msgID, MsgStatusFailed)
}

// GetMessageStatus returns the current status of a message.
func (s *AckService) GetMessageStatus(ctx context.Context, msgID string) (string, error) {
	data, err := s.dao.GetMessageStatus(ctx, msgID)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}
	return data["status"], nil
}
