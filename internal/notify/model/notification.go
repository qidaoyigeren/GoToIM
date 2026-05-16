package model

import "time"

// NotifyType classifies a push notification.
type NotifyType string

const (
	NotifyOrderStatus NotifyType = "order_status"
	NotifyFlashSale   NotifyType = "flash_sale"
	NotifyLogistics   NotifyType = "logistics"
	NotifySystem      NotifyType = "system"
)

// Notification represents a push notification sent to a user.
type Notification struct {
	NotifyID  string     `json:"notify_id"`
	UserID    string     `json:"user_id"`
	Type      NotifyType `json:"type"`
	Title     string     `json:"title"`
	Content   string     `json:"content"`
	OrderID   string     `json:"order_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	Status    string     `json:"status"` // pending → delivered → acked → failed
}

// platformStats holds aggregate platform metrics.
type PlatformStats struct {
	PushRatePerSec float64           `json:"push_rate_per_sec"`
	TotalPushed    int64             `json:"total_pushed"`
	AckRate        float64           `json:"ack_rate"`
	LatencyP50Ms   float64           `json:"latency_p50_ms"`
	LatencyP99Ms   float64           `json:"latency_p99_ms"`
	LatencyMaxMs   float64           `json:"latency_max_ms"`
	ActiveConns    int64             `json:"active_connections"`
	DeliveryPath   DeliveryPathRatio `json:"delivery_path"`
	OnlineUsers    int64             `json:"online_users"`
	OfflinePending int64             `json:"offline_pending"`
	Simulation     SimulationState   `json:"simulation"`
}

// DeliveryPathRatio shows the split between direct gRPC and Kafka fallback delivery.
type DeliveryPathRatio struct {
	GrpcDirect    float64 `json:"grpc_direct"`
	KafkaFallback float64 `json:"kafka_fallback"`
}

// SimulationState describes the current load generator state.
type SimulationState struct {
	Active        bool    `json:"active"`
	Mode          string  `json:"mode"`
	QPS           float64 `json:"qps"`
	UptimeSeconds int64   `json:"uptime_seconds"`
}
