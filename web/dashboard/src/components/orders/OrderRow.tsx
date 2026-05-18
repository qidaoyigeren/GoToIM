import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import type { Order } from '@/types/order'
import StatusBadge from '@/components/ui/StatusBadge'

type Props = { order: Order; index: number }

export default function OrderRow({ order, index }: Props) {
  const navigate = useNavigate()
  const [now] = useState(() => Date.now())

  const timeAgo = (dateStr: string) => {
    const diff = now - new Date(dateStr).getTime()
    const mins = Math.floor(diff / 60000)
    if (mins < 1) return '刚刚'
    if (mins < 60) return `${mins}分钟前`
    const hours = Math.floor(mins / 60)
    if (hours < 24) return `${hours}小时前`
    return `${Math.floor(hours / 24)}天前`
  }

  return (
    <tr
      className="border-b border-gray-50 hover:bg-gray-50/50 cursor-pointer transition-colors animate-fade-in-up"
      style={{ animationDelay: `${index * 30}ms` }}
      onClick={() => navigate(`/orders/${order.order_id}`)}
    >
      <td className="py-3 px-4">
        <span className="text-sm font-mono font-medium text-gray-800">{order.order_id}</span>
      </td>
      <td className="py-3 px-4 text-sm text-gray-600">{order.user_id}</td>
      <td className="py-3 px-4 text-sm text-gray-800 font-medium">
        ¥{order.total.toLocaleString()}
      </td>
      <td className="py-3 px-4">
        <StatusBadge status={order.status} />
      </td>
      <td className="py-3 px-4 text-sm text-gray-500">
        {order.items.map((i) => i.product_name).join(', ')}
      </td>
      <td className="py-3 px-4 text-xs text-gray-400">
        {timeAgo(order.updated_at)}
      </td>
    </tr>
  )
}
