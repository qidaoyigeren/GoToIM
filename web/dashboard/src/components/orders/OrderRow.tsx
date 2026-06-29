import { MessageSquareText, ReceiptText } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import StatusBadge from '@/components/ui/StatusBadge'
import type { Order, OrderImportance } from '@/types/order'
import { IMPORTANCE_LABELS, ORDER_TYPE_LABELS } from '@/types/order'

type Props = { order: Order; index: number }

const importanceTone: Record<OrderImportance, string> = {
  normal: 'bg-gray-50 text-gray-600',
  high: 'bg-blue-50 text-blue-700',
  urgent: 'bg-amber-50 text-amber-700',
  critical: 'bg-red-50 text-red-700',
}

export default function OrderRow({ order, index }: Props) {
  const navigate = useNavigate()
  const importance = order.importance || 'normal'
  const firstItem = order.items[0]

  return (
    <article
      className="rounded-lg border border-gray-100 bg-white p-4 transition-colors hover:border-blue-100 hover:bg-blue-50/30"
      style={{ animationDelay: `${index * 30}ms` }}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-sm font-semibold text-gray-900">{order.order_id}</span>
            <StatusBadge status={order.status} size="sm" />
          </div>
          <p className="mt-1 text-xs text-gray-500">
            用户 {order.user_id} / {order.merchant_name || order.merchant_id || '未指定商家'}
          </p>
        </div>
        <span className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${importanceTone[importance]}`}>
          {IMPORTANCE_LABELS[importance]}
        </span>
      </div>

      <div className="mt-4 flex gap-3">
        <div className="flex h-16 w-16 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-500">
          <ReceiptText size={24} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium text-gray-900">{firstItem?.product_name || '订单商品'}</div>
          <div className="mt-1 text-xs text-gray-500">
            {ORDER_TYPE_LABELS[order.order_type || 'normal']} / {order.items.length} 类商品
          </div>
          <div className="mt-2 flex items-center justify-between">
            <span className="text-base font-bold text-gray-950">¥{order.total.toLocaleString()}</span>
            <span className="text-xs text-gray-400">{formatRelative(order.updated_at)}</span>
          </div>
        </div>
      </div>

      <div className="mt-4 flex flex-wrap gap-2 border-t border-gray-100 pt-3">
        <button
          type="button"
          onClick={() => navigate(`/orders/${order.order_id}`)}
          className="rounded-lg bg-gray-900 px-3 py-1.5 text-xs font-medium text-white hover:bg-gray-800"
        >
          查看详情
        </button>
        <button
          type="button"
          onClick={() => navigate(`/chat?order_id=${encodeURIComponent(order.order_id)}&conversation_id=${encodeURIComponent(order.private_conversation_id || '')}`)}
          className="inline-flex items-center gap-1.5 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-1.5 text-xs font-medium text-emerald-700 hover:bg-emerald-100"
        >
          <MessageSquareText size={13} />
          订单私聊
        </button>
        {order.support_room_id && (
          <button
            type="button"
            onClick={() => navigate(`/chat?room_id=${encodeURIComponent(order.support_room_id || '')}`)}
            className="rounded-lg border border-blue-200 bg-blue-50 px-3 py-1.5 text-xs font-medium text-blue-700 hover:bg-blue-100"
          >
            商家群聊
          </button>
        )}
      </div>
    </article>
  )
}

function formatRelative(dateStr: string) {
  const diff = Date.now() - new Date(dateStr).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return '刚刚'
  if (mins < 60) return `${mins}分钟前`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}小时前`
  return `${Math.floor(hours / 24)}天前`
}
