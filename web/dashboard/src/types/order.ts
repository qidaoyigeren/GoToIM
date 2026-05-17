export type OrderStatus =
  | 'created'
  | 'paid'
  | 'confirmed'
  | 'shipped'
  | 'delivered'
  | 'cancelled'
  | 'delivery_failed'

export interface OrderItem {
  product_name: string
  quantity: number
  price: number
}

export interface Order {
  order_id: string
  user_id: string
  status: OrderStatus
  items: OrderItem[]
  total: number
  created_at: string
  updated_at: string
}

export interface OrderStatusChange {
  order_id: string
  from_status: OrderStatus
  to_status: OrderStatus
  changed_at: string
  extra?: Record<string, string>
}

export const ORDER_STATUS_LABELS: Record<OrderStatus, string> = {
  created: '已创建',
  paid: '已支付',
  confirmed: '已确认',
  shipped: '运输中',
  delivered: '已送达',
  cancelled: '已取消',
  delivery_failed: '配送异常',
}

export const ORDER_STATUS_SEQUENCE: OrderStatus[] = [
  'created',
  'paid',
  'confirmed',
  'shipped',
  'delivered',
]

export function getValidTransitions(current: OrderStatus): OrderStatus[] {
  const transitions: Record<OrderStatus, OrderStatus[]> = {
    created: ['paid', 'cancelled'],
    paid: ['confirmed', 'cancelled'],
    confirmed: ['shipped', 'cancelled'],
    shipped: ['delivered', 'delivery_failed'],
    delivered: [],
    cancelled: [],
    delivery_failed: [],
  }
  return transitions[current] || []
}
