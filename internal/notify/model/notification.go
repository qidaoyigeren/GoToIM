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
	NotifyID          string     `json:"notify_id"`
	UserID            string     `json:"user_id"`
	Type              NotifyType `json:"type"`
	BusinessType      string     `json:"business_type,omitempty"`
	EventType         string     `json:"event_type,omitempty"`
	Title             string     `json:"title"`
	Content           string     `json:"content"`
	OrderID           string     `json:"order_id,omitempty"`
	Status            string     `json:"status"`
	Priority          string     `json:"priority,omitempty"`
	TTLSeconds        int64      `json:"ttl_seconds,omitempty"`
	AckPolicy         string     `json:"ack_policy,omitempty"`
	ExpectedAckCount  int64      `json:"expected_ack_count,omitempty"`
	AckedCount        int64      `json:"acked_count,omitempty"`
	BusinessAckStatus string     `json:"business_ack_status,omitempty"`
	TargetDeviceIDs   []string   `json:"target_device_ids,omitempty"`
	PrimaryDeviceID   string     `json:"primary_device_id,omitempty"`
	IdempotencyKey    string     `json:"idempotency_key,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// NotificationOutbox is the durable handoff from business writes to delivery.
type NotificationOutbox struct {
	OutboxID     string    `json:"outbox_id"`
	NotifyID     string    `json:"notify_id"`
	UserID       string    `json:"user_id"`
	OrderID      string    `json:"order_id,omitempty"`
	BusinessType string    `json:"business_type"`
	EventType    string    `json:"event_type"`
	PayloadJSON  string    `json:"payload_json"`
	Priority     string    `json:"priority"`
	TTLSeconds   int64     `json:"ttl_seconds"`
	Status       string    `json:"status"`
	RetryCount   int64     `json:"retry_count"`
	NextRetryAt  time.Time `json:"next_retry_at,omitempty"`
	LockedBy     string    `json:"locked_by,omitempty"`
	LockedUntil  time.Time `json:"locked_until,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// NotificationAttempt records one delivery attempt for a notification.
type NotificationAttempt struct {
	AttemptID    string     `json:"attempt_id"`
	NotifyID     string     `json:"notify_id"`
	Channel      string     `json:"channel"`
	Target       string     `json:"target"`
	Status       string     `json:"status"`
	Path         string     `json:"path,omitempty"`
	TargetNode   string     `json:"target_node,omitempty"`
	ErrorCode    string     `json:"error_code,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	LatencyMs    float64    `json:"latency_ms,omitempty"`
	AttemptNo    int64      `json:"attempt_no,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// NotificationAck records a client ACK receipt.
type NotificationAck struct {
	AckID             string     `json:"ack_id"`
	NotifyID          string     `json:"notify_id"`
	UserID            string     `json:"user_id"`
	MsgID             string     `json:"msg_id,omitempty"`
	DeviceID          string     `json:"device_id,omitempty"`
	SessionID         string     `json:"session_id,omitempty"`
	AckKey            string     `json:"ack_key,omitempty"`
	LatencyMs         float64    `json:"latency_ms"`
	PolicySatisfiedAt *time.Time `json:"policy_satisfied_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// NotificationDLQ records terminal delivery failures that need operator action.
type NotificationDLQ struct {
	DLQID       string     `json:"dlq_id"`
	NotifyID    string     `json:"notify_id"`
	OutboxID    string     `json:"outbox_id"`
	UserID      string     `json:"user_id"`
	OrderID     string     `json:"order_id,omitempty"`
	Reason      string     `json:"reason"`
	LastError   string     `json:"last_error"`
	PayloadJSON string     `json:"payload_json"`
	RetryCount  int64      `json:"retry_count"`
	CreatedAt   time.Time  `json:"created_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy  string     `json:"resolved_by,omitempty"`
	Resolution  string     `json:"resolution,omitempty"`
}

// platformStats holds aggregate platform metrics.
type PlatformStats struct {
	PushRatePerSec         float64            `json:"push_rate_per_sec"`
	TotalPushed            int64              `json:"total_pushed"`
	AckRate                float64            `json:"ack_rate"`
	LatencyP50Ms           float64            `json:"latency_p50_ms"`
	LatencyP99Ms           float64            `json:"latency_p99_ms"`
	LatencyMaxMs           float64            `json:"latency_max_ms"`
	ActiveConns            int64              `json:"active_connections"`
	DeliveryPath           DeliveryPathRatio  `json:"delivery_path"`
	DeliveryPathDetail     DeliveryPathDetail `json:"delivery_path_detail"`
	OnlineUsers            int64              `json:"online_users"`
	OfflinePending         int64              `json:"offline_pending"`
	Simulation             SimulationState    `json:"simulation"`
	RetryCount             int64              `json:"retry_count"`
	DLQCount               int64              `json:"dlq_count"`
	OldestDLQAgeSeconds    int64              `json:"oldest_dlq_age_seconds"`
	OutboxPending          int64              `json:"outbox_pending"`
	OutboxFailed           int64              `json:"outbox_failed"`
	NotificationsByType    map[string]int64   `json:"notifications_by_type"`
	AckPolicySatisfiedRate float64            `json:"ack_policy_satisfied_rate"`
}

// DeliveryPathRatio shows the split between direct gRPC and Kafka fallback delivery.
type DeliveryPathRatio struct {
	GrpcDirect    float64 `json:"grpc_direct"`
	KafkaFallback float64 `json:"kafka_fallback"`
}

// DeliveryPathDetail keeps newer channels without changing the legacy shape.
type DeliveryPathDetail struct {
	GrpcDirect    float64 `json:"grpc_direct"`
	KafkaFallback float64 `json:"kafka_fallback"`
	OfflineStored float64 `json:"offline_stored"`
	Failed        float64 `json:"failed"`
	LogicPush     float64 `json:"logic_push"`
	Unknown       float64 `json:"unknown"`
}

// SimulationState describes the current load generator state.
type SimulationState struct {
	Active        bool    `json:"active"`
	Mode          string  `json:"mode"`
	QPS           float64 `json:"qps"`
	UptimeSeconds int64   `json:"uptime_seconds"`
}

// ScenarioRun tracks a load or demo scenario as a durable resource.
type ScenarioRun struct {
	RunID                  string             `json:"run_id"`
	Mode                   string             `json:"mode"`
	Status                 string             `json:"status"`
	QPS                    int                `json:"qps"`
	Users                  int                `json:"users"`
	GeneratedOrders        int64              `json:"generated_orders"`
	GeneratedNotifications int64              `json:"generated_notifications"`
	SentCount              int64              `json:"sent_count"`
	AckedCount             int64              `json:"acked_count"`
	FailedCount            int64              `json:"failed_count"`
	DLQCount               int64              `json:"dlq_count"`
	LatencyP95Ms           float64            `json:"latency_p95_ms"`
	LatencyP99Ms           float64            `json:"latency_p99_ms"`
	RecentEvents           []*ScenarioEvent   `json:"recent_events,omitempty"`
	DeliveryPathDetail     DeliveryPathDetail `json:"delivery_path_detail"`
	StartedAt              time.Time          `json:"started_at"`
	FinishedAt             *time.Time         `json:"finished_at,omitempty"`
	LastError              string             `json:"last_error,omitempty"`
}

// ScenarioEvent is an append-only event emitted by a scenario run.
type ScenarioEvent struct {
	EventID     string    `json:"event_id"`
	RunID       string    `json:"run_id"`
	Type        string    `json:"type"`
	PayloadJSON string    `json:"payload_json"`
	CreatedAt   time.Time `json:"created_at"`
}
