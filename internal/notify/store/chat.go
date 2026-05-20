package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

// UpsertChatConversation creates or returns an order-scoped customer support conversation.
func (s *SQLStore) UpsertChatConversation(conv *model.ChatConversation) (*model.ChatConversation, error) {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO chat_conversations
		(conversation_id, order_id, customer_uid, merchant_uid, room_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at)`,
		conv.ConversationID, conv.OrderID, conv.CustomerUID, conv.MerchantUID, conv.RoomID,
		formatTime(conv.CreatedAt), formatTime(conv.UpdatedAt))
	if err != nil {
		return nil, err
	}
	return s.GetChatConversationByOrder(conv.OrderID, conv.CustomerUID, conv.MerchantUID)
}

// GetChatConversation returns one conversation by id.
func (s *SQLStore) GetChatConversation(conversationID string) (*model.ChatConversation, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT conversation_id, order_id, customer_uid, merchant_uid, room_id,
		COALESCE(last_message_id, ''), COALESCE(last_message_at, ''), created_at, updated_at
		FROM chat_conversations WHERE conversation_id = ?`, conversationID)
	return scanChatConversation(row)
}

// GetChatConversationByOrder returns one conversation by order and user pair.
func (s *SQLStore) GetChatConversationByOrder(orderID string, customerUID, merchantUID int64) (*model.ChatConversation, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT conversation_id, order_id, customer_uid, merchant_uid, room_id,
		COALESCE(last_message_id, ''), COALESCE(last_message_at, ''), created_at, updated_at
		FROM chat_conversations WHERE order_id = ? AND customer_uid = ? AND merchant_uid = ?`,
		orderID, customerUID, merchantUID)
	return scanChatConversation(row)
}

// ListChatConversations lists conversations visible to one user.
func (s *SQLStore) ListChatConversations(userID int64) ([]*model.ChatConversation, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT conversation_id, order_id, customer_uid, merchant_uid, room_id,
		COALESCE(last_message_id, ''), COALESCE(last_message_at, ''), created_at, updated_at
		FROM chat_conversations
		WHERE customer_uid = ? OR merchant_uid = ?
		ORDER BY COALESCE(last_message_at, updated_at) DESC`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conversations []*model.ChatConversation
	for rows.Next() {
		conv, err := scanChatConversation(rows)
		if err != nil {
			return nil, err
		}
		if n, err := s.chatUnreadCount(conv.ConversationID, userID); err == nil {
			conv.UnreadCount = n
		}
		conversations = append(conversations, conv)
	}
	return conversations, rows.Err()
}

// InsertChatMessage inserts a direct IM chat message and updates the conversation summary.
func (s *SQLStore) InsertChatMessage(msg *model.ChatMessage) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(context.Background(), `INSERT INTO chat_messages
		(message_id, conversation_id, order_id, sender_uid, receiver_uid, sender_role, body, status, delivery_path, created_at, delivered_at, read_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.MessageID, msg.ConversationID, msg.OrderID, msg.SenderUID, msg.ReceiverUID, msg.SenderRole,
		msg.Body, msg.Status, msg.DeliveryPath, formatTime(msg.CreatedAt), nullableTimePtr(msg.DeliveredAt), nullableTimePtr(msg.ReadAt)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE chat_conversations
		SET last_message_id = ?, last_message_at = ?, updated_at = ? WHERE conversation_id = ?`,
		msg.MessageID, formatTime(msg.CreatedAt), formatTime(msg.CreatedAt), msg.ConversationID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateChatMessageStatus updates delivery/read state for one message.
func (s *SQLStore) UpdateChatMessageStatus(messageID, status string, deliveredAt, readAt *time.Time) error {
	if deliveredAt != nil && readAt != nil {
		_, err := s.db.ExecContext(context.Background(), `UPDATE chat_messages SET status = ?, delivered_at = ?, read_at = ? WHERE message_id = ?`,
			status, formatTime(*deliveredAt), formatTime(*readAt), messageID)
		return err
	}
	if deliveredAt != nil {
		_, err := s.db.ExecContext(context.Background(), `UPDATE chat_messages SET status = ?, delivered_at = ? WHERE message_id = ?`,
			status, formatTime(*deliveredAt), messageID)
		return err
	}
	if readAt != nil {
		_, err := s.db.ExecContext(context.Background(), `UPDATE chat_messages SET status = ?, read_at = ? WHERE message_id = ?`,
			status, formatTime(*readAt), messageID)
		return err
	}
	_, err := s.db.ExecContext(context.Background(), `UPDATE chat_messages SET status = ? WHERE message_id = ?`, status, messageID)
	return err
}

// ListChatMessages lists messages in a conversation in chronological order.
func (s *SQLStore) ListChatMessages(conversationID string, limit int) ([]*model.ChatMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(context.Background(), `SELECT message_id, conversation_id, order_id, sender_uid, receiver_uid,
		sender_role, body, status, COALESCE(delivery_path, ''), created_at, delivered_at, read_at
		FROM chat_messages WHERE conversation_id = ? ORDER BY created_at ASC LIMIT ?`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.ChatMessage
	for rows.Next() {
		msg, err := scanChatMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func (s *SQLStore) chatUnreadCount(conversationID string, userID int64) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM chat_messages
		WHERE conversation_id = ? AND receiver_uid = ? AND status <> 'read'`, conversationID, userID).Scan(&n)
	return n, err
}

func scanChatConversation(row scanner) (*model.ChatConversation, error) {
	var conv model.ChatConversation
	var lastMessageAt, createdAt, updatedAt string
	if err := row.Scan(&conv.ConversationID, &conv.OrderID, &conv.CustomerUID, &conv.MerchantUID, &conv.RoomID,
		&conv.LastMessageID, &lastMessageAt, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if lastMessageAt != "" {
		if t, err := parseTime(lastMessageAt); err == nil {
			conv.LastMessageAt = t
		}
	}
	var err error
	conv.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return nil, err
	}
	conv.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

func scanChatMessage(row scanner) (*model.ChatMessage, error) {
	var msg model.ChatMessage
	var createdAt string
	var deliveredAt, readAt sql.NullString
	if err := row.Scan(&msg.MessageID, &msg.ConversationID, &msg.OrderID, &msg.SenderUID, &msg.ReceiverUID,
		&msg.SenderRole, &msg.Body, &msg.Status, &msg.DeliveryPath, &createdAt, &deliveredAt, &readAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var err error
	msg.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return nil, err
	}
	if deliveredAt.Valid && deliveredAt.String != "" {
		t, err := parseTime(deliveredAt.String)
		if err != nil {
			return nil, err
		}
		msg.DeliveredAt = &t
	}
	if readAt.Valid && readAt.String != "" {
		t, err := parseTime(readAt.String)
		if err != nil {
			return nil, err
		}
		msg.ReadAt = &t
	}
	return &msg, nil
}
