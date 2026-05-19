export type NotifyType = 'order_status' | 'flash_sale' | 'logistics' | 'system'

export type NotifyStatus = 'pending' | 'delivered' | 'acked' | 'failed'

export interface Notification {
  notify_id: string
  user_id: string
  type: NotifyType
  title: string
  content: string
  order_id?: string
  created_at: string
  status: NotifyStatus
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
  started_at: string
  finished_at?: string
}

export const NOTIFY_TYPE_LABELS: Record<NotifyType, string> = {
  order_status: '订单状态',
  flash_sale: '闪购通知',
  logistics: '物流更新',
  system: '系统通知',
}
