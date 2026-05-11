package router

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// Message status constants (mirrored from service/ack.go for independence).
const (
	MsgStatusPending   = "pending"
	MsgStatusDelivered = "delivered"
	MsgStatusAcked     = "acked"
	MsgStatusFailed    = "failed"
)

// ACKHandler processes delivery confirmations from clients.
type ACKHandler struct {
	dao     dao.MessageDAO
	pushDAO dao.PushDAO
}

// NewACKHandler creates a new ACKHandler.
func NewACKHandler(d dao.MessageDAO, pd dao.PushDAO) *ACKHandler {
	return &ACKHandler{dao: d, pushDAO: pd}
}

// HandleACK processes a client ACK.
func (a *ACKHandler) HandleACK(ctx context.Context, uid int64, msgID string) error {
	if err := a.dao.UpdateMessageStatus(ctx, msgID, MsgStatusAcked); err != nil {
		return fmt.Errorf("update msg status: %w", err)
	}

	if err := a.dao.RemoveFromOfflineQueue(ctx, uid, msgID); err != nil {
		log.Warningf("remove from offline queue: uid=%d msg_id=%s err=%v", uid, msgID, err)
	}

	if a.pushDAO != nil {
		if err := a.pushDAO.PublishACK(ctx, msgID, uid, MsgStatusAcked); err != nil {
			log.Warningf("publish ack event: uid=%d msg_id=%s err=%v", uid, msgID, err)
		}
	}

	metrics.MsgAckTotal.Inc()
	log.Infof("ack processed: uid=%d msg_id=%s", uid, msgID)
	return nil
}

// TrackMessage stores message metadata for delivery tracking (idempotent).
func (a *ACKHandler) TrackMessage(ctx context.Context, msgID string, fromUID, toUID int64, op int32, body []byte) error {
	existing, _ := a.dao.GetMessageStatus(ctx, msgID)
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
	return a.dao.SetMessageStatus(ctx, msgID, fields)
}

// MarkDelivered marks a message as delivered.
func (a *ACKHandler) MarkDelivered(ctx context.Context, msgID string) error {
	return a.dao.UpdateMessageStatus(ctx, msgID, MsgStatusDelivered)
}

// MarkFailed marks a message as failed.
func (a *ACKHandler) MarkFailed(ctx context.Context, msgID string) error {
	return a.dao.UpdateMessageStatus(ctx, msgID, MsgStatusFailed)
}

// GetMessageStatus returns the current status of a message.
func (a *ACKHandler) GetMessageStatus(ctx context.Context, msgID string) (string, error) {
	data, err := a.dao.GetMessageStatus(ctx, msgID)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}
	return data["status"], nil
}
