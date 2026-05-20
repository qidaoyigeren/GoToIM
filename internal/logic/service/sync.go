package service

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/dao"
	log "github.com/Terry-Mao/goim/pkg/log"
)

// DirectPusher defines the direct push capability used by SyncService.
type DirectPusher interface {
	DirectPush(ctx context.Context, sessions []*Session, op int32, body []byte) ([]*Session, error)
}

type batchMessageStatusGetter interface {
	BatchGetMessageStatus(ctx context.Context, msgIDs []string) ([]map[string]string, error)
}

// SyncService handles offline message sync and device cursor based pulls.
type SyncService struct {
	dao     dao.MessageDAO
	sessMgr *SessionManager
	pusher  DirectPusher
}

// NewSyncService creates a SyncService.
func NewSyncService(d dao.MessageDAO, sessMgr *SessionManager, pusher DirectPusher) *SyncService {
	return &SyncService{
		dao:     d,
		sessMgr: sessMgr,
		pusher:  pusher,
	}
}

// OnUserOnline syncs offline messages for legacy clients.
func (s *SyncService) OnUserOnline(ctx context.Context, uid int64, lastSeq int64) error {
	return s.OnUserOnlineWithDevice(ctx, uid, "", lastSeq)
}

// OnUserOnlineWithDevice syncs offline messages using a device-level cursor.
func (s *SyncService) OnUserOnlineWithDevice(ctx context.Context, uid int64, deviceID string, lastSeq int64) error {
	cursorSeq, _ := s.dao.GetDeviceCursor(ctx, uid, deviceID)
	if cursorSeq > lastSeq {
		lastSeq = cursorSeq
	}
	return s.syncOffline(ctx, uid, deviceID, lastSeq)
}

// GetOfflineMessagesByDevice fetches offline messages after the device cursor.
func (s *SyncService) GetOfflineMessagesByDevice(ctx context.Context, uid int64, deviceID string, limit int32) (*protocol.SyncReplyBody, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	msgIDs, err := s.dao.GetOfflineMessagesByDeviceCursor(ctx, uid, deviceID, int(limit))
	if err != nil {
		return nil, err
	}
	return s.buildSyncReply(ctx, uid, deviceID, msgIDs, limit)
}

func (s *SyncService) syncOffline(ctx context.Context, uid int64, deviceID string, lastSeq int64) error {
	msgIDs, err := s.dao.GetOfflineQueue(ctx, uid, float64(lastSeq), 100)
	if err != nil {
		return fmt.Errorf("get offline queue: %w", err)
	}
	if len(msgIDs) == 0 {
		return nil
	}

	log.Infof("syncing offline messages: uid=%d count=%d last_seq=%d", uid, len(msgIDs), lastSeq)

	sessions, err := s.sessMgr.GetSessions(ctx, uid)
	if err != nil || len(sessions) == 0 {
		return nil
	}

	statuses, err := s.batchGetMessageStatus(ctx, msgIDs)
	if err != nil {
		return fmt.Errorf("batch get message status: %w", err)
	}
	messages, seq := s.buildMessagesFromStatuses(ctx, uid, lastSeq, msgIDs, statuses)
	if len(messages) == 0 {
		return nil
	}

	syncBytes, err := protocol.MarshalSyncReply(&protocol.SyncReplyBody{
		CurrentSeq: seq,
		HasMore:    false,
		Messages:   messages,
	})
	if err != nil {
		return fmt.Errorf("marshal sync reply: %w", err)
	}

	if _, err := s.pusher.DirectPush(ctx, sessions, protocol.OpSyncReply, syncBytes); err != nil {
		log.Warningf("batch sync push failed: uid=%d count=%d err=%v", uid, len(messages), err)
		return nil
	}

	if deviceID != "" && seq > lastSeq {
		_ = s.dao.AdvanceDeviceCursor(ctx, uid, deviceID, seq)
	}
	return nil
}

func (s *SyncService) buildSyncReply(ctx context.Context, uid int64, deviceID string, msgIDs []string, limit int32) (*protocol.SyncReplyBody, error) {
	lastSeq, _ := s.dao.GetDeviceCursor(ctx, uid, deviceID)
	reply := &protocol.SyncReplyBody{
		CurrentSeq: lastSeq,
		HasMore:    len(msgIDs) >= int(limit),
	}
	statuses, err := s.batchGetMessageStatus(ctx, msgIDs)
	if err != nil {
		return nil, err
	}
	reply.Messages, reply.CurrentSeq = s.buildMessagesFromStatuses(ctx, uid, lastSeq, msgIDs, statuses)
	return reply, nil
}

// GetOfflineMessages fetches a page of offline messages after lastSeq.
func (s *SyncService) GetOfflineMessages(ctx context.Context, uid int64, lastSeq int64, limit int32) (*protocol.SyncReplyBody, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	msgIDs, err := s.dao.GetOfflineQueue(ctx, uid, float64(lastSeq), int(limit))
	if err != nil {
		return nil, fmt.Errorf("get offline queue: %w", err)
	}
	reply := &protocol.SyncReplyBody{
		CurrentSeq: lastSeq,
		HasMore:    len(msgIDs) >= int(limit),
	}
	statuses, err := s.batchGetMessageStatus(ctx, msgIDs)
	if err != nil {
		return nil, err
	}
	reply.Messages, reply.CurrentSeq = s.buildMessagesFromStatuses(ctx, uid, lastSeq, msgIDs, statuses)
	return reply, nil
}

func (s *SyncService) batchGetMessageStatus(ctx context.Context, msgIDs []string) ([]map[string]string, error) {
	if len(msgIDs) == 0 {
		return nil, nil
	}
	if getter, ok := s.dao.(batchMessageStatusGetter); ok {
		return getter.BatchGetMessageStatus(ctx, msgIDs)
	}
	statuses := make([]map[string]string, 0, len(msgIDs))
	for _, msgID := range msgIDs {
		data, err := s.dao.GetMessageStatus(ctx, msgID)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, data)
	}
	return statuses, nil
}

func (s *SyncService) buildMessagesFromStatuses(ctx context.Context, uid int64, lastSeq int64, msgIDs []string, statuses []map[string]string) ([]*protocol.MsgBody, int64) {
	seq := lastSeq
	messages := make([]*protocol.MsgBody, 0, len(msgIDs))
	for i, msgID := range msgIDs {
		if i >= len(statuses) {
			break
		}
		msgData := statuses[i]
		if len(msgData) == 0 {
			continue
		}
		if msgData["status"] == MsgStatusAcked {
			_ = s.dao.RemoveFromOfflineQueue(ctx, uid, msgID)
			continue
		}
		seq++
		messages = append(messages, msgBodyFromStatus(msgID, seq, msgData))
	}
	return messages, seq
}

func msgBodyFromStatus(msgID string, seq int64, msgData map[string]string) *protocol.MsgBody {
	mb := &protocol.MsgBody{MsgID: msgID, Seq: seq}
	if v := msgData["from_uid"]; v != "" {
		fmt.Sscanf(v, "%d", &mb.FromUID)
	}
	if v := msgData["to_uid"]; v != "" {
		fmt.Sscanf(v, "%d", &mb.ToUID)
	}
	if v := msgData["created_at"]; v != "" {
		fmt.Sscanf(v, "%d", &mb.Timestamp)
	}
	if v := msgData["body"]; v != "" {
		if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
			mb.Content = decoded
		}
	}
	return mb
}
