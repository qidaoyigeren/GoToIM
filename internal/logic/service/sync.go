package service

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/dao"
	log "github.com/Terry-Mao/goim/pkg/log"
)

// DirectPusher pushes messages directly to specific sessions via gRPC.
type DirectPusher interface {
	DirectPush(ctx context.Context, sessions []*Session, op int32, body []byte) error
}

// SyncService handles offline message synchronization and multi-device sync.
type SyncService struct {
	dao     dao.MessageDAO
	sessMgr *SessionManager
	pusher  DirectPusher
}

// NewSyncService creates a new SyncService.
func NewSyncService(d dao.MessageDAO, sessMgr *SessionManager, pusher DirectPusher) *SyncService {
	return &SyncService{
		dao:     d,
		sessMgr: sessMgr,
		pusher:  pusher,
	}
}

// OnUserOnline is called when a user connects. It checks for offline messages
// and triggers sync if needed.
func (s *SyncService) OnUserOnline(ctx context.Context, uid int64, lastSeq int64) error {
	// Get offline messages since lastSeq
	msgIDs, err := s.dao.GetOfflineQueue(ctx, uid, float64(lastSeq), 100)
	if err != nil {
		return fmt.Errorf("get offline queue: %w", err)
	}

	if len(msgIDs) == 0 {
		return nil
	}

	log.Infof("syncing offline messages: uid=%d count=%d last_seq=%d", uid, len(msgIDs), lastSeq)

	// Get user sessions for push
	sessions, err := s.sessMgr.GetSessions(ctx, uid)
	if err != nil || len(sessions) == 0 {
		return nil
	}

	// Push each offline message to all user sessions
	seq := lastSeq
	for _, msgID := range msgIDs {
		// Get message status to find the original message data
		msgData, err := s.dao.GetMessageStatus(ctx, msgID)
		if err != nil || len(msgData) == 0 {
			continue
		}

		status := msgData["status"]
		if status == MsgStatusAcked {
			// Already acked, remove from queue
			s.dao.RemoveFromOfflineQueue(ctx, uid, msgID)
			continue
		}

		seq++
		// Build sync message with full content
		mb := &protocol.MsgBody{
			MsgID: msgID,
			Seq:   seq,
		}
		if v, ok := msgData["from_uid"]; ok {
			fmt.Sscanf(v, "%d", &mb.FromUID)
		}
		if v, ok := msgData["to_uid"]; ok {
			fmt.Sscanf(v, "%d", &mb.ToUID)
		}
		if v, ok := msgData["created_at"]; ok {
			fmt.Sscanf(v, "%d", &mb.Timestamp)
		}
		if v, ok := msgData["body"]; ok && v != "" {
			if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
				mb.Content = decoded
			}
		}

		syncBody := &protocol.SyncReplyBody{
			CurrentSeq: seq,
			HasMore:    false,
			Messages:   []*protocol.MsgBody{mb},
		}

		syncBytes, err := protocol.MarshalSyncReply(syncBody)
		if err != nil {
			log.Warningf("marshal sync reply failed: %v", err)
			continue
		}

		// Push sync data to all sessions
		for _, sess := range sessions {
			if sess == nil {
				continue
			}
			s.pusher.DirectPush(ctx, []*Session{sess}, protocol.OpSyncReply, syncBytes)
		}
	}

	return nil
}

// GetOfflineMessages returns offline messages for a user since lastSeq.
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

	seq := lastSeq
	for _, msgID := range msgIDs {
		msgData, err := s.dao.GetMessageStatus(ctx, msgID)
		if err != nil || len(msgData) == 0 {
			continue
		}

		status := msgData["status"]
		if status == MsgStatusAcked {
			s.dao.RemoveFromOfflineQueue(ctx, uid, msgID)
			continue
		}

		seq++
		mb := &protocol.MsgBody{
			MsgID: msgID,
			Seq:   seq,
		}
		// Parse stored message metadata
		if v, ok := msgData["from_uid"]; ok {
			fmt.Sscanf(v, "%d", &mb.FromUID)
		}
		if v, ok := msgData["to_uid"]; ok {
			fmt.Sscanf(v, "%d", &mb.ToUID)
		}
		if v, ok := msgData["created_at"]; ok {
			fmt.Sscanf(v, "%d", &mb.Timestamp)
		}
		// Read stored message body (base64-encoded)
		if v, ok := msgData["body"]; ok && v != "" {
			if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
				mb.Content = decoded
			}
		}

		reply.Messages = append(reply.Messages, mb)
		reply.CurrentSeq = seq
	}

	return reply, nil
}
