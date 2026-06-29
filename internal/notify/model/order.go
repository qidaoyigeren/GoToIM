package model

import (
	"fmt"
	"time"
)

// OrderStatus represents the state of a purchase order.
type OrderStatus string

const (
	OrderCreated        OrderStatus = "created"
	OrderPaid           OrderStatus = "paid"
	OrderConfirmed      OrderStatus = "confirmed"
	OrderShipped        OrderStatus = "shipped"
	OrderDelivered      OrderStatus = "delivered"
	OrderCancelled      OrderStatus = "cancelled"
	OrderDeliveryFailed OrderStatus = "delivery_failed"
)

// OrderType describes the demo purchase form. The value intentionally stays
// lightweight because GoIM delivery is the focus of this service.
type OrderType string

const (
	OrderTypeNormal     OrderType = "normal"
	OrderTypePresale    OrderType = "presale"
	OrderTypeUrgent     OrderType = "urgent"
	OrderTypeEnterprise OrderType = "enterprise"
	OrderTypeAfterSale  OrderType = "after_sale"
	OrderTypeVirtual    OrderType = "virtual"
)

// OrderImportance controls notification priority, TTL, and ACK policy.
type OrderImportance string

const (
	OrderImportanceNormal   OrderImportance = "normal"
	OrderImportanceHigh     OrderImportance = "high"
	OrderImportanceUrgent   OrderImportance = "urgent"
	OrderImportanceCritical OrderImportance = "critical"
)

// Order represents a demo purchase order.
type Order struct {
	OrderID               string          `json:"order_id"`
	UserID                string          `json:"user_id"`
	MerchantID            string          `json:"merchant_id,omitempty"`
	MerchantUID           int64           `json:"merchant_uid,omitempty"`
	MerchantName          string          `json:"merchant_name,omitempty"`
	Status                OrderStatus     `json:"status"`
	OrderType             OrderType       `json:"order_type,omitempty"`
	Importance            OrderImportance `json:"importance,omitempty"`
	BuyerNote             string          `json:"buyer_note,omitempty"`
	FulfillmentMode       string          `json:"fulfillment_mode,omitempty"`
	SupportRoomID         string          `json:"support_room_id,omitempty"`
	PrivateConversationID string          `json:"private_conversation_id,omitempty"`
	Items                 []OrderItem     `json:"items"`
	Total                 float64         `json:"total"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

// OrderItem represents a line item in an order.
type OrderItem struct {
	ProductID   string  `json:"product_id,omitempty"`
	SKUID       string  `json:"sku_id,omitempty"`
	ProductName string  `json:"product_name"`
	Quantity    int     `json:"quantity"`
	Price       float64 `json:"price"`
	ImageURL    string  `json:"image_url,omitempty"`
}

// OrderStatusEvent is an append-only record for an order state change.
type OrderStatusEvent struct {
	EventID        string            `json:"event_id"`
	OrderID        string            `json:"order_id"`
	FromStatus     OrderStatus       `json:"from_status"`
	ToStatus       OrderStatus       `json:"to_status"`
	Extra          map[string]string `json:"extra,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

// orderTransitions defines valid order state transitions.
var orderTransitions = map[OrderStatus][]OrderStatus{
	OrderCreated:        {OrderConfirmed, OrderCancelled},
	OrderPaid:           {OrderConfirmed, OrderCancelled},
	OrderConfirmed:      {OrderShipped, OrderCancelled},
	OrderShipped:        {OrderDelivered, OrderDeliveryFailed},
	OrderDelivered:      {},
	OrderCancelled:      {},
	OrderDeliveryFailed: {},
}

// TransitionError describes why an order status transition is invalid.
type TransitionError struct {
	From      OrderStatus   `json:"from"`
	To        OrderStatus   `json:"to"`
	Allowed   []OrderStatus `json:"allowed"`
	Reason    string        `json:"reason"`
	Timestamp time.Time     `json:"timestamp"`
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("invalid transition from %s to %s: allowed %v", e.From, e.To, e.Allowed)
}

// ValidTransition checks if a status transition is valid.
// Returns nil if valid, or a TransitionError if invalid.
func ValidTransition(from, to OrderStatus) error {
	allowed := orderTransitions[from]
	for _, valid := range allowed {
		if valid == to {
			return nil
		}
	}
	return &TransitionError{
		From:      from,
		To:        to,
		Allowed:   allowed,
		Reason:    fmt.Sprintf("status %s cannot transition to %s", from, to),
		Timestamp: time.Now(),
	}
}
