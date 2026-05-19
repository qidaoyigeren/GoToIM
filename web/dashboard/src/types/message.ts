export interface PushMessage {
  msg_id: string
  from_uid: number
  to_uid: number
  timestamp: number
  seq: number
  content: string // JSON string
}

export interface PushMessageParsed {
  msg_id: string
  from_uid: number
  to_uid: number
  timestamp: number
  seq: number
  type: string
  title: string
  content: string
  notify_id: string
  order_id: string
}

export interface AckBody {
  msg_id: string
  seq: number
}

export interface SyncRequestBody {
  last_seq: number
  limit: number
}

export interface SyncReplyBody {
  current_seq: number
  has_more: boolean
  messages: PushMessage[]
}

export interface DeliveryPath {
  grpc_direct: number
  kafka_fallback: number
}

export interface DeliveryPathDetail extends DeliveryPath {
  offline_stored: number
  failed: number
  logic_push: number
  unknown: number
}

export interface PlatformStats {
  push_rate_per_sec: number
  total_pushed: number
  ack_rate: number
  latency_p50_ms: number
  latency_p95_ms?: number
  latency_p99_ms: number
  latency_max_ms: number
  active_connections: number
  delivery_path: DeliveryPath
  delivery_path_detail?: DeliveryPathDetail
  online_users: number
  offline_pending: number
  simulation: SimulationState
  retry_count?: number
  dlq_count?: number
  oldest_dlq_age_seconds?: number
  outbox_pending?: number
  outbox_failed?: number
  notifications_by_type?: Record<string, number>
  ack_policy_satisfied_rate?: number
}

export interface SimulationState {
  active: boolean
  mode: string
  qps: number
  uptime_seconds: number
}

export interface RealtimeEvent {
  id: string
  type: 'push_sent' | 'push_delivered' | 'ack_received' | 'push_failed' | 'order_status_change'
  msg_id?: string
  order_id?: string
  title: string
  detail: string
  timestamp: number
  delivery_path?: 'grpc_direct' | 'kafka_fallback'
}
