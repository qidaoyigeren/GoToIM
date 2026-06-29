import type { OrderStatus } from '@/types/order'
import { ORDER_STATUS_LABELS } from '@/types/order'

type Props = {
  status: OrderStatus
  size?: 'sm' | 'md'
}

const colorMap: Record<OrderStatus, string> = {
  created: 'border-blue-200 bg-blue-50 text-blue-700',
  paid: 'border-amber-200 bg-amber-50 text-amber-700',
  confirmed: 'border-indigo-200 bg-indigo-50 text-indigo-700',
  shipped: 'border-emerald-200 bg-emerald-50 text-emerald-700',
  delivered: 'border-green-200 bg-green-50 text-green-700',
  cancelled: 'border-red-200 bg-red-50 text-red-700',
  delivery_failed: 'border-pink-200 bg-pink-50 text-pink-700',
}

export default function StatusBadge({ status, size = 'md' }: Props) {
  return (
    <span
      className={`inline-flex items-center rounded-full border font-medium ${colorMap[status]} ${
        size === 'sm' ? 'px-2 py-0.5 text-[10px]' : 'px-2.5 py-0.5 text-xs'
      }`}
    >
      {ORDER_STATUS_LABELS[status] ?? status}
    </span>
  )
}
