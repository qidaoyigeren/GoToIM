import { useParams, useNavigate } from 'react-router-dom'
import { useEffect, useMemo, useState } from 'react'
import { useOrderDetail } from '@/hooks/useOrders'
import { useNotificationStore } from '@/stores/notificationStore'
import { getNotificationTrace, getOrderTimeline } from '@/api/notify'
import type { NotificationTrace, OrderTimeline } from '@/types/notification'
import OrderInfo from '@/components/order-detail/OrderInfo'
import StatusTimeline from '@/components/order-detail/StatusTimeline'
import MessageRecord from '@/components/order-detail/MessageRecord'
import PushTraceCard from '@/components/order-detail/PushTraceCard'
import SimulateStatusChange from '@/components/order-detail/SimulateStatusChange'
import { SkeletonCard } from '@/components/ui/Skeleton'
import { CARD_LG } from '@/components/ui/cardStyles'
import ErrorState from '@/components/ui/ErrorState'
import { ArrowLeft, MessageSquareText } from 'lucide-react'
import type { Notification } from '@/types/notification'

const EMPTY_NOTIFICATIONS: Notification[] = []

export default function OrderDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: order, isLoading, error, refetch } = useOrderDetail(id!)
  const notifications = useNotificationStore((s) => s.notifications) ?? EMPTY_NOTIFICATIONS
  const [trace, setTrace] = useState<NotificationTrace | null>(null)
  const [timeline, setTimeline] = useState<OrderTimeline | null>(null)

  const orderNotifications = useMemo(
    () => order ? notifications.filter((n) => n.order_id === order.order_id) : [],
    [notifications, order]
  )

  useEffect(() => {
    if (!id) return
    let cancelled = false
    const load = async () => {
      try {
        const data = await getOrderTimeline(id)
        if (cancelled) return
        setTimeline(data)
        const firstNotification = data.notifications?.[0]
        if (!firstNotification) {
          setTrace(null)
          return
        }
        const traceData = await getNotificationTrace(firstNotification.notify_id)
        if (!cancelled) setTrace(traceData)
      } catch {
        if (!cancelled) {
          setTimeline(null)
          setTrace(null)
        }
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [id])

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
    return <ErrorState message="Failed to load order" onRetry={() => refetch()} />
  }

  const statusTimestamps: Partial<Record<string, string>> = {
    created: order.created_at,
  }
  if (order.status !== 'created') statusTimestamps[order.status] = order.updated_at

  const timelineNotifications = timeline?.notifications?.length ? timeline.notifications : orderNotifications

  return (
    <div className="space-y-6 animate-fade-in">
      <button
        onClick={() => navigate('/orders')}
        className="inline-flex items-center gap-1.5 text-sm text-gray-500 transition-colors hover:text-gray-700"
      >
        <ArrowLeft size={16} />
        Back to orders
      </button>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <div className="space-y-6 lg:col-span-2">
          <OrderInfo order={order} />
          <PushTraceCard trace={trace} />
          {timeline && <BusinessTimeline timeline={timeline} />}
        </div>
        <div className="space-y-6">
          <StatusTimeline currentStatus={order.status} statusTimestamps={statusTimestamps} />
          <button
            type="button"
            onClick={() => navigate(`/chat?order_id=${encodeURIComponent(order.order_id)}`)}
            className="flex w-full items-center justify-between rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-left text-sm font-medium text-emerald-800 transition-colors hover:bg-emerald-100"
          >
            <span>
              <span className="block">打开订单在线客服</span>
              <span className="mt-0.5 block text-xs font-normal text-emerald-700">本订单的直连 IM 会话</span>
            </span>
            <MessageSquareText size={18} />
          </button>
          <SimulateStatusChange orderId={order.order_id} currentStatus={order.status} />
        </div>
      </div>

      {timelineNotifications.length > 0 && (
        <section className={CARD_LG}>
          <h3 className="mb-3 text-sm font-semibold text-gray-700">消息记录</h3>
          <div className="space-y-2">
            {timelineNotifications.map((n) => (
              <button
                key={n.notify_id}
                type="button"
                className="w-full text-left"
                onClick={() => {
                  void getNotificationTrace(n.notify_id).then(setTrace)
                }}
              >
                <MessageRecord notification={n} />
              </button>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

function BusinessTimeline({ timeline }: { timeline: OrderTimeline }) {
  return (
    <section className={CARD_LG}>
      <h3 className="text-sm font-semibold text-gray-800">业务时间线</h3>
      <div className="mt-4 space-y-3">
        {(timeline.timeline ?? []).map((event) => (
          <div key={`${event.type}-${event.id}`} className="flex gap-3">
            <div className="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-gray-400" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <p className="text-sm font-medium text-gray-800">{event.label}</p>
                {event.status && <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-600">{event.status}</span>}
                {event.delivery_path && <span className="rounded-full bg-blue-50 px-2 py-0.5 text-xs text-blue-700">{event.delivery_path}</span>}
              </div>
              {event.detail && <p className="mt-0.5 text-xs text-gray-500">{event.detail}</p>}
              <p className="mt-0.5 text-xs text-gray-400">{new Date(event.occurred_at).toLocaleString()}</p>
            </div>
          </div>
        ))}
      </div>
    </section>
  )
}
