package model

import "time"

// Merchant is a lightweight demo merchant that owns products and a support room.
type Merchant struct {
	MerchantID  string    `json:"merchant_id"`
	MerchantUID int64     `json:"merchant_uid"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	LogoURL     string    `json:"logo_url,omitempty"`
	GroupRoomID string    `json:"group_room_id,omitempty"`
	GroupName   string    `json:"group_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Product is a demo purchasable item. Inventory is intentionally omitted.
type Product struct {
	ProductID       string    `json:"product_id"`
	MerchantID      string    `json:"merchant_id"`
	SKUID           string    `json:"sku_id,omitempty"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	Price           float64   `json:"price"`
	ImageURL        string    `json:"image_url,omitempty"`
	FulfillmentMode string    `json:"fulfillment_mode,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// MerchantGroup represents a merchant-created room users can join.
type MerchantGroup struct {
	GroupID     string    `json:"group_id"`
	MerchantID  string    `json:"merchant_id"`
	MerchantUID int64     `json:"merchant_uid"`
	RoomID      string    `json:"room_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	MemberCount int64     `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
