import { isDemoMode } from '@/config'
import { notifyClient } from './client'
import type { ApiResponse } from '@/types/api'
import type { DeliveryMessage, DeliveryMessageDetail } from '@/types/delivery'
import type { Notification } from '@/types/notification'
import type { Order } from '@/types/order'

type DeliveryResponse<T> = ApiResponse<T>

export async function listDeliveryMessages(limit = 200): Promise<DeliveryMessage[]> {
  if (isDemoMode()) return demoDeliveryMessages(limit)
  const res = await notifyClient.request<DeliveryResponse<DeliveryMessage[]>>('/delivery/messages', {
    params: { limit },
  })
  return res.data ?? []
}

export async function getDeliveryMessage(id: string): Promise<DeliveryMessageDetail> {
  if (isDemoMode()) return demoDeliveryMessage(id)
  const res = await notifyClient.request<DeliveryResponse<DeliveryMessageDetail>>(
    `/delivery/messages/${encodeURIComponent(id)}`
  )
  return res.data
}

async function demoDeliveryMessages(limit: number): Promise<DeliveryMessage[]> {
  const [{ getMockNotifications, getMockOrders }, { getMockChatSnapshot }] = await Promise.all([
    import('./mock'),
    import('./chat'),
  ])
  const notifications = getMockNotifications()
  const orders = getMockOrders()
  const chat = getMockChatSnapshot()

  const rows: DeliveryMessage[] = []
  for (const n of notifications) {
    rows.push(notificationRow(n))
    if (n.status === 'acked') rows.push(ackRow(n))
    if (n.status === 'failed') rows.push(dlqRow(n))
  }
  for (const message of chat.messages) {
    const conv = chat.conversations.find((item) => item.conversation_id === message.conversation_id)
    rows.push({
      id: `chat:${message.message_id}`,
      message_id: message.message_id,
      record_kind: 'chat',
      type: conv?.type === 'group' ? '群聊' : '私聊',
      order_id: message.order_id,
      sender: `UID ${message.sender_uid}`,
      target: conv?.type === 'group' ? conv.room_id : `UID ${message.receiver_uid}`,
      delivery_method: conv?.type === 'group' ? 'room push' : 'direct push',
      status: message.status,
      priority: 'normal',
      ack_policy: conv?.type === 'group' ? 'room fanout' : 'peer read',
      conversation_id: message.conversation_id,
      room_id: conv?.room_id,
      sender_uid: message.sender_uid,
      receiver_uid: message.receiver_uid,
      sender_role: message.sender_role,
      created_at: message.created_at,
      updated_at: message.read_at || message.delivered_at || message.created_at,
    })
  }

  for (const order of orders.slice(0, 3)) {
    rows.push({
      id: `retry:${order.order_id}`,
      message_id: `retry-${order.order_id}`,
      record_kind: 'retry',
      type: '重试',
      order_id: order.order_id,
      sender: 'notify outbox',
      target: `UID ${order.user_id}`,
      delivery_method: 'kafka fallback',
      status: 'pending',
      priority: order.importance === 'critical' ? 'critical' : 'normal',
      ttl_seconds: 900,
      ack_policy: 'any_device',
      retry_count: 1,
      created_at: order.created_at,
      updated_at: order.updated_at,
    })
  }

  return rows
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
    .slice(0, limit)
}

async function demoDeliveryMessage(id: string): Promise<DeliveryMessageDetail> {
  const rows = await demoDeliveryMessages(300)
  const message = rows.find((item) => item.id === id) ?? rows[0]
  if (!message) throw new Error('delivery message not found')

  const [{ getMockOrders, getMockNotifications }, { getMockChatSnapshot }] = await Promise.all([
    import('./mock'),
    import('./chat'),
  ])
  const order = getMockOrders().find((item) => item.order_id === message.order_id)
  const notification = getMockNotifications().find((item) => item.notify_id === message.message_id)
  const chat = getMockChatSnapshot()
  const chatMessage = chat.messages.find((item) => item.message_id === message.message_id)
  const conversation = chat.conversations.find((item) => item.conversation_id === message.conversation_id)

  const createdAt = message.created_at
  const deliveredAt = message.updated_at || createdAt
  const failure = message.status === 'failed' || message.record_kind === 'dlq'
  return {
    message,
    raw_payload: notification
      ? {
          type: notification.type,
          notify_id: notification.notify_id,
          order_id: notification.order_id,
          title: notification.title,
          content: notification.content,
          trace_id: notification.trace_id || `trace-${notification.notify_id}`,
        }
      : chatMessage
        ? {
            type: conversation?.type === 'group' ? 'group_chat_message' : 'chat_message',
            message_id: chatMessage.message_id,
            conversation_id: chatMessage.conversation_id,
            room_type: conversation?.type,
            merchant_id: conversation?.merchant_id,
            room_id: conversation?.room_id,
            sender_uid: chatMessage.sender_uid,
            receiver_uid: chatMessage.receiver_uid,
            delivery_path: chatMessage.delivery_path,
            body: chatMessage.body,
          }
        : { id: message.id, status: message.status },
    business_type: message.business_type || (message.record_kind === 'chat' ? 'chat' : 'purchase_order'),
    event_type: message.event_type || message.type,
    trace_id: message.trace_id || `trace-${message.message_id}`,
    delivery_path: message.delivery_method,
    attempts: [
      {
        attempt_id: `attempt-${message.message_id}`,
        status: failure ? 'failed' : 'success',
        path: message.delivery_method,
        target: message.target,
        target_node: message.target_node || 'demo-node-1',
        error_message: failure ? message.last_error || '模拟投递失败' : '',
        latency_ms: message.latency_ms ?? 18,
        started_at: createdAt,
        finished_at: deliveredAt,
      },
    ],
    acks: message.record_kind === 'chat'
      ? []
      : [
          {
            ack_id: `ack-${message.message_id}`,
            user_id: message.target?.replace('UID ', '') || '10001',
            msg_id: message.message_id,
            ack_key: 'web-dashboard',
            latency_ms: 32,
            created_at: deliveredAt,
          },
        ],
    retry_count: message.retry_count || 0,
    dlq: message.record_kind === 'dlq' ? {
      dlq_id: message.id,
      notify_id: message.message_id,
      outbox_id: `outbox-${message.message_id}`,
      user_id: message.target?.replace('UID ', '') || '',
      order_id: message.order_id,
      business_type: message.business_type,
      reason: 'demo_failure',
      last_error: message.last_error || '模拟 DLQ 记录',
      payload_json: '{}',
      retry_count: message.retry_count || 3,
      created_at: createdAt,
    } : undefined,
    failure_reason: failure ? message.last_error || '模拟投递失败' : '暂无失败原因',
    last_error: failure ? message.last_error || '模拟投递失败' : '',
    target_node: message.target_node || 'demo-node-1',
    latency_ms: message.latency_ms ?? 18,
    order,
    chat_message: chatMessage,
    conversation,
  }
}

function notificationRow(n: Notification): DeliveryMessage {
  return {
    id: `notification:${n.notify_id}`,
    message_id: n.notify_id,
    record_kind: 'notification',
    type: n.event_type === 'created_merchant' ? '商家新订单通知' : '购买订单通知',
    order_id: n.order_id,
    sender: 'notify-server',
    target: `UID ${n.user_id}`,
    delivery_method: 'direct push',
    status: n.status,
    priority: n.priority || 'normal',
    ttl_seconds: n.ttl_seconds || 3600,
    ack_policy: n.ack_policy || 'best_effort',
    business_type: n.business_type || 'purchase_order',
    event_type: n.event_type || n.type,
    trace_id: n.trace_id || `trace-${n.notify_id}`,
    created_at: n.created_at,
    updated_at: n.updated_at || n.created_at,
  }
}

function ackRow(n: Notification): DeliveryMessage {
  return {
    id: `ack:${n.notify_id}`,
    message_id: `ack-${n.notify_id}`,
    record_kind: 'ack',
    type: 'ACK',
    order_id: n.order_id,
    sender: `UID ${n.user_id}`,
    target: 'notify-server',
    delivery_method: 'ACK',
    status: 'acked',
    priority: n.priority || 'normal',
    ttl_seconds: n.ttl_seconds,
    ack_policy: n.ack_policy,
    business_type: n.business_type,
    event_type: 'client_ack',
    trace_id: n.trace_id || `trace-${n.notify_id}`,
    created_at: n.updated_at || n.created_at,
    updated_at: n.updated_at || n.created_at,
  }
}

function dlqRow(n: Notification): DeliveryMessage {
  return {
    id: `dlq:${n.notify_id}`,
    message_id: `dlq-${n.notify_id}`,
    record_kind: 'dlq',
    type: 'DLQ',
    order_id: n.order_id,
    sender: 'notify outbox',
    target: `UID ${n.user_id}`,
    delivery_method: 'kafka fallback',
    status: 'dlq',
    priority: n.priority || 'normal',
    ttl_seconds: n.ttl_seconds,
    ack_policy: n.ack_policy,
    business_type: n.business_type,
    event_type: n.event_type,
    trace_id: n.trace_id || `trace-${n.notify_id}`,
    retry_count: 3,
    last_error: '模拟多次投递失败后进入 DLQ',
    created_at: n.updated_at || n.created_at,
    updated_at: n.updated_at || n.created_at,
  }
}
