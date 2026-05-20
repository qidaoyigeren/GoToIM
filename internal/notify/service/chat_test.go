package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

func TestChatServiceConversationMessageAndReadFlow(t *testing.T) {
	store := newMemoryChatStore()
	pusher := &fakeChatPusher{path: "grpc_direct"}
	svc := NewChatService(store, pusher)

	conv, err := svc.CreateConversation("ORD-chat-1", 10001, 90001)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if conv.RoomID != "order_chat:ORD-chat-1" {
		t.Fatalf("room id = %q", conv.RoomID)
	}

	convs, err := svc.ListConversations(10001)
	if err != nil || len(convs) != 1 {
		t.Fatalf("ListConversations len=%d err=%v", len(convs), err)
	}

	msg, err := svc.SendMessage(context.Background(), conv.ConversationID, 10001, "Where is my package?")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if msg.ReceiverUID != 90001 || msg.SenderRole != model.ChatRoleCustomer {
		t.Fatalf("message route sender=%s receiver=%d", msg.SenderRole, msg.ReceiverUID)
	}
	if msg.Status != model.ChatStatusDelivered || msg.DeliveredAt == nil {
		t.Fatalf("message status=%s delivered_at=%v", msg.Status, msg.DeliveredAt)
	}
	if len(pusher.calls) != 1 || pusher.calls[0].mids[0] != 90001 {
		t.Fatalf("pusher calls=%+v", pusher.calls)
	}

	messages, err := svc.ListMessages(conv.ConversationID, 90001, 100)
	if err != nil || len(messages) != 1 {
		t.Fatalf("ListMessages len=%d err=%v", len(messages), err)
	}

	if err := svc.MarkMessageStatus(msg.MessageID, model.ChatStatusRead); err != nil {
		t.Fatalf("MarkMessageStatus: %v", err)
	}
	messages, _ = svc.ListMessages(conv.ConversationID, 90001, 100)
	if messages[0].Status != model.ChatStatusRead || messages[0].ReadAt == nil {
		t.Fatalf("read message status=%s read_at=%v", messages[0].Status, messages[0].ReadAt)
	}
}

type memoryChatStore struct {
	mu            sync.Mutex
	conversations map[string]*model.ChatConversation
	messages      map[string]*model.ChatMessage
}

func newMemoryChatStore() *memoryChatStore {
	return &memoryChatStore{
		conversations: make(map[string]*model.ChatConversation),
		messages:      make(map[string]*model.ChatMessage),
	}
}

func (s *memoryChatStore) UpsertChatConversation(conv *model.ChatConversation) (*model.ChatConversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.conversations {
		if existing.OrderID == conv.OrderID && existing.CustomerUID == conv.CustomerUID && existing.MerchantUID == conv.MerchantUID {
			return cloneConversation(existing), nil
		}
	}
	s.conversations[conv.ConversationID] = cloneConversation(conv)
	return cloneConversation(conv), nil
}

func (s *memoryChatStore) GetChatConversation(conversationID string) (*model.ChatConversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv := s.conversations[conversationID]
	if conv == nil {
		return nil, ErrChatConversationNotFound
	}
	return cloneConversation(conv), nil
}

func (s *memoryChatStore) ListChatConversations(userID int64) ([]*model.ChatConversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.ChatConversation
	for _, conv := range s.conversations {
		if conv.CustomerUID == userID || conv.MerchantUID == userID {
			out = append(out, cloneConversation(conv))
		}
	}
	return out, nil
}

func (s *memoryChatStore) InsertChatMessage(msg *model.ChatMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[msg.MessageID] = cloneMessage(msg)
	if conv := s.conversations[msg.ConversationID]; conv != nil {
		conv.LastMessageID = msg.MessageID
		conv.LastMessageAt = msg.CreatedAt
		conv.UpdatedAt = msg.CreatedAt
	}
	return nil
}

func (s *memoryChatStore) ListChatMessages(conversationID string, limit int) ([]*model.ChatMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.ChatMessage
	for _, msg := range s.messages {
		if msg.ConversationID == conversationID {
			out = append(out, cloneMessage(msg))
		}
	}
	return out, nil
}

func (s *memoryChatStore) UpdateChatMessageStatus(messageID, status string, deliveredAt, readAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := s.messages[messageID]
	if msg == nil {
		return ErrChatConversationNotFound
	}
	msg.Status = status
	if deliveredAt != nil {
		msg.DeliveredAt = deliveredAt
	}
	if readAt != nil {
		msg.ReadAt = readAt
	}
	return nil
}

type fakeChatPusher struct {
	path  string
	calls []fakeChatPushCall
}

type fakeChatPushCall struct {
	op   int32
	mids []int64
	data interface{}
}

func (f *fakeChatPusher) PushJSONToUsersDetailedContext(_ context.Context, op int32, mids []int64, data interface{}) ([]DeliveryResult, error) {
	f.calls = append(f.calls, fakeChatPushCall{op: op, mids: mids, data: data})
	return []DeliveryResult{{MsgID: "logic-msg-1", Path: f.path, AttemptNo: 1}}, nil
}

func cloneConversation(conv *model.ChatConversation) *model.ChatConversation {
	cp := *conv
	return &cp
}

func cloneMessage(msg *model.ChatMessage) *model.ChatMessage {
	cp := *msg
	return &cp
}
