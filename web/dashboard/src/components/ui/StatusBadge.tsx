import type { OrderStatus } from '@/types/order'
import { ORDER_STATUS_LABELS } from '@/types/order'

type Props = {
  status: OrderStatus
  size?: 'sm' | 'md'
}

const colorMap: Record<OrderStatus, string> = {
  created: 'bg-blue-50 text-blue-700 border-blue-200',
  paid: 'bg-amber-50 text-amber-700 border-amber-200',
  confirmed: 'bg-indigo-50 text-indigo-700 border-indigo-200',
  shipped: 'bg-emerald-50 text-emerald-700 border-emerald-200',
  delivered: 'bg-green-50 text-green-700 border-green-200',
  cancelled: 'bg-red-50 text-red-700 border-red-200',
  delivery_failed: 'bg-pink-50 text-pink-700 border-pink-200',
}

export default function StatusBadge({ status, size = 'md' }: Props) {
  return (
    <span
      className={`inline-flex items-center border rounded-full font-medium ${
        colorMap[status]
      } ${size === 'sm' ? 'px-2 py-0.5 text-[10px]' : 'px-2.5 py-0.5 text-xs'}`}
    >
      {ORDER_STATUS_LABELS[status]}
    </span>
  )
}
