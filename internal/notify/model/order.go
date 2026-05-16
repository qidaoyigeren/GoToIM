package model

import "time"

// OrderStatus represents the state of an e-commerce order.
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

// Order represents an e-commerce order.
type Order struct {
	OrderID   string      `json:"order_id"`
	UserID    string      `json:"user_id"`
	Status    OrderStatus `json:"status"`
	Items     []OrderItem `json:"items"`
	Total     float64     `json:"total"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// OrderItem represents a line item in an order.
type OrderItem struct {
	ProductName string  `json:"product_name"`
	Quantity    int     `json:"quantity"`
	Price       float64 `json:"price"`
}

// orderTransitions defines valid order state transitions.
var orderTransitions = map[OrderStatus][]OrderStatus{
	OrderCreated:        {OrderPaid, OrderCancelled},
	OrderPaid:           {OrderConfirmed, OrderCancelled},
	OrderConfirmed:      {OrderShipped, OrderCancelled},
	OrderShipped:        {OrderDelivered, OrderDeliveryFailed},
	OrderDelivered:      {},
	OrderCancelled:      {},
	OrderDeliveryFailed: {},
}

// ValidTransition checks if a status transition is valid.
func ValidTransition(from, to OrderStatus) bool {
	for _, valid := range orderTransitions[from] {
		if valid == to {
			return true
		}
	}
	return false
}
