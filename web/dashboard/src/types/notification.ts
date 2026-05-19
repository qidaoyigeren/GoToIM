export type NotifyType = 'order_status' | 'flash_sale' | 'logistics' | 'system'

export type NotifyStatus = 'pending' | 'delivered' | 'acked' | 'failed'

export interface Notification {
  notify_id: string
  user_id: string
  type: NotifyType
  business_type?: string
  event_type?: string
  title: string
  content: string
  order_id?: string
  created_at: string
  updated_at?: string
  status: NotifyStatus
  priority?: string
  ttl_seconds?: number
  ack_policy?: string
  expected_ack_count?: number
  acked_count?: number
  business_ack_status?: string
  target_device_ids?: string[]
  primary_device_id?: string
  trace_id?: string
}

export interface NotificationAttempt {
  attempt_id: string
  notify_id: string
  channel: string
  target: string
  status: string
  path?: string
  target_node?: string
  error_code?: string
  error_message?: string
  latency_ms?: number
  attempt_no?: number
  trace_id?: string
  started_at: string
  finished_at?: string
}

export interface NotificationAck {
  ack_id: string
  notify_id: string
  user_id: string
  msg_id?: string
  device_id?: string
  session_id?: string
  ack_key?: string
  latency_ms: number
  policy_satisfied_at?: string
  trace_id?: string
  created_at: string
}

export interface NotificationOutbox {
  outbox_id: string
  notify_id: string
  user_id: string
  order_id?: string
  business_type: string
  event_type: string
  status: string
  retry_count: number
  next_retry_at?: string
  last_error?: string
  trace_id?: string
  created_at: string
  updated_at: string
}

export interface NotificationDLQ {
  dlq_id: string
  notify_id: string
  outbox_id: string
  user_id: string
  order_id?: string
  business_type?: string
  trace_id?: string
  reason: string
  last_error: string
  retry_count: number
  created_at: string
  resolved_at?: string
  resolved_by?: string
  resolution?: string
  latest_audit?: RecoveryAudit
}

export interface NotificationTrace {
  notification: Notification
  trace_id: string
  business_ref: { type: string; id: string }
  outbox?: NotificationOutbox
  attempts: NotificationAttempt[]
  delivery_path: string
  retry_count: number
  dlq?: NotificationDLQ
  acks: NotificationAck[]
  ack_policy_status: {
    policy: string
    status: string
    expected_ack_count: number
    acked_count: number
    satisfied: boolean
    policy_satisfied_at?: string
  }
}

export interface TimelineEvent {
  id: string
  type: string
  label: string
  detail?: string
  notify_id?: string
  order_id?: string
  status?: string
  delivery_path?: string
  retry_count?: number
  business_type?: string
  failure_reason?: string
  trace_id?: string
  occurred_at: string
}

export interface OrderTimeline {
  order: import('./order').Order
  status_events: Array<{
    event_id: string
    order_id: string
    from_status: string
    to_status: string
    extra?: Record<string, string>
    created_at: string
  }>
  notifications: Notification[]
  attempts: NotificationAttempt[]
  acks: NotificationAck[]
  dlq_events: NotificationDLQ[]
  timeline: TimelineEvent[]
}

export interface DLQBulkResult {
  matched: number
  replayed?: number
  resolved?: number
  skipped: number
  items: NotificationDLQ[]
}

export interface ReplayApprovalRequest {
  request_id: string
  action: 'replay' | 'resolve'
  status: 'pending' | 'approved' | 'rejected' | 'executed' | 'cancelled'
  operator: string
  approver?: string
  filter: DLQFilters
  matched_count: number
  threshold: number
  resolution?: string
  note?: string
  throttle_per_sec?: number
  created_at: string
  updated_at: string
  decided_at?: string
  executed_at?: string
  execution_result?: DLQBulkResult
}

export interface ReplayApprovalGate {
  approval_required: true
  request: ReplayApprovalRequest
}

export interface CampaignAudience {
  audience_id: string
  campaign_id: string
  name: string
  definition?: Record<string, string>
  target_count: number
  created_at: string
}

export interface CampaignAudienceBatch {
  batch_id: string
  audience_id: string
  campaign_id: string
  status: string
  start_offset: number
  end_offset: number
  target_count: number
  success_count: number
  failed_count: number
  created_at: string
  updated_at: string
}

export interface CampaignAudienceTarget {
  audience_id: string
  campaign_id: string
  user_id: string
  batch_id?: string
  notify_id?: string
  status: string
  created_at: string
  updated_at: string
}

export interface RecoveryAudit {
  audit_id: string
  action: string
  operator: string
  dlq_id: string
  notify_id: string
  outbox_id: string
  business_type?: string
  reason?: string
  resolution?: string
  note?: string
  before_status?: string
  after_status?: string
  created_at: string
}

export interface DLQFilters {
  resolved?: 'true' | 'false' | ''
  reason?: string
  business_type?: string
  older_than_seconds?: number
  operator?: string
  limit?: number
}

export const NOTIFY_TYPE_LABELS: Record<NotifyType, string> = {
  order_status: '订单状态',
  flash_sale: '闪购通知',
  logistics: '物流更新',
  system: '系统通知',
}
