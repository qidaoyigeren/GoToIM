import type { Order, OrderItem, OrderStatus } from '@/types/order'
import type { Notification, NotifyType } from '@/types/notification'
import type { PlatformStats } from '@/types/message'
import type { OnlineStats } from '@/types/online'

// ---- In-memory mock database ----

const mockProducts = [
  { product_name: 'iPhone 16 Pro Max', price: 9999 },
  { product_name: 'MacBook Pro 14"', price: 14999 },
  { product_name: 'AirPods Pro 2', price: 1899 },
  { product_name: 'iPad Air M2', price: 4799 },
  { product_name: 'Apple Watch Ultra 2', price: 6499 },
  { product_name: 'Sony WH-1000XM5', price: 2499 },
  { product_name: 'Kindle Paperwhite', price: 1068 },
  { product_name: 'Nintendo Switch OLED', price: 2699 },
  { product_name: 'Dyson V15 Detect', price: 4990 },
  { product_name: 'DJI Mini 4 Pro', price: 6988 },
]

function randomPick<T>(arr: T[]): T {
  return arr[Math.floor(Math.random() * arr.length)]
}

function generateId(): string {
  return `ORD${Date.now()}${Math.random().toString(36).slice(2, 8).toUpperCase()}`
}

function generateNotifyId(): string {
  return `NTF${Date.now()}${Math.random().toString(36).slice(2, 8).toUpperCase()}`
}

let mockOrders: Order[] = []
let mockNotifications: Notification[] = []
let eventCounter = 0

// Seed initial data
function seedData() {
  if (mockOrders.length > 0) return

  const statuses: OrderStatus[] = ['created', 'paid', 'confirmed', 'shipped', 'delivered', 'cancelled', 'delivery_failed']
  const userIds = ['10001', '10002', '10003', '10004', '10005', '10006', '10007', '10008']

  for (let i = 0; i < 25; i++) {
    const status = randomPick(statuses)
    const itemCount = Math.floor(Math.random() * 3) + 1
    const items: OrderItem[] = []
    let total = 0
    for (let j = 0; j < itemCount; j++) {
      const product = randomPick(mockProducts)
      const qty = Math.floor(Math.random() * 2) + 1
      items.push({ ...product, quantity: qty })
      total += product.price * qty
    }

    const pastMinutes = Math.floor(Math.random() * 7200)
    const orderId = `ORD${String(i + 1).padStart(6, '0')}`
    const createdAt = new Date(Date.now() - pastMinutes * 1000).toISOString()
    const updatedAt = new Date(Date.now() - (pastMinutes - Math.floor(Math.random() * 1800)) * 1000).toISOString()

    mockOrders.push({
      order_id: orderId,
      user_id: randomPick(userIds),
      status,
      items,
      total: Math.round(total * 100) / 100,
      created_at: createdAt,
      updated_at: updatedAt,
    })

    // Generate corresponding notifications
    const notifyTypeMap: Record<string, NotifyType> = {
      created: 'order_status',
      paid: 'order_status',
      confirmed: 'order_status',
      shipped: 'logistics',
      delivered: 'order_status',
      cancelled: 'system',
      delivery_failed: 'system',
    }
    mockNotifications.push({
      notify_id: `NTF${String(i + 1).padStart(6, '0')}`,
      user_id: mockOrders[i].user_id,
      type: notifyTypeMap[status] || 'order_status',
      title: statusLabels[status] || '状态更新',
      content: `订单 ${orderId} ${statusLabels[status] || '状态更新'}`,
      order_id: orderId,
      created_at: updatedAt,
      status: Math.random() > 0.3 ? 'acked' : 'delivered',
    })
  }
}

const statusLabels: Record<string, string> = {
  created: '订单已创建',
  paid: '订单已支付',
  confirmed: '订单已确认',
  shipped: '订单已发货',
  delivered: '订单已签收',
  cancelled: '订单已取消',
  delivery_failed: '配送异常',
}

// ---- Mock API functions ----

export async function mockCreateOrder(
  userId: string,
  items: OrderItem[],
  total: number
): Promise<{ order: Order; notification: Notification }> {
  seedData()
  const orderId = generateId()
  const now = new Date().toISOString()
  const order: Order = {
    order_id: orderId,
    user_id: userId,
    status: 'created',
    items,
    total,
    created_at: now,
    updated_at: now,
  }
  mockOrders.unshift(order)

  const notif: Notification = {
    notify_id: generateNotifyId(),
    user_id: userId,
    type: 'order_status',
    title: '订单已创建',
    content: `订单 ${orderId} 已创建，等待支付`,
    order_id: orderId,
    created_at: now,
    status: 'delivered',
  }
  mockNotifications.unshift(notif)

  return { order, notification: notif }
}

export async function mockChangeOrderStatus(
  orderId: string,
  newStatus: OrderStatus,
  extra?: Record<string, string>
): Promise<{ order: Order; notification: Notification }> {
  seedData()
  const idx = mockOrders.findIndex((o) => o.order_id === orderId)
  if (idx === -1) throw new Error('Order not found')

  mockOrders[idx] = {
    ...mockOrders[idx],
    status: newStatus,
    updated_at: new Date().toISOString(),
  }

  const title = statusLabels[newStatus] || '状态更新'
  const notif: Notification = {
    notify_id: generateNotifyId(),
    user_id: mockOrders[idx].user_id,
    type: newStatus === 'delivered' ? 'order_status' : newStatus === 'shipped' ? 'logistics' : 'order_status',
    title,
    content: `订单 ${orderId} ${title}${extra?.reason ? `：${extra.reason}` : ''}`,
    order_id: orderId,
    created_at: new Date().toISOString(),
    status: 'delivered',
  }
  mockNotifications.unshift(notif)

  return { order: mockOrders[idx], notification: notif }
}

export async function mockGetOrder(orderId: string): Promise<Order> {
  seedData()
  const order = mockOrders.find((o) => o.order_id === orderId)
  if (!order) throw new Error('Order not found')
  return order
}

export async function mockGetUserOrders(_userId: string): Promise<Order[]> {
  seedData()
  return mockOrders.filter((o) => o.user_id === _userId)
}

export async function mockGetUserNotifications(_userId: string): Promise<Notification[]> {
  seedData()
  return mockNotifications.filter((n) => n.user_id === _userId)
}

export function mockPlatformStats(): PlatformStats {
  seedData()
  const total = mockOrders.length
  const delivered = mockOrders.filter((o) => o.status === 'delivered').length
  const paid = mockOrders.filter((o) => o.status === 'paid').length
  const shipped = mockOrders.filter((o) => o.status === 'shipped').length
  const failed = mockOrders.filter((o) => o.status === 'delivery_failed' || o.status === 'cancelled').length

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
    online_users: Math.floor(Math.random() * 1200) + 800,
    offline_pending: Math.floor(Math.random() * 50),
    simulation: {
      active: false,
      mode: '',
      qps: 0,
      uptime_seconds: 0,
    },
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

// ---- Mock realtime event generator (for WebSocket simulation) ----

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
  const types: MockRealtimeEvent['type'][] = ['push_sent', 'push_delivered', 'ack_received']
  const type = Math.random() > 0.9 ? 'push_failed' : randomPick(types)

  const details: Record<string, { title: string; detail: string }> = {
    push_sent: { title: '消息已发送', detail: `消息已路由至 Comet 节点 sh001，目标用户 ${order.user_id}` },
    push_delivered: { title: '消息已送达', detail: `通过 ${deliveryPath === 'grpc_direct' ? 'gRPC 直连' : 'Kafka 可靠通道'} 送达设备` },
    ack_received: { title: 'ACK 已确认', detail: `客户端已确认消息，seq=${eventCounter}，延迟 ${Math.floor(Math.random() * 30) + 2}ms` },
    push_failed: { title: '推送失败', detail: '目标设备离线，消息已写入离线队列等待补偿' },
    order_status_change: { title: '订单状态变更', detail: `订单 ${order.order_id} 状态更新为 ${order.status}` },
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

// ---- Mock simulation ----

let simInterval: ReturnType<typeof setInterval> | null = null
let simActive = false
let simStartTime = 0

export function startMockSimulation(): void {
  if (simActive) return
  simActive = true
  simStartTime = Date.now()
}

export function stopMockSimulation(): void {
  simActive = false
  if (simInterval) {
    clearInterval(simInterval)
    simInterval = null
  }
}

export function getMockSimulationStatus() {
  return {
    active: simActive,
    mode: simActive ? 'normal' : '',
    qps: simActive ? 100 : 0,
    uptime_seconds: simActive ? Math.floor((Date.now() - simStartTime) / 1000) : 0,
  }
}

// Export seed data for use by stores
export function getMockOrders(): Order[] {
  seedData()
  return mockOrders
}

export function getMockNotifications(): Notification[] {
  seedData()
  return mockNotifications
}
