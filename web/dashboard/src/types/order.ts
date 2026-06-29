export type OrderStatus =
  | 'created'
  | 'paid'
  | 'confirmed'
  | 'shipped'
  | 'delivered'
  | 'cancelled'
  | 'delivery_failed'

export type OrderType =
  | 'normal'
  | 'presale'
  | 'urgent'
  | 'enterprise'
  | 'after_sale'
  | 'virtual'

export type OrderImportance = 'normal' | 'high' | 'urgent' | 'critical'

export interface Merchant {
  merchant_id: string
  merchant_uid: number
  name: string
  description?: string
  logo_url?: string
  group_room_id?: string
  group_name?: string
  created_at: string
  updated_at: string
}

export interface Product {
  product_id: string
  merchant_id: string
  sku_id?: string
  name: string
  description?: string
  price: number
  image_url?: string
  fulfillment_mode?: string
  created_at: string
  updated_at: string
}

export interface MerchantGroup {
  group_id: string
  merchant_id: string
  merchant_uid: number
  room_id: string
  name: string
  description?: string
  member_count: number
  created_at: string
  updated_at: string
}

export interface OrderItem {
  product_id?: string
  sku_id?: string
  product_name: string
  quantity: number
  price: number
  image_url?: string
}

export interface Order {
  order_id: string
  user_id: string
  merchant_id?: string
  merchant_uid?: number
  merchant_name?: string
  status: OrderStatus
  order_type?: OrderType
  importance?: OrderImportance
  buyer_note?: string
  fulfillment_mode?: string
  support_room_id?: string
  private_conversation_id?: string
  items: OrderItem[]
  total: number
  created_at: string
  updated_at: string
}

export interface PurchaseOrderItemInput {
  product_id: string
  sku_id?: string
  quantity: number
}

export interface PurchaseOrderInput {
  user_id: string
  merchant_id: string
  merchant_uid?: number
  order_type?: OrderType
  importance?: OrderImportance
  buyer_note?: string
  fulfillment_mode?: string
  items: PurchaseOrderItemInput[]
}

export const ORDER_STATUS_LABELS: Record<OrderStatus, string> = {
  created: '已下单',
  paid: '历史兼容状态',
  confirmed: '商家已确认',
  shipped: '履约已发出',
  delivered: '订单已送达',
  cancelled: '订单已取消',
  delivery_failed: '履约异常',
}

export const ORDER_TYPE_LABELS: Record<OrderType, string> = {
  normal: '普通商品',
  presale: '预售',
  urgent: '加急',
  enterprise: '企业采购',
  after_sale: '售后服务',
  virtual: '虚拟商品',
}

export const IMPORTANCE_LABELS: Record<OrderImportance, string> = {
  normal: '普通',
  high: '重要',
  urgent: '紧急',
  critical: '关键',
}

export const ORDER_STATUS_SEQUENCE: OrderStatus[] = [
  'created',
  'confirmed',
  'shipped',
  'delivered',
]

export function getValidTransitions(current: OrderStatus): OrderStatus[] {
  const transitions: Record<OrderStatus, OrderStatus[]> = {
    created: ['confirmed', 'cancelled'],
    paid: ['confirmed', 'cancelled'],
    confirmed: ['shipped', 'cancelled'],
    shipped: ['delivered', 'delivery_failed'],
    delivered: [],
    cancelled: [],
    delivery_failed: [],
  }
  return transitions[current] || []
}
