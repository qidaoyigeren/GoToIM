import { create } from 'zustand'
import type { Order, OrderStatus } from '@/types/order'

type OrderStoreState = {
  orders: Order[]
  selectedOrderId: string | null
  setOrders: (orders: Order[]) => void
  addOrder: (order: Order) => void
  updateOrderStatus: (orderId: string, newStatus: OrderStatus, updatedAt: string) => void
  selectOrder: (id: string | null) => void
}

export const useOrderStore = create<OrderStoreState>((set) => ({
  orders: [],
  selectedOrderId: null,
  setOrders: (orders) => set({ orders: Array.isArray(orders) ? orders : [] }),
  addOrder: (order) => set((s) => ({ orders: [order, ...s.orders] })),
  updateOrderStatus: (orderId, newStatus, updatedAt) =>
    set((s) => ({
      orders: s.orders.map((o) =>
        o.order_id === orderId ? { ...o, status: newStatus, updated_at: updatedAt } : o
      ),
    })),
  selectOrder: (id) => set({ selectedOrderId: id }),
}))
