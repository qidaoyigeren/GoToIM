package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/notify/model"
)

var (
	ErrChatConversationNotFound = errors.New("chat conversation not found")
	ErrChatForbidden            = errors.New("user is not a participant in this conversation")
	chatConversationSeq         int64
	chatMessageSeq              int64
)

// ChatStore is the persistence surface required by ChatService.
type ChatStore interface {
	UpsertChatConversation(conv *model.ChatConversation) (*model.ChatConversation, error)
	GetChatConversation(conversationID string) (*model.ChatConversation, error)
	ListChatConversations(userID int64) ([]*model.ChatConversation, error)
	InsertChatMessage(msg *model.ChatMessage) error
	ListChatMessages(conversationID string, limit int) ([]*model.ChatMessage, error)
	UpdateChatMessageStatus(messageID, status string, deliveredAt, readAt *time.Time) error
}

type chatGroupStore interface {
	GetMerchantGroupByRoomID(roomID string) (*model.MerchantGroup, error)
	AddChatMember(conversationID string, userID int64, role string, joinedAt time.Time) error
	IsChatMember(conversationID string, userID int64) (bool, error)
}

// ChatPusher calls Logic directly. It deliberately bypasses notification outbox.
type ChatPusher interface {
	PushJSONToUsersDetailedContext(ctx context.Context, op int32, mids []int64, data interface{}) ([]DeliveryResult, error)
}

type roomChatPusher interface {
	PushToRoomContext(ctx context.Context, op int32, roomType, roomID string, body []byte) (string, error)
}

// ChatService handles order-scoped customer service chat.
type ChatService struct {
	store  ChatStore
	pusher ChatPusher
}

func NewChatService(store ChatStore, pusher ChatPusher) *ChatService {
	return &ChatService{store: store, pusher: pusher}
}

func (s *ChatService) CreateConversation(orderID string, customerUID, merchantUID int64) (*model.ChatConversation, error) {
	now := time.Now()
	return s.store.UpsertChatConversation(&model.ChatConversation{
		ConversationID: generateChatConversationID(),
		Type:           model.ChatTypePrivate,
		OrderID:        orderID,
		Title:          "订单私聊 " + orderID,
		CustomerUID:    customerUID,
		MerchantUID:    merchantUID,
		RoomID:         fmt.Sprintf("order_chat:%s", orderID),
		CreatedAt:      now,
		UpdatedAt:      now,
	})
}

func (s *ChatService) JoinMerchantGroup(roomID string, userID int64) (*model.ChatConversation, error) {
	gs, ok := s.store.(chatGroupStore)
	if !ok {
		return nil, fmt.Errorf("chat group store is not configured")
	}
	group, err := gs.GetMerchantGroupByRoomID(roomID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	conv := &model.ChatConversation{
		ConversationID: group.GroupID,
		Type:           model.ChatTypeGroup,
		OrderID:        "group:" + group.RoomID,
		MerchantID:     group.MerchantID,
		Title:          group.Name,
		CustomerUID:    0,
		MerchantUID:    group.MerchantUID,
		RoomID:         group.RoomID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	conv, err = s.store.UpsertChatConversation(conv)
	if err != nil {
		return nil, err
	}
	role := model.ChatRoleMember
	if userID == group.MerchantUID {
		role = model.ChatRoleMerchant
	}
	if err := gs.AddChatMember(conv.ConversationID, userID, role, now); err != nil {
		return nil, err
	}
	return conv, nil
}

func (s *ChatService) ListConversations(userID int64) ([]*model.ChatConversation, error) {
	return s.store.ListChatConversations(userID)
}

func (s *ChatService) ListMessages(conversationID string, userID int64, limit int) ([]*model.ChatMessage, error) {
	conv, err := s.store.GetChatConversation(conversationID)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canAccessConversation(conv, userID); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrChatForbidden
	}
	return s.store.ListChatMessages(conversationID, limit)
}

func (s *ChatService) SendMessage(ctx context.Context, conversationID string, senderUID int64, body string) (*model.ChatMessage, error) {
	conv, err := s.store.GetChatConversation(conversationID)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canAccessConversation(conv, senderUID); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrChatForbidden
	}

	if conv.Type == model.ChatTypeGroup {
		return s.sendGroupMessage(ctx, conv, senderUID, body)
	}

	receiverUID := conv.CustomerUID
	role := model.ChatRoleMerchant
	if senderUID == conv.CustomerUID {
		receiverUID = conv.MerchantUID
		role = model.ChatRoleCustomer
	}

	now := time.Now()
	msg := &model.ChatMessage{
		MessageID:      generateChatMessageID(),
		ConversationID: conversationID,
		OrderID:        conv.OrderID,
		SenderUID:      senderUID,
		ReceiverUID:    receiverUID,
		SenderRole:     role,
		Body:           body,
		Status:         model.ChatStatusPending,
		CreatedAt:      now,
	}

	if err := s.store.InsertChatMessage(msg); err != nil {
		return nil, err
	}

	msg.Status, msg.DeliveryPath, msg.DeliveredAt = s.deliver(ctx, conv, msg)
	if err := s.store.UpdateChatMessageStatus(msg.MessageID, msg.Status, msg.DeliveredAt, msg.ReadAt); err != nil {
		return msg, err
	}
	return msg, nil
}

func (s *ChatService) sendGroupMessage(ctx context.Context, conv *model.ChatConversation, senderUID int64, body string) (*model.ChatMessage, error) {
	now := time.Now()
	msg := &model.ChatMessage{
		MessageID:      generateChatMessageID(),
		ConversationID: conv.ConversationID,
		OrderID:        conv.OrderID,
		SenderUID:      senderUID,
		ReceiverUID:    0,
		SenderRole:     model.ChatRoleMember,
		Body:           body,
		Status:         model.ChatStatusPending,
		CreatedAt:      now,
	}
	if senderUID == conv.MerchantUID {
		msg.SenderRole = model.ChatRoleMerchant
	}
	if err := s.store.InsertChatMessage(msg); err != nil {
		return nil, err
	}
	msg.Status, msg.DeliveryPath, msg.DeliveredAt = s.deliverGroup(ctx, conv, msg)
	if err := s.store.UpdateChatMessageStatus(msg.MessageID, msg.Status, msg.DeliveredAt, msg.ReadAt); err != nil {
		return msg, err
	}
	return msg, nil
}

func (s *ChatService) MarkMessageStatus(messageID, status string) error {
	now := time.Now()
	switch status {
	case model.ChatStatusDelivered:
		return s.store.UpdateChatMessageStatus(messageID, status, &now, nil)
	case model.ChatStatusRead:
		return s.store.UpdateChatMessageStatus(messageID, status, &now, &now)
	case model.ChatStatusPending, model.ChatStatusSent, model.ChatStatusFailed:
		return s.store.UpdateChatMessageStatus(messageID, status, nil, nil)
	default:
		return fmt.Errorf("unsupported chat status: %s", status)
	}
}

func (s *ChatService) deliver(ctx context.Context, conv *model.ChatConversation, msg *model.ChatMessage) (string, string, *time.Time) {
	if s.pusher == nil {
		return model.ChatStatusPending, "no_logic_pusher", nil
	}
	payload := map[string]interface{}{
		"type":            "chat_message",
		"message_id":      msg.MessageID,
		"conversation_id": msg.ConversationID,
		"order_id":        msg.OrderID,
		"room_type":       model.ChatTypePrivate,
		"merchant_id":     conv.MerchantID,
		"room_id":         conv.RoomID,
		"sender_uid":      msg.SenderUID,
		"receiver_uid":    msg.ReceiverUID,
		"sender_role":     msg.SenderRole,
		"body":            msg.Body,
		"status":          msg.Status,
		"delivery_path":   "direct_push",
		"timestamp":       msg.CreatedAt.UnixMilli(),
	}
	results, err := s.pusher.PushJSONToUsersDetailedContext(ctx, protocol.OpRaw, []int64{msg.ReceiverUID}, payload)
	if err != nil {
		return model.ChatStatusFailed, err.Error(), nil
	}
	path := "logic_push"
	if len(results) > 0 && results[0].Path != "" {
		path = results[0].Path
	}
	if path == "grpc_direct" || path == "direct_sent" {
		now := time.Now()
		return model.ChatStatusDelivered, path, &now
	}
	if path == "offline_stored" {
		return model.ChatStatusPending, path, nil
	}
	return model.ChatStatusSent, path, nil
}

func (s *ChatService) deliverGroup(ctx context.Context, conv *model.ChatConversation, msg *model.ChatMessage) (string, string, *time.Time) {
	pusher, ok := s.pusher.(roomChatPusher)
	if s.pusher == nil || !ok {
		return model.ChatStatusPending, "no_room_pusher", nil
	}
	payload := map[string]interface{}{
		"type":            "group_chat_message",
		"message_id":      msg.MessageID,
		"conversation_id": msg.ConversationID,
		"order_id":        msg.OrderID,
		"room_type":       model.ChatTypeGroup,
		"merchant_id":     conv.MerchantID,
		"room_id":         conv.RoomID,
		"sender_uid":      msg.SenderUID,
		"sender_role":     msg.SenderRole,
		"body":            msg.Body,
		"status":          msg.Status,
		"delivery_path":   "room_push",
		"timestamp":       msg.CreatedAt.UnixMilli(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return model.ChatStatusFailed, err.Error(), nil
	}
	_, err = pusher.PushToRoomContext(ctx, protocol.OpRaw, "live", conv.RoomID, body)
	if err != nil {
		return model.ChatStatusFailed, err.Error(), nil
	}
	now := time.Now()
	return model.ChatStatusSent, "room_push", &now
}

func (s *ChatService) canAccessConversation(conv *model.ChatConversation, uid int64) (bool, error) {
	if conv == nil {
		return false, nil
	}
	if conv.Type == model.ChatTypeGroup {
		if uid == conv.MerchantUID {
			return true, nil
		}
		gs, ok := s.store.(chatGroupStore)
		if !ok {
			return false, nil
		}
		return gs.IsChatMember(conv.ConversationID, uid)
	}
	return isChatParticipant(conv, uid), nil
}

func isChatParticipant(conv *model.ChatConversation, uid int64) bool {
	return conv != nil && (uid == conv.CustomerUID || uid == conv.MerchantUID)
}

func generateChatConversationID() string {
	seq := atomic.AddInt64(&chatConversationSeq, 1)
	return fmt.Sprintf("CHAT-%d-%06d", time.Now().UnixNano(), seq)
}

func generateChatMessageID() string {
	seq := atomic.AddInt64(&chatMessageSeq, 1)
	return fmt.Sprintf("CHM-%d-%06d", time.Now().UnixNano(), seq)
}
