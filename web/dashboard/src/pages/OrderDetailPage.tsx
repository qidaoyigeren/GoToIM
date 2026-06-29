import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, MessageSquareText, UsersRound } from 'lucide-react'
import { getNotificationTrace, getOrderTimeline } from '@/api/notify'
import MessageRecord from '@/components/order-detail/MessageRecord'
import OrderInfo from '@/components/order-detail/OrderInfo'
import PushTraceCard from '@/components/order-detail/PushTraceCard'
import SimulateStatusChange from '@/components/order-detail/SimulateStatusChange'
import StatusTimeline from '@/components/order-detail/StatusTimeline'
import ErrorState from '@/components/ui/ErrorState'
import { SkeletonCard } from '@/components/ui/Skeleton'
import { CARD_LG } from '@/components/ui/cardStyles'
import { useOrderDetail } from '@/hooks/useOrders'
import { useNotificationStore } from '@/stores/notificationStore'
import type { OrderStatus } from '@/types/order'
import type { Notification, NotificationTrace, OrderTimeline } from '@/types/notification'

const EMPTY_NOTIFICATIONS: Notification[] = []

export default function OrderDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: order, isLoading, error, refetch } = useOrderDetail(id || '')
  const notifications = useNotificationStore((s) => s.notifications) ?? EMPTY_NOTIFICATIONS
  const [trace, setTrace] = useState<NotificationTrace | null>(null)
  const [timeline, setTimeline] = useState<OrderTimeline | null>(null)

  const orderNotifications = useMemo(
    () => order ? notifications.filter((item) => item.order_id === order.order_id) : [],
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
    return <ErrorState message="订单加载失败" onRetry={() => refetch()} />
  }

  const statusTimestamps: Partial<Record<OrderStatus, string>> = {
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
        返回订单
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
            onClick={() => navigate(`/chat?order_id=${encodeURIComponent(order.order_id)}&conversation_id=${encodeURIComponent(order.private_conversation_id || '')}`)}
            className="flex w-full items-center justify-between rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-left text-sm font-medium text-emerald-800 transition-colors hover:bg-emerald-100"
          >
            <span>
              <span className="block">打开订单私聊</span>
              <span className="mt-0.5 block text-xs font-normal text-emerald-700">用户和商家基于订单的 direct push</span>
            </span>
            <MessageSquareText size={18} />
          </button>
          {order.support_room_id && (
            <button
              type="button"
              onClick={() => navigate(`/chat?room_id=${encodeURIComponent(order.support_room_id || '')}`)}
              className="flex w-full items-center justify-between rounded-lg border border-blue-200 bg-blue-50 px-4 py-3 text-left text-sm font-medium text-blue-800 transition-colors hover:bg-blue-100"
            >
              <span>
                <span className="block">加入商家群聊</span>
                <span className="mt-0.5 block text-xs font-normal text-blue-700">观察 room push 链路</span>
              </span>
              <UsersRound size={18} />
            </button>
          )}
          <SimulateStatusChange orderId={order.order_id} currentStatus={order.status} />
        </div>
      </div>

      {timelineNotifications.length > 0 && (
        <section className={CARD_LG}>
          <h3 className="mb-3 text-sm font-semibold text-gray-700">订单通知记录</h3>
          <div className="space-y-2">
            {timelineNotifications.map((notification) => (
              <button
                key={notification.notify_id}
                type="button"
                className="w-full text-left"
                onClick={() => { void getNotificationTrace(notification.notify_id).then(setTrace) }}
              >
                <MessageRecord notification={notification} />
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
      <h3 className="text-sm font-semibold text-gray-800">订单消息链路时间线</h3>
      <div className="mt-4 space-y-3">
        {(timeline.timeline ?? []).map((event) => (
          <div key={`${event.type}-${event.id}`} className="flex gap-3">
            <div className="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-gray-400" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <p className="text-sm font-medium text-gray-800">{event.label}</p>
                {event.status && <Chip label={event.status} />}
                {event.delivery_path && <Chip label={event.delivery_path} tone="blue" />}
                {event.priority && <Chip label={`priority ${event.priority}`} tone={event.priority === 'critical' ? 'red' : 'blue'} />}
                {event.ack_policy && <Chip label={`ACK ${event.ack_policy}`} tone="green" />}
              </div>
              {event.ttl_seconds ? <p className="mt-0.5 text-xs text-gray-500">TTL {event.ttl_seconds}s</p> : null}
              {event.detail && <p className="mt-0.5 text-xs text-gray-500">{event.detail}</p>}
              <p className="mt-0.5 text-xs text-gray-400">{new Date(event.occurred_at).toLocaleString('zh-CN')}</p>
            </div>
          </div>
        ))}
      </div>
    </section>
  )
}

function Chip({ label, tone = 'gray' }: { label: string; tone?: 'gray' | 'blue' | 'green' | 'red' }) {
  const colors = {
    gray: 'bg-gray-100 text-gray-600',
    blue: 'bg-blue-50 text-blue-700',
    green: 'bg-emerald-50 text-emerald-700',
    red: 'bg-red-50 text-red-700',
  }
  return <span className={`rounded-full px-2 py-0.5 text-xs ${colors[tone]}`}>{label}</span>
}
