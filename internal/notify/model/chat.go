package model

import "time"

const (
	ChatRoleCustomer = "customer"
	ChatRoleMerchant = "merchant"
	ChatRoleMember   = "member"

	ChatStatusPending   = "pending"
	ChatStatusSent      = "sent"
	ChatStatusDelivered = "delivered"
	ChatStatusRead      = "read"
	ChatStatusFailed    = "failed"

	ChatTypePrivate = "private"
	ChatTypeGroup   = "group"
)

// ChatConversation is an IM conversation for an order private chat or a merchant group.
type ChatConversation struct {
	ConversationID string    `json:"conversation_id"`
	Type           string    `json:"type"`
	OrderID        string    `json:"order_id,omitempty"`
	MerchantID     string    `json:"merchant_id,omitempty"`
	Title          string    `json:"title,omitempty"`
	CustomerUID    int64     `json:"customer_uid"`
	MerchantUID    int64     `json:"merchant_uid"`
	RoomID         string    `json:"room_id"`
	LastMessageID  string    `json:"last_message_id,omitempty"`
	LastMessageAt  time.Time `json:"last_message_at,omitempty"`
	UnreadCount    int64     `json:"unread_count,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ChatMessage is a direct IM message persisted outside the notification pipeline.
type ChatMessage struct {
	MessageID      string     `json:"message_id"`
	ConversationID string     `json:"conversation_id"`
	OrderID        string     `json:"order_id"`
	SenderUID      int64      `json:"sender_uid"`
	ReceiverUID    int64      `json:"receiver_uid"`
	SenderRole     string     `json:"sender_role"`
	Body           string     `json:"body"`
	Status         string     `json:"status"`
	DeliveryPath   string     `json:"delivery_path,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	ReadAt         *time.Time `json:"read_at,omitempty"`
}

// ChatMember records membership in a merchant group conversation.
type ChatMember struct {
	ConversationID string    `json:"conversation_id"`
	UserID         int64     `json:"user_id"`
	Role           string    `json:"role"`
	JoinedAt       time.Time `json:"joined_at"`
}
