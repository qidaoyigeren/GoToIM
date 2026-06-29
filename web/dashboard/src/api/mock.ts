import type { Order, OrderImportance, OrderItem, OrderStatus, OrderType } from '@/types/order'
import type { Notification, NotifyType } from '@/types/notification'
import type { PlatformStats } from '@/types/message'
import type { OnlineStats } from '@/types/online'

const demoMerchants = [
  { merchant_id: 'm_fast_supply', merchant_uid: 90001, name: '极速数码生活馆' },
  { merchant_id: 'm_office_plus', merchant_uid: 90002, name: '企业办公优选' },
  { merchant_id: 'm_service_center', merchant_uid: 90003, name: '售后服务中心' },
]

const demoProducts = [
  { product_id: 'p_phone_case', product_name: '透明防摔保护壳', price: 129 },
  { product_id: 'p_keyboard', product_name: '无线办公键盘', price: 329 },
  { product_id: 'p_monitor', product_name: '27 英寸办公显示器', price: 1599 },
  { product_id: 'p_headset', product_name: '降噪通话耳机', price: 699 },
  { product_id: 'p_support', product_name: '远程售后服务单', price: 99 },
  { product_id: 'p_virtual', product_name: '数字会员兑换码', price: 199 },
]

const statusLabels: Partial<Record<OrderStatus, string>> = {
  created: '订单已创建',
  confirmed: '商家已确认',
  shipped: '履约已发出',
  delivered: '订单已送达',
  cancelled: '订单已取消',
  delivery_failed: '履约异常',
}

const orderTypes: OrderType[] = ['normal', 'presale', 'urgent', 'enterprise', 'after_sale', 'virtual']
const importanceLevels: OrderImportance[] = ['normal', 'high', 'urgent', 'critical']
const mockOrders: Order[] = []
const mockNotifications: Notification[] = []
let eventCounter = 0
let simInterval: ReturnType<typeof setInterval> | null = null
let simActive = false
let simStartTime = 0
let simMode = ''
let simQps = 0

function randomPick<T>(arr: T[]): T {
  return arr[Math.floor(Math.random() * arr.length)]
}

function generateId(prefix: string): string {
  return `${prefix}${Date.now()}${Math.random().toString(36).slice(2, 8).toUpperCase()}`
}

function notificationFor(order: Order, status: OrderStatus): Notification {
  const title = statusLabels[status] || '订单状态更新'
  const notifyType: NotifyType = status === 'shipped' ? 'logistics' : status === 'created' ? 'purchase_order' : 'order_status'
  return {
    notify_id: generateId('NTF'),
    user_id: order.user_id,
    type: notifyType,
    business_type: 'purchase_order',
    event_type: status,
    title,
    content: `订单 ${order.order_id} ${title}，消息已进入 GoIM 投递链路。`,
    order_id: order.order_id,
    created_at: new Date().toISOString(),
    status: Math.random() > 0.25 ? 'acked' : 'delivered',
    priority: order.importance === 'critical' ? 'P0' : order.importance === 'urgent' ? 'P1' : order.importance === 'high' ? 'P2' : 'P3',
    ttl_seconds: order.importance === 'critical' ? 300 : order.importance === 'urgent' ? 900 : 3600,
    ack_policy: order.importance === 'normal' ? 'best_effort' : 'required',
    expected_ack_count: order.importance === 'normal' ? 0 : 1,
    acked_count: Math.random() > 0.25 ? 1 : 0,
  }
}

function buildOrder(seed: number, status: OrderStatus): Order {
  const merchant = randomPick(demoMerchants)
  const itemCount = Math.floor(Math.random() * 3) + 1
  const items: OrderItem[] = []
  let total = 0
  for (let i = 0; i < itemCount; i++) {
    const product = randomPick(demoProducts)
    const quantity = Math.floor(Math.random() * 2) + 1
    items.push({
      product_id: product.product_id,
      product_name: product.product_name,
      quantity,
      price: product.price,
    })
    total += product.price * quantity
  }

  const pastMinutes = Math.floor(Math.random() * 7200)
  const createdAt = new Date(Date.now() - pastMinutes * 1000).toISOString()
  const updatedAt = new Date(Date.now() - Math.max(0, pastMinutes - Math.floor(Math.random() * 1800)) * 1000).toISOString()

  return {
    order_id: `ORD${String(seed).padStart(6, '0')}`,
    user_id: randomPick(['10001', '10002', '10003', '10004', '10005', '10006', '10007', '10008']),
    merchant_id: merchant.merchant_id,
    merchant_uid: merchant.merchant_uid,
    merchant_name: merchant.name,
    status,
    order_type: randomPick(orderTypes),
    importance: randomPick(importanceLevels),
    buyer_note: '请关注消息送达和 ACK 状态',
    fulfillment_mode: status === 'delivered' ? '已完成履约' : '标准履约',
    support_room_id: `${merchant.merchant_id}_group`,
    private_conversation_id: `CHAT-${seed}`,
    items,
    total: Math.round(total * 100) / 100,
    created_at: createdAt,
    updated_at: updatedAt,
  }
}

function seedData() {
  if (mockOrders.length > 0) return
  const statuses: OrderStatus[] = ['created', 'confirmed', 'shipped', 'delivered', 'cancelled', 'delivery_failed']
  for (let i = 1; i <= 25; i++) {
    const order = buildOrder(i, randomPick(statuses))
    mockOrders.push(order)
    mockNotifications.push(notificationFor(order, order.status))
  }
}

export async function mockCreateOrder(
  userId: string,
  items: OrderItem[],
  total: number
): Promise<{ order: Order; notification: Notification }> {
  seedData()
  const merchant = demoMerchants[0]
  const now = new Date().toISOString()
  const order: Order = {
    order_id: generateId('ORD'),
    user_id: userId,
    merchant_id: merchant.merchant_id,
    merchant_uid: merchant.merchant_uid,
    merchant_name: merchant.name,
    status: 'created',
    order_type: 'urgent',
    importance: 'high',
    buyer_note: items.map((item) => item.product_name).join(', '),
    fulfillment_mode: '标准履约',
    support_room_id: `${merchant.merchant_id}_group`,
    private_conversation_id: generateId('CHAT'),
    items,
    total,
    created_at: now,
    updated_at: now,
  }
  mockOrders.unshift(order)

  const notification = {
    ...notificationFor(order, 'created'),
    status: 'delivered' as const,
    created_at: now,
  }
  mockNotifications.unshift(notification)

  return { order, notification }
}

export async function mockChangeOrderStatus(
  orderId: string,
  newStatus: OrderStatus,
  extra?: Record<string, string>
): Promise<{ order: Order; notification: Notification }> {
  seedData()
  const idx = mockOrders.findIndex((order) => order.order_id === orderId)
  if (idx === -1) throw new Error('Order not found')

  const updatedAt = new Date().toISOString()
  mockOrders[idx] = {
    ...mockOrders[idx],
    status: newStatus,
    fulfillment_mode: newStatus === 'delivered' ? '已完成履约' : mockOrders[idx].fulfillment_mode,
    updated_at: updatedAt,
  }

  const notification = notificationFor(mockOrders[idx], newStatus)
  notification.content = `订单 ${orderId} ${notification.title}${extra?.location ? `，位置：${extra.location}` : ''}${extra?.reason ? `，原因：${extra.reason}` : ''}。`
  notification.created_at = updatedAt
  notification.status = 'delivered'
  mockNotifications.unshift(notification)

  return { order: mockOrders[idx], notification }
}

export async function mockGetOrder(orderId: string): Promise<Order> {
  seedData()
  const order = mockOrders.find((item) => item.order_id === orderId)
  if (!order) throw new Error('Order not found')
  return order
}

export async function mockGetUserOrders(userId: string): Promise<Order[]> {
  seedData()
  return mockOrders.filter((order) => order.user_id === userId)
}

export async function mockGetUserNotifications(userId: string): Promise<Notification[]> {
  seedData()
  return mockNotifications.filter((notification) => notification.user_id === userId)
}

export function mockPlatformStats(): PlatformStats {
  seedData()
  const total = mockOrders.length
  return {
    push_rate_per_sec: Math.floor(Math.random() * 500) + 200,
    total_pushed: total * 3 + Math.floor(Math.random() * 100),
    ack_rate: 0.85 + Math.random() * 0.12,
    latency_p50_ms: Math.floor(Math.random() * 15) + 5,
    latency_p99_ms: Math.floor(Math.random() * 80) + 20,
    latency_max_ms: Math.floor(Math.random() * 300) + 100,
    active_connections: Math.floor(Math.random() * 500) + 300,
    delivery_path: {
      grpc_direct: 0.7 + Math.random() * 0.2,
      kafka_fallback: 0.08 + Math.random() * 0.12,
    },
    delivery_path_detail: {
      grpc_direct: 0.68,
      kafka_fallback: 0.12,
      offline_stored: 0.08,
      failed: 0.02,
      logic_push: 0.1,
      unknown: 0,
    },
    online_users: Math.floor(Math.random() * 1200) + 800,
    offline_pending: Math.floor(Math.random() * 50),
    simulation: getMockSimulationStatus(),
    retry_count: Math.floor(Math.random() * 30),
    dlq_count: Math.floor(Math.random() * 5),
    outbox_pending: Math.floor(Math.random() * 20),
    outbox_failed: Math.floor(Math.random() * 3),
    notifications_by_type: {
      purchase_order: 32,
      order_status: 86,
      logistics: 24,
      system: 6,
    },
    ack_policy_satisfied_rate: 0.9 + Math.random() * 0.08,
  }
}

export function mockOnlineStats(): OnlineStats {
  return {
    ip_count: Math.floor(Math.random() * 800) + 400,
    conn_count: Math.floor(Math.random() * 1500) + 500,
    user_count: Math.floor(Math.random() * 1200) + 800,
    offline_pending: Math.floor(Math.random() * 50),
    direct_pushed: Math.floor(Math.random() * 5000) + 20000,
    kafka_fallback: Math.floor(Math.random() * 800) + 2000,
  }
}

export type MockRealtimeEvent = {
  id: string
  type: 'push_sent' | 'push_delivered' | 'ack_received' | 'push_failed' | 'order_status_change'
  msg_id: string
  order_id: string
  title: string
  detail: string
  timestamp: number
  delivery_path: 'grpc_direct' | 'kafka_fallback'
}

export function generateRealtimeEvent(): MockRealtimeEvent {
  seedData()
  eventCounter++
  const order = randomPick(mockOrders)
  const deliveryPath = Math.random() > 0.15 ? 'grpc_direct' : 'kafka_fallback'
  const types: MockRealtimeEvent['type'][] = ['push_sent', 'push_delivered', 'ack_received', 'order_status_change']
  const type = Math.random() > 0.92 ? 'push_failed' : randomPick(types)
  const pathLabel = deliveryPath === 'grpc_direct' ? 'direct push' : 'Kafka fallback'

  const details: Record<MockRealtimeEvent['type'], { title: string; detail: string }> = {
    push_sent: { title: '消息已发送', detail: `Logic 已将订单消息路由至 Comet，目标用户 ${order.user_id}` },
    push_delivered: { title: '消息已送达', detail: `通过 ${pathLabel} 送达客户端，会话等待 ACK` },
    ack_received: { title: 'ACK 已确认', detail: `客户端已确认消息 seq=${eventCounter}，延迟 ${Math.floor(Math.random() * 30) + 2}ms` },
    push_failed: { title: '投递失败', detail: '目标连接离线，消息已进入离线补偿或等待重试' },
    order_status_change: { title: '订单状态更新', detail: `订单 ${order.order_id} 当前状态：${statusLabels[order.status] || order.status}` },
  }

  return {
    id: `evt_${Date.now()}_${eventCounter}`,
    type,
    msg_id: `msg_${Date.now()}_${eventCounter}`,
    order_id: order.order_id,
    title: details[type].title,
    detail: details[type].detail,
    timestamp: Date.now(),
    delivery_path: deliveryPath,
  }
}

export function startMockSimulation(mode = 'peak', qps = 100, _users = 0): void {
  if (simActive) return
  simActive = true
  simStartTime = Date.now()
  simMode = mode
  simQps = qps
}

export function stopMockSimulation(): void {
  simActive = false
  simMode = ''
  simQps = 0
  if (simInterval) {
    clearInterval(simInterval)
    simInterval = null
  }
}

export function getMockSimulationStatus() {
  return {
    active: simActive,
    mode: simActive ? simMode : '',
    qps: simActive ? simQps : 0,
    uptime_seconds: simActive ? Math.floor((Date.now() - simStartTime) / 1000) : 0,
  }
}

export function getMockOrders(): Order[] {
  seedData()
  return mockOrders
}

export function getMockNotifications(): Notification[] {
  seedData()
  return mockNotifications
}
