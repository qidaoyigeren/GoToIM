package model

import "time"

// FlashSale represents a time-limited promotional event.
type FlashSale struct {
	SaleID      string    `json:"sale_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	TargetUIDs  []string  `json:"target_uids"` // empty = broadcast to all online users
	StartAt     time.Time `json:"start_at"`
	CreatedAt   time.Time `json:"created_at"`
}
