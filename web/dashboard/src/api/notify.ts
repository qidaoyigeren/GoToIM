import { isDemoMode } from '@/config'
import { notifyClient } from './client'
import type { Order, OrderStatus } from '@/types/order'
import type { Notification } from '@/types/notification'
import type { PlatformStats, SimulationState } from '@/types/message'
import type { ApiResponse } from '@/types/api'

type NotifyResponse<T> = ApiResponse<T>

export async function createOrder(
  userId: string,
  items: { product_name: string; quantity: number; price: number }[],
  total: number
): Promise<{ order: Order; notification: Notification }> {
  if (isDemoMode()) {
    const { mockCreateOrder } = await import('./mock')
    return mockCreateOrder(userId, items, total)
  }
  const res = await notifyClient.request<NotifyResponse<{ order: Order; notification: Notification }>>(
    '/order/create',
    { method: 'POST', body: { user_id: userId, items, total } }
  )
  return res.data
}

export async function changeOrderStatus(
  orderId: string,
  newStatus: OrderStatus,
  extra?: Record<string, string>
): Promise<{ order: Order; notification: Notification }> {
  if (isDemoMode()) {
    const { mockChangeOrderStatus } = await import('./mock')
    return mockChangeOrderStatus(orderId, newStatus, extra)
  }
  const res = await notifyClient.request<NotifyResponse<{ order: Order; notification: Notification }>>(
    '/order/status-change',
    { method: 'POST', body: { order_id: orderId, new_status: newStatus, extra } }
  )
  return res.data
}

export async function getOrder(orderId: string): Promise<Order> {
  if (isDemoMode()) {
    const { mockGetOrder } = await import('./mock')
    return mockGetOrder(orderId)
  }
  const res = await notifyClient.request<NotifyResponse<Order>>(`/orders/${orderId}`)
  return res.data
}

export async function getUserOrders(userId: string): Promise<Order[]> {
  if (isDemoMode()) {
    const { mockGetUserOrders } = await import('./mock')
    return mockGetUserOrders(userId)
  }
  const res = await notifyClient.request<NotifyResponse<Order[]>>(`/orders/user/${userId}`)
  return res.data
}

export async function getUserNotifications(userId: string): Promise<Notification[]> {
  if (isDemoMode()) {
    const { mockGetUserNotifications } = await import('./mock')
    return mockGetUserNotifications(userId)
  }
  const res = await notifyClient.request<NotifyResponse<Notification[]>>(`/user/${userId}/notifications`)
  return res.data
}

export async function getPlatformStats(): Promise<PlatformStats> {
  if (isDemoMode()) {
    const { mockPlatformStats } = await import('./mock')
    return mockPlatformStats()
  }
  const res = await notifyClient.request<NotifyResponse<PlatformStats>>('/platform/stats')
  return res.data
}

export async function sendAck(notifyId: string): Promise<boolean> {
  if (isDemoMode()) {
    return true
  }
  const res = await notifyClient.request<NotifyResponse<{ recorded: boolean }>>('/ack', {
    method: 'POST',
    body: { notify_id: notifyId },
  })
  return res.data.recorded
}

export async function startSimulation(mode: string, qps: number, users: number): Promise<void> {
  if (isDemoMode()) {
    return
  }
  await notifyClient.request('/simulate/start', {
    method: 'POST',
    body: { mode, qps, users },
  })
}

export async function stopSimulation(): Promise<void> {
  if (isDemoMode()) {
    return
  }
  await notifyClient.request('/simulate/stop', { method: 'POST' })
}

export async function getSimulationStatus(): Promise<SimulationState> {
  if (isDemoMode()) {
    return { active: false, mode: '', qps: 0, uptime_seconds: 0 }
  }
  const res = await notifyClient.request<NotifyResponse<SimulationState>>('/simulate/status')
  return res.data
}

export async function createFlashSale(
  title: string,
  desc: string,
  targetUserIds: string[]
): Promise<void> {
  if (isDemoMode()) {
    return
  }
  await notifyClient.request('/flash-sale/notify', {
    method: 'POST',
    body: { title, description: desc, target_uids: targetUserIds },
  })
}

export async function sendLogisticsUpdate(
  orderId: string,
  location: string,
  description: string
): Promise<void> {
  if (isDemoMode()) {
    return
  }
  await notifyClient.request('/logistics/update', {
    method: 'POST',
    body: { order_id: orderId, location, description },
  })
}
