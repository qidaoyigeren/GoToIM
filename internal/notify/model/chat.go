package model

import "time"

const (
	ChatRoleCustomer = "customer"
	ChatRoleMerchant = "merchant"

	ChatStatusPending   = "pending"
	ChatStatusSent      = "sent"
	ChatStatusDelivered = "delivered"
	ChatStatusRead      = "read"
	ChatStatusFailed    = "failed"
)

// ChatConversation is a one-to-one order-scoped customer service thread.
type ChatConversation struct {
	ConversationID string    `json:"conversation_id"`
	OrderID        string    `json:"order_id"`
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
