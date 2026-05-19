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
	ScenarioRunID     string     `json:"scenario_run_id,omitempty"`
	IdempotencyKey    string     `json:"idempotency_key,omitempty"`
	TraceID           string     `json:"trace_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// NotificationOutbox is the durable handoff from business writes to delivery.
type NotificationOutbox struct {
	OutboxID             string    `json:"outbox_id"`
	NotifyID             string    `json:"notify_id"`
	UserID               string    `json:"user_id"`
	OrderID              string    `json:"order_id,omitempty"`
	BusinessType         string    `json:"business_type"`
	EventType            string    `json:"event_type"`
	PayloadJSON          string    `json:"payload_json"`
	Priority             string    `json:"priority"`
	TTLSeconds           int64     `json:"ttl_seconds"`
	Status               string    `json:"status"`
	RetryCount           int64     `json:"retry_count"`
	NextRetryAt          time.Time `json:"next_retry_at,omitempty"`
	LockedBy             string    `json:"locked_by,omitempty"`
	LockedUntil          time.Time `json:"locked_until,omitempty"`
	LastError            string    `json:"last_error,omitempty"`
	ScenarioRunID        string    `json:"scenario_run_id,omitempty"`
	TraceID              string    `json:"trace_id,omitempty"`
	CompensationStrategy string    `json:"compensation_strategy,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// Campaign represents a notification campaign with lifecycle management.
type Campaign struct {
	CampaignID     string     `json:"campaign_id"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	BusinessType   string     `json:"business_type"`
	TargetCount    int64      `json:"target_count"`
	SentCount      int64      `json:"sent_count"`
	FailedCount    int64      `json:"failed_count"`
	Status         string     `json:"status"`
	RateLimit      int        `json:"rate_limit"`
	PausedAt       *time.Time `json:"paused_at,omitempty"`
	CancelledAt    *time.Time `json:"cancelled_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	IdempotencyKey string     `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CampaignStatus values.
const (
	CampaignDraft     = "draft"
	CampaignActive    = "active"
	CampaignPaused    = "paused"
	CampaignCompleted = "completed"
	CampaignCancelled = "cancelled"
)

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
	TraceID      string     `json:"trace_id,omitempty"`
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
	TraceID           string     `json:"trace_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// NotificationDLQ records terminal delivery failures that need operator action.
type NotificationDLQ struct {
	DLQID                string                     `json:"dlq_id"`
	NotifyID             string                     `json:"notify_id"`
	OutboxID             string                     `json:"outbox_id"`
	UserID               string                     `json:"user_id"`
	OrderID              string                     `json:"order_id,omitempty"`
	BusinessType         string                     `json:"business_type,omitempty"`
	Reason               string                     `json:"reason"`
	LastError            string                     `json:"last_error"`
	PayloadJSON          string                     `json:"payload_json"`
	RetryCount           int64                      `json:"retry_count"`
	CompensationStrategy string                     `json:"compensation_strategy,omitempty"`
	CreatedAt            time.Time                  `json:"created_at"`
	ResolvedAt           *time.Time                 `json:"resolved_at,omitempty"`
	ResolvedBy           string                     `json:"resolved_by,omitempty"`
	Resolution           string                     `json:"resolution,omitempty"`
	TraceID              string                     `json:"trace_id,omitempty"`
	LatestAudit          *NotificationRecoveryAudit `json:"latest_audit,omitempty"`
}

// BusinessRef points a notification back to the business object operators know.
type BusinessRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// AckPolicyStatus summarizes whether the business ACK contract is satisfied.
type AckPolicyStatus struct {
	Policy            string     `json:"policy"`
	Status            string     `json:"status"`
	ExpectedAckCount  int64      `json:"expected_ack_count"`
	AckedCount        int64      `json:"acked_count"`
	Satisfied         bool       `json:"satisfied"`
	PolicySatisfiedAt *time.Time `json:"policy_satisfied_at,omitempty"`
}

// NotificationTrace gives operators a full explanation for one notification.
type NotificationTrace struct {
	Notification    *Notification          `json:"notification"`
	TraceID         string                 `json:"trace_id"`
	BusinessRef     BusinessRef            `json:"business_ref"`
	Outbox          *NotificationOutbox    `json:"outbox,omitempty"`
	Attempts        []*NotificationAttempt `json:"attempts"`
	DeliveryPath    string                 `json:"delivery_path"`
	RetryCount      int64                  `json:"retry_count"`
	DLQ             *NotificationDLQ       `json:"dlq,omitempty"`
	ACKs            []*NotificationAck     `json:"acks"`
	ACKPolicyStatus AckPolicyStatus        `json:"ack_policy_status"`
}

// TimelineEvent is a business-oriented event for order operations.
type TimelineEvent struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	Label         string    `json:"label"`
	Detail        string    `json:"detail,omitempty"`
	NotifyID      string    `json:"notify_id,omitempty"`
	OrderID       string    `json:"order_id,omitempty"`
	Status        string    `json:"status,omitempty"`
	DeliveryPath  string    `json:"delivery_path,omitempty"`
	RetryCount    int64     `json:"retry_count,omitempty"`
	BusinessType  string    `json:"business_type,omitempty"`
	FailureReason string    `json:"failure_reason,omitempty"`
	TraceID       string    `json:"trace_id,omitempty"`
	OccurredAt    time.Time `json:"occurred_at"`
}

// OrderTimeline combines raw order records with a flattened operator timeline.
type OrderTimeline struct {
	Order         *Order                 `json:"order"`
	StatusEvents  []*OrderStatusEvent    `json:"status_events"`
	Notifications []*Notification        `json:"notifications"`
	Attempts      []*NotificationAttempt `json:"attempts"`
	ACKs          []*NotificationAck     `json:"acks"`
	DLQEvents     []*NotificationDLQ     `json:"dlq_events"`
	Timeline      []TimelineEvent        `json:"timeline"`
}

// RateBreakdown is used by SLA metrics grouped by business or path.
type RateBreakdown struct {
	Key         string  `json:"key"`
	Total       int64   `json:"total"`
	Successful  int64   `json:"successful"`
	SuccessRate float64 `json:"success_rate"`
}

// CountBreakdown ranks operational reasons by volume.
type CountBreakdown struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// RetryPressureBreakdown reports retry load by business type.
type RetryPressureBreakdown struct {
	BusinessType string  `json:"business_type"`
	Total        int64   `json:"total"`
	Retried      int64   `json:"retried"`
	RetryRate    float64 `json:"retry_rate"`
}

// BusinessSLAMetrics is the dashboard-facing operational SLA view.
type BusinessSLAMetrics struct {
	WindowSeconds               int64                    `json:"window_seconds"`
	Since                       time.Time                `json:"since"`
	Until                       time.Time                `json:"until"`
	TotalNotifications          int64                    `json:"total_notifications"`
	SuccessfulNotifications     int64                    `json:"successful_notifications"`
	NotificationSuccessRate     float64                  `json:"notification_success_rate"`
	ACKSatisfiedCount           int64                    `json:"ack_satisfied_count"`
	ACKSatisfactionRate         float64                  `json:"ack_satisfaction_rate"`
	DLQCount                    int64                    `json:"dlq_count"`
	DLQRate                     float64                  `json:"dlq_rate"`
	RetriedNotifications        int64                    `json:"retried_notifications"`
	RetryRate                   float64                  `json:"retry_rate"`
	DeliveryLatencyP95Ms        float64                  `json:"delivery_latency_p95_ms"`
	DeliveryLatencyP99Ms        float64                  `json:"delivery_latency_p99_ms"`
	ACKLatencyP95Ms             float64                  `json:"ack_latency_p95_ms"`
	ACKLatencyP99Ms             float64                  `json:"ack_latency_p99_ms"`
	SuccessByBusinessType       []RateBreakdown          `json:"success_by_business_type"`
	SuccessByDeliveryPath       []RateBreakdown          `json:"success_by_delivery_path"`
	FailureReasonRanking        []CountBreakdown         `json:"failure_reason_ranking"`
	DLQReasonRanking            []CountBreakdown         `json:"dlq_reason_ranking"`
	RetryPressureByBusinessType []RetryPressureBreakdown `json:"retry_pressure_by_business_type"`
}

// NotificationRecoveryAudit records an operator recovery action.
type NotificationRecoveryAudit struct {
	AuditID      string    `json:"audit_id"`
	Action       string    `json:"action"`
	Operator     string    `json:"operator"`
	DLQID        string    `json:"dlq_id"`
	NotifyID     string    `json:"notify_id"`
	OutboxID     string    `json:"outbox_id"`
	BusinessType string    `json:"business_type,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	Resolution   string    `json:"resolution,omitempty"`
	Note         string    `json:"note,omitempty"`
	BeforeStatus string    `json:"before_status,omitempty"`
	AfterStatus  string    `json:"after_status,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// RecoveryAuditFilter selects recovery audit records for operations views.
type RecoveryAuditFilter struct {
	Operator     string
	Action       string
	BusinessType string
	Since        time.Time
	Until        time.Time
	Limit        int
}

// DLQBulkFilter selects DLQ rows for bulk operator recovery.
type DLQBulkFilter struct {
	Reason           string `json:"reason,omitempty"`
	BusinessType     string `json:"business_type,omitempty"`
	OlderThanSeconds int64  `json:"older_than_seconds,omitempty"`
	Resolved         string `json:"resolved,omitempty"`
	Operator         string `json:"operator,omitempty"`
	Limit            int    `json:"limit,omitempty"`
}

// DLQBulkResult reports one recovery action result.
type DLQBulkResult struct {
	Matched  int64              `json:"matched"`
	Replayed int64              `json:"replayed,omitempty"`
	Resolved int64              `json:"resolved,omitempty"`
	Skipped  int64              `json:"skipped"`
	Items    []*NotificationDLQ `json:"items"`
}

const (
	ReplayRequestPending   = "pending"
	ReplayRequestApproved  = "approved"
	ReplayRequestRejected  = "rejected"
	ReplayRequestExecuted  = "executed"
	ReplayRequestCancelled = "cancelled"
)

// ReplayApprovalRequest is a lightweight approval gate for risky bulk recovery.
type ReplayApprovalRequest struct {
	RequestID       string         `json:"request_id"`
	Action          string         `json:"action"`
	Status          string         `json:"status"`
	Operator        string         `json:"operator"`
	Approver        string         `json:"approver,omitempty"`
	Filter          DLQBulkFilter  `json:"filter"`
	MatchedCount    int64          `json:"matched_count"`
	Threshold       int64          `json:"threshold"`
	Resolution      string         `json:"resolution,omitempty"`
	Note            string         `json:"note,omitempty"`
	ThrottlePerSec  int            `json:"throttle_per_sec,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DecidedAt       *time.Time     `json:"decided_at,omitempty"`
	ExecutedAt      *time.Time     `json:"executed_at,omitempty"`
	ExecutionResult *DLQBulkResult `json:"execution_result,omitempty"`
}

// CampaignAudience is an imported snapshot used for targeted campaigns.
type CampaignAudience struct {
	AudienceID  string            `json:"audience_id"`
	CampaignID  string            `json:"campaign_id"`
	Name        string            `json:"name"`
	Definition  map[string]string `json:"definition,omitempty"`
	TargetCount int64             `json:"target_count"`
	CreatedAt   time.Time         `json:"created_at"`
}

// CampaignAudienceBatch tracks batched outbox creation for an audience.
type CampaignAudienceBatch struct {
	BatchID      string    `json:"batch_id"`
	AudienceID   string    `json:"audience_id"`
	CampaignID   string    `json:"campaign_id"`
	Status       string    `json:"status"`
	StartOffset  int       `json:"start_offset"`
	EndOffset    int       `json:"end_offset"`
	TargetCount  int64     `json:"target_count"`
	SuccessCount int64     `json:"success_count"`
	FailedCount  int64     `json:"failed_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CampaignAudienceTarget is one user in an imported campaign audience snapshot.
type CampaignAudienceTarget struct {
	AudienceID string    `json:"audience_id"`
	CampaignID string    `json:"campaign_id"`
	UserID     string    `json:"user_id"`
	BatchID    string    `json:"batch_id,omitempty"`
	NotifyID   string    `json:"notify_id,omitempty"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// platformStats holds aggregate platform metrics.
type PlatformStats struct {
	PushRatePerSec         float64            `json:"push_rate_per_sec"`
	TotalPushed            int64              `json:"total_pushed"`
	AckRate                float64            `json:"ack_rate"`
	LatencyP50Ms           float64            `json:"latency_p50_ms"`
	LatencyP95Ms           float64            `json:"latency_p95_ms"`
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

// ErrorEntry summarizes one class of errors encountered during a scenario run.
type ErrorEntry struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int64  `json:"count"`
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
	P50LatencyMs           float64            `json:"p50_latency_ms"`
	P95LatencyMs           float64            `json:"p95_latency_ms"`
	P99LatencyMs           float64            `json:"p99_latency_ms"`
	MaxLatencyMs           float64            `json:"max_latency_ms"`
	ErrorCount             int64              `json:"error_count"`
	ErrorSummary           []ErrorEntry       `json:"error_summary,omitempty"`
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

// ScenarioReport is a comprehensive scenario run report for demo/export.
type ScenarioReport struct {
	ScenarioID      string                `json:"scenario_id"`
	ScenarioName    string                `json:"scenario_name"`
	Status          string                `json:"status"`
	StartedAt       time.Time             `json:"started_at"`
	EndedAt         *time.Time            `json:"ended_at,omitempty"`
	DurationSeconds float64               `json:"duration_seconds"`
	Summary         ScenarioSummary       `json:"summary"`
	Latency         ScenarioLatency       `json:"latency"`
	DeliveryPath    DeliveryPathBreakdown `json:"delivery_path"`
	ErrorSummary    []ErrorEntry          `json:"error_summary"`
	Timeline        []TimelineEntry       `json:"timeline"`
	Suggestions     []string              `json:"suggestions"`
}

// ScenarioSummary holds aggregate counts for a scenario report.
type ScenarioSummary struct {
	TotalNotifications int64   `json:"total_notifications"`
	SuccessCount       int64   `json:"success_count"`
	FailedCount        int64   `json:"failed_count"`
	DLQCount           int64   `json:"dlq_count"`
	DroppedCount       int64   `json:"dropped_count"`
	AckCount           int64   `json:"ack_count"`
	SuccessRate        float64 `json:"success_rate"`
	AckRate            float64 `json:"ack_rate"`
}

// ScenarioLatency holds latency percentiles for a scenario report.
type ScenarioLatency struct {
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

// DeliveryPathBreakdown shows delivery path counts.
type DeliveryPathBreakdown struct {
	Direct        int64 `json:"direct"`
	KafkaFallback int64 `json:"kafka_fallback"`
	Offline       int64 `json:"offline"`
}

// TimelineEntry is a time-stamped event for the report timeline.
type TimelineEntry struct {
	Time    time.Time `json:"time"`
	Phase   string    `json:"phase"`
	Message string    `json:"message"`
}
