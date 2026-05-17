import { isDemoMode } from '@/config'
import { logicClient } from './client'
import type { OnlineStats } from '@/types/online'
import type { ApiResponse } from '@/types/api'

export async function getOnlineTotal(): Promise<OnlineStats> {
  if (isDemoMode()) {
    const { mockOnlineStats } = await import('./mock')
    return mockOnlineStats()
  }
  const res = await logicClient.request<ApiResponse<OnlineStats>>('/online/total')
  return res.data
}

export async function getOnlineTop(type: string, limit: number): Promise<{ room_id: string; count: number }[]> {
  if (isDemoMode()) {
    return [
      { room_id: 'order_room', count: 342 },
      { room_id: 'flash_sale_all', count: 1280 },
      { room_id: 'logistics_room', count: 89 },
    ]
  }
  const res = await logicClient.request<ApiResponse<{ room_id: string; count: number }[]>>('/online/top', {
    params: { type, limit },
  })
  return res.data
}

export async function syncOfflineMessages(
  userId: string,
  lastSeq: number,
  limit: number
): Promise<{ current_seq: number; has_more: boolean; messages: Array<{ msg_id: string; content: string; seq: number; timestamp: number }> }> {
  if (isDemoMode()) {
    return { current_seq: lastSeq, has_more: false, messages: [] }
  }
  const res = await logicClient.request<
    ApiResponse<{ current_seq: number; has_more: boolean; messages: Array<{ msg_id: string; content: string; seq: number; timestamp: number }> }>
  >('/sync', { params: { mid: userId, last_seq: lastSeq, limit } })
  return res.data
}
