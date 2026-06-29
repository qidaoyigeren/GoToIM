package model

import "time"

// DeliveryMessage is a flattened row for the dashboard delivery status page.
type DeliveryMessage struct {
	ID             string    `json:"id"`
	MessageID      string    `json:"message_id"`
	RecordKind     string    `json:"record_kind"`
	Type           string    `json:"type"`
	OrderID        string    `json:"order_id,omitempty"`
	Sender         string    `json:"sender,omitempty"`
	Target         string    `json:"target,omitempty"`
	DeliveryMethod string    `json:"delivery_method,omitempty"`
	Status         string    `json:"status"`
	Priority       string    `json:"priority,omitempty"`
	TTLSeconds     int64     `json:"ttl_seconds,omitempty"`
	AckPolicy      string    `json:"ack_policy,omitempty"`
	BusinessType   string    `json:"business_type,omitempty"`
	EventType      string    `json:"event_type,omitempty"`
	TraceID        string    `json:"trace_id,omitempty"`
	RetryCount     int64     `json:"retry_count,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	RoomID         string    `json:"room_id,omitempty"`
	SenderUID      int64     `json:"sender_uid,omitempty"`
	ReceiverUID    int64     `json:"receiver_uid,omitempty"`
	SenderRole     string    `json:"sender_role,omitempty"`
	TargetNode     string    `json:"target_node,omitempty"`
	LatencyMs      float64   `json:"latency_ms,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

// DeliveryMessageDetail expands one delivery row with raw trace material.
type DeliveryMessageDetail struct {
	Message           *DeliveryMessage       `json:"message"`
	RawPayload        interface{}            `json:"raw_payload,omitempty"`
	BusinessType      string                 `json:"business_type,omitempty"`
	EventType         string                 `json:"event_type,omitempty"`
	TraceID           string                 `json:"trace_id,omitempty"`
	DeliveryPath      string                 `json:"delivery_path,omitempty"`
	Attempts          []*NotificationAttempt `json:"attempts,omitempty"`
	ACKs              []*NotificationAck     `json:"acks,omitempty"`
	RetryCount        int64                  `json:"retry_count,omitempty"`
	DLQ               *NotificationDLQ       `json:"dlq,omitempty"`
	FailureReason     string                 `json:"failure_reason,omitempty"`
	LastError         string                 `json:"last_error,omitempty"`
	TargetNode        string                 `json:"target_node,omitempty"`
	LatencyMs         float64                `json:"latency_ms,omitempty"`
	Order             *Order                 `json:"order,omitempty"`
	NotificationTrace *NotificationTrace     `json:"notification_trace,omitempty"`
	OrderTimeline     *OrderTimeline         `json:"order_timeline,omitempty"`
	ChatMessage       *ChatMessage           `json:"chat_message,omitempty"`
	Conversation      *ChatConversation      `json:"conversation,omitempty"`
}
