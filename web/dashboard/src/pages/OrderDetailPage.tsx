import { useParams, useNavigate } from 'react-router-dom'
import { useEffect, useMemo, useState } from 'react'
import { useOrderDetail } from '@/hooks/useOrders'
import { useNotificationStore } from '@/stores/notificationStore'
import { getNotificationAttempts } from '@/api/notify'
import type { NotificationAttempt } from '@/types/notification'
import type { TraceRecord } from '@/components/order-detail/PushTraceCard'
import OrderInfo from '@/components/order-detail/OrderInfo'
import StatusTimeline from '@/components/order-detail/StatusTimeline'
import MessageRecord from '@/components/order-detail/MessageRecord'
import PushTraceCard from '@/components/order-detail/PushTraceCard'
import SimulateStatusChange from '@/components/order-detail/SimulateStatusChange'
import { SkeletonCard } from '@/components/ui/Skeleton'
import ErrorState from '@/components/ui/ErrorState'
import { ArrowLeft } from 'lucide-react'

export default function OrderDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: order, isLoading, error, refetch } = useOrderDetail(id!)
  const notifications = useNotificationStore((s) => s.notifications)
  const [traces, setTraces] = useState<TraceRecord[]>([])

  // Filter notifications for this order
  const orderNotifications = useMemo(
    () => order ? notifications.filter((n) => n.order_id === order.order_id) : [],
    [notifications, order]
  )
  const orderNotificationIds = useMemo(
    () => orderNotifications.map((n) => n.notify_id).join('|'),
    [orderNotifications]
  )

  // Fetch real delivery attempts from the API
  useEffect(() => {
    let cancelled = false
    const loadTraces = async () => {
      if (orderNotifications.length === 0) {
        setTraces([])
        return
      }
      try {
        const results = await Promise.all(orderNotifications.map((n) => getNotificationAttempts(n.notify_id)))
        if (cancelled) return
        setTraces(results.flat().map(mapAttemptToTrace))
      } catch {
        if (!cancelled) setTraces([])
      }
    }
    void loadTraces()
    return () => { cancelled = true }
  }, [orderNotifications, orderNotificationIds])

  if (isLoading) {
    return (
      <div className="space-y-6 animate-fade-in">
        <SkeletonCard />
        <SkeletonCard />
        <SkeletonCard />
      </div>
    )
  }

  if (error || !order) {
    return <ErrorState message="加载订单失败" onRetry={() => refetch()} />
  }

  // Generate status timestamps from order data
  const statusTimestamps: Partial<Record<string, string>> = {
    created: order.created_at,
  }
  if (order.status !== 'created') statusTimestamps[order.status] = order.updated_at

  return (
    <div className="space-y-6 animate-fade-in">
      <button
        onClick={() => navigate('/orders')}
        className="inline-flex items-center gap-1.5 text-sm text-gray-500 hover:text-gray-700 transition-colors"
      >
        <ArrowLeft size={16} />
        返回订单列表
      </button>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <div className="lg:col-span-2 space-y-6">
          <OrderInfo order={order} />
          <PushTraceCard traces={traces} />
        </div>
        <div className="space-y-6">
          <StatusTimeline currentStatus={order.status} statusTimestamps={statusTimestamps} />
          <SimulateStatusChange orderId={order.order_id} currentStatus={order.status} />
        </div>
      </div>

      {orderNotifications.length > 0 && (
        <div className="bg-white rounded-xl border border-gray-100 p-5 shadow-sm">
          <h3 className="text-sm font-semibold text-gray-700 mb-3">消息记录</h3>
          <div className="space-y-2">
            {orderNotifications.map((n) => (
              <MessageRecord key={n.notify_id} notification={n} />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function mapAttemptToTrace(a: NotificationAttempt): TraceRecord {
  const raw = (a.path || a.channel || '').toLowerCase()
  const channel = raw.includes('kafka') ? 'kafka_fallback' as const : 'grpc_direct' as const
  const statusMap: Record<string, TraceRecord['status']> = {
    delivered: 'delivered', acked: 'acked', failed: 'failed',
  }
  return {
    msg_id: a.attempt_id,
    target: a.target,
    channel,
    status: statusMap[a.status] || 'pending',
    timestamp: a.started_at,
    retry_count: Math.max(0, (a.attempt_no || 1) - 1),
  }
}
