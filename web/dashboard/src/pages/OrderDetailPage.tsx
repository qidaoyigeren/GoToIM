import { useParams, useNavigate } from 'react-router-dom'
import { useOrderDetail } from '@/hooks/useOrders'
import { useNotificationStore } from '@/stores/notificationStore'
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

  // Filter notifications for this order
  const orderNotifications = notifications.filter((n) => n.order_id === order.order_id)

  // Generate mock push traces based on notification history
  const traces = orderNotifications.map((n) => ({
    msg_id: n.notify_id,
    target: `用户 ${n.user_id}`,
    channel: stableBucket(n.notify_id) > 20 ? 'grpc_direct' as const : 'kafka_fallback' as const,
    status: n.status === 'acked' ? 'acked' as const :
            n.status === 'delivered' ? 'delivered' as const :
            n.status === 'failed' ? 'failed' as const : 'pending' as const,
    timestamp: n.created_at,
    retry_count: n.status === 'failed' ? (stableBucket(n.notify_id) % 3) + 1 : 0,
  }))

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

function stableBucket(value: string): number {
  let hash = 0
  for (let i = 0; i < value.length; i++) {
    hash = (hash * 31 + value.charCodeAt(i)) % 100
  }
  return hash
}
