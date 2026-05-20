import { isDemoMode } from '@/config'
import { notifyClient } from './client'
import type { Order, OrderStatus } from '@/types/order'
import type { CampaignAudience, CampaignAudienceBatch, CampaignAudienceTarget, DLQBulkResult, DLQFilters, Notification, NotificationAttempt, NotificationDLQ, NotificationTrace, OrderTimeline, RecoveryAudit, ReplayApprovalGate, ReplayApprovalRequest } from '@/types/notification'
import type { BusinessSLA, PlatformStats, SimulationState } from '@/types/message'
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

export async function getBusinessSLA(window = '24h'): Promise<BusinessSLA> {
  if (isDemoMode()) {
    return {
      window_seconds: 86400,
      since: new Date(Date.now() - 86400_000).toISOString(),
      until: new Date().toISOString(),
      total_notifications: 0,
      successful_notifications: 0,
      notification_success_rate: 0,
      ack_satisfied_count: 0,
      ack_satisfaction_rate: 0,
      dlq_count: 0,
      dlq_rate: 0,
      retried_notifications: 0,
      retry_rate: 0,
      delivery_latency_p95_ms: 0,
      delivery_latency_p99_ms: 0,
      ack_latency_p95_ms: 0,
      ack_latency_p99_ms: 0,
      success_by_business_type: [],
      success_by_delivery_path: [],
      failure_reason_ranking: [],
      dlq_reason_ranking: [],
      retry_pressure_by_business_type: [],
    }
  }
  const res = await notifyClient.request<NotifyResponse<BusinessSLA>>(`/platform/sla?window=${encodeURIComponent(window)}`)
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
  targetUserIds: string[],
  audienceId?: string
): Promise<void> {
  if (isDemoMode()) {
    return
  }
  await notifyClient.request('/flash-sale/notify', {
    method: 'POST',
    body: { title, description: desc, target_uids: targetUserIds, audience_id: audienceId },
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

export async function getNotificationAttempts(notifyId: string): Promise<NotificationAttempt[]> {
  if (isDemoMode()) {
    return []
  }
  const res = await notifyClient.request<NotifyResponse<NotificationAttempt[]>>(
    `/notifications/${notifyId}/attempts`
  )
  return res.data
}

export async function getNotificationTrace(notifyId: string): Promise<NotificationTrace> {
  const res = await notifyClient.request<NotifyResponse<NotificationTrace>>(
    `/notifications/${notifyId}/trace`
  )
  return normalizeNotificationTrace(res.data, notifyId)
}

export async function getOrderTimeline(orderId: string): Promise<OrderTimeline> {
  const res = await notifyClient.request<NotifyResponse<OrderTimeline>>(
    `/orders/${orderId}/timeline`
  )
  return normalizeOrderTimeline(res.data)
}

function normalizeNotificationTrace(trace: NotificationTrace | null | undefined, notifyId: string): NotificationTrace {
  if (!trace) {
    return {
      notification: {
        notify_id: notifyId,
        user_id: '',
        type: 'system',
        title: 'Notification trace',
        content: '',
        created_at: new Date().toISOString(),
        status: 'pending',
      },
      trace_id: notifyId,
      business_ref: { type: 'notification', id: notifyId },
      attempts: [],
      delivery_path: 'pending',
      retry_count: 0,
      acks: [],
      ack_policy_status: {
        policy: 'none',
        status: 'pending',
        expected_ack_count: 0,
        acked_count: 0,
        satisfied: false,
      },
    }
  }

  return {
    ...trace,
    attempts: Array.isArray(trace.attempts) ? trace.attempts : [],
    acks: Array.isArray(trace.acks) ? trace.acks : [],
    ack_policy_status: trace.ack_policy_status ?? {
      policy: 'none',
      status: 'pending',
      expected_ack_count: 0,
      acked_count: 0,
      satisfied: false,
    },
  }
}

function normalizeOrderTimeline(timeline: OrderTimeline): OrderTimeline {
  return {
    ...timeline,
    status_events: Array.isArray(timeline.status_events) ? timeline.status_events : [],
    notifications: Array.isArray(timeline.notifications) ? timeline.notifications : [],
    attempts: Array.isArray(timeline.attempts) ? timeline.attempts : [],
    acks: Array.isArray(timeline.acks) ? timeline.acks : [],
    dlq_events: Array.isArray(timeline.dlq_events) ? timeline.dlq_events : [],
    timeline: Array.isArray(timeline.timeline) ? timeline.timeline : [],
  }
}

export async function listDLQ(filters: DLQFilters = {}): Promise<NotificationDLQ[]> {
  const params = new URLSearchParams()
  Object.entries(filters).forEach(([key, value]) => {
    if (value !== undefined && value !== '') params.set(key, String(value))
  })
  const suffix = params.toString() ? `?${params.toString()}` : ''
  const res = await notifyClient.request<NotifyResponse<NotificationDLQ[]>>(`/dlq${suffix}`)
  return res.data
}

export async function replayDLQ(dlqId: string, note?: string, operator = 'dashboard'): Promise<NotificationDLQ> {
  const res = await notifyClient.request<NotifyResponse<{ replayed: boolean; item: NotificationDLQ }>>(
    `/dlq/${dlqId}/replay`,
    { method: 'POST', body: { operator, note } }
  )
  return res.data.item
}

export async function resolveDLQ(dlqId: string, resolution = 'resolved', note?: string, operator = 'dashboard'): Promise<NotificationDLQ> {
  const res = await notifyClient.request<NotifyResponse<{ resolved: boolean; item: NotificationDLQ }>>(
    `/dlq/${dlqId}/resolve`,
    { method: 'POST', body: { operator, resolution, note } }
  )
  return res.data.item
}

export async function bulkReplayDLQ(filter: DLQFilters, note?: string, operator = 'dashboard'): Promise<DLQBulkResult | ReplayApprovalGate> {
  const res = await notifyClient.request<NotifyResponse<DLQBulkResult | ReplayApprovalGate>>('/dlq/bulk/replay', {
    method: 'POST',
    body: { ...filter, operator, note },
  })
  return res.data
}

export async function bulkResolveDLQ(filter: DLQFilters, resolution = 'resolved', note?: string, operator = 'dashboard'): Promise<DLQBulkResult | ReplayApprovalGate> {
  const res = await notifyClient.request<NotifyResponse<DLQBulkResult | ReplayApprovalGate>>('/dlq/bulk/resolve', {
    method: 'POST',
    body: { ...filter, operator, resolution, note },
  })
  return res.data
}

export async function getDLQAudits(dlqId: string): Promise<RecoveryAudit[]> {
  const res = await notifyClient.request<NotifyResponse<RecoveryAudit[]>>(`/dlq/${dlqId}/audits`)
  return res.data
}

export async function listRecoveryAudits(filters: {
  operator?: string
  action?: string
  business_type?: string
  since?: string
  until?: string
  limit?: number
} = {}): Promise<RecoveryAudit[]> {
  const params = new URLSearchParams()
  Object.entries(filters).forEach(([key, value]) => {
    if (value !== undefined && value !== '') params.set(key, String(value))
  })
  const suffix = params.toString() ? `?${params.toString()}` : ''
  const res = await notifyClient.request<NotifyResponse<RecoveryAudit[]>>(`/recovery/audits${suffix}`)
  return res.data
}

export async function createReplayRequest(body: {
  action: 'replay' | 'resolve'
  filter: DLQFilters
  operator?: string
  resolution?: string
  note?: string
  throttle_per_sec?: number
}): Promise<ReplayApprovalRequest> {
  const res = await notifyClient.request<NotifyResponse<ReplayApprovalRequest>>('/recovery/replay-requests', {
    method: 'POST',
    body,
  })
  return res.data
}

export async function listReplayRequests(status?: string): Promise<ReplayApprovalRequest[]> {
  const suffix = status ? `?status=${encodeURIComponent(status)}` : ''
  const res = await notifyClient.request<NotifyResponse<ReplayApprovalRequest[]>>(`/recovery/replay-requests${suffix}`)
  return res.data
}

export async function approveReplayRequest(id: string, operator = 'dashboard', note?: string): Promise<ReplayApprovalRequest> {
  const res = await notifyClient.request<NotifyResponse<ReplayApprovalRequest>>(`/recovery/replay-requests/${id}/approve`, {
    method: 'PATCH',
    body: { operator, note },
  })
  return res.data
}

export async function executeReplayRequest(id: string, operator = 'dashboard'): Promise<ReplayApprovalRequest> {
  const res = await notifyClient.request<NotifyResponse<ReplayApprovalRequest>>(`/recovery/replay-requests/${id}/execute`, {
    method: 'POST',
    body: { operator },
  })
  return res.data
}

export async function importCampaignAudience(campaignId: string, body: {
  name?: string
  definition?: Record<string, string>
  target_uids: string[]
  batch_size?: number
}): Promise<{ audience: CampaignAudience; batches: CampaignAudienceBatch[] }> {
  const res = await notifyClient.request<NotifyResponse<{ audience: CampaignAudience; batches: CampaignAudienceBatch[] }>>(
    `/campaigns/${campaignId}/audience/import`,
    { method: 'POST', body }
  )
  return res.data
}

export async function listCampaignAudienceTargets(campaignId: string, audienceId: string): Promise<CampaignAudienceTarget[]> {
  const res = await notifyClient.request<NotifyResponse<CampaignAudienceTarget[]>>(
    `/campaigns/${campaignId}/audiences/${audienceId}/targets`
  )
  return res.data
}

export async function listCampaignAudienceBatches(campaignId: string, audienceId: string): Promise<CampaignAudienceBatch[]> {
  const res = await notifyClient.request<NotifyResponse<CampaignAudienceBatch[]>>(
    `/campaigns/${campaignId}/audiences/${audienceId}/batches`
  )
  return res.data
}

export async function retryCampaignAudienceBatch(campaignId: string, audienceId: string, batchId: string): Promise<void> {
  await notifyClient.request(`/campaigns/${campaignId}/audiences/${audienceId}/batches/${batchId}/retry`, {
    method: 'POST',
  })
}
