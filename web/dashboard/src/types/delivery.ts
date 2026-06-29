import type { ChatConversation, ChatMessage } from './chat'
import type { NotificationDLQ, NotificationTrace, OrderTimeline } from './notification'
import type { Order } from './order'

export type DeliveryRecordKind = 'notification' | 'chat' | 'ack' | 'dlq' | 'retry'

export interface DeliveryMessage {
  id: string
  message_id: string
  record_kind: DeliveryRecordKind
  type: string
  order_id?: string
  sender?: string
  target?: string
  delivery_method?: string
  status: string
  priority?: string
  ttl_seconds?: number
  ack_policy?: string
  business_type?: string
  event_type?: string
  trace_id?: string
  retry_count?: number
  last_error?: string
  conversation_id?: string
  room_id?: string
  sender_uid?: number
  receiver_uid?: number
  sender_role?: string
  target_node?: string
  latency_ms?: number
  created_at: string
  updated_at?: string
}

export interface DeliveryAttemptView {
  attempt_id: string
  status: string
  path?: string
  target?: string
  target_node?: string
  error_message?: string
  latency_ms?: number
  started_at: string
  finished_at?: string
}

export interface DeliveryAckView {
  ack_id: string
  user_id: string
  msg_id?: string
  device_id?: string
  ack_key?: string
  latency_ms?: number
  created_at: string
}

export interface DeliveryMessageDetail {
  message: DeliveryMessage
  raw_payload?: unknown
  business_type?: string
  event_type?: string
  trace_id?: string
  delivery_path?: string
  attempts?: DeliveryAttemptView[]
  acks?: DeliveryAckView[]
  retry_count?: number
  dlq?: NotificationDLQ
  failure_reason?: string
  last_error?: string
  target_node?: string
  latency_ms?: number
  order?: Order
  notification_trace?: NotificationTrace
  order_timeline?: OrderTimeline
  chat_message?: ChatMessage
  conversation?: ChatConversation
}
