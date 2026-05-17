import { useChangeOrderStatus } from '@/hooks/useOrders'
import { getValidTransitions, ORDER_STATUS_LABELS, type OrderStatus } from '@/types/order'
import { Play } from 'lucide-react'

type Props = {
  orderId: string
  currentStatus: OrderStatus
}

export default function SimulateStatusChange({ orderId, currentStatus }: Props) {
  const mutation = useChangeOrderStatus()
  const transitions = getValidTransitions(currentStatus)

  if (transitions.length === 0) return null

  const handleChange = (newStatus: OrderStatus) => {
    mutation.mutate({ orderId, newStatus })
  }

  return (
    <div className="bg-white rounded-xl border border-gray-100 p-5 shadow-sm">
      <h3 className="text-sm font-semibold text-gray-700 mb-3">模拟状态变更</h3>
      <p className="text-xs text-gray-400 mb-3">点击下方按钮触发订单状态变更，观察实时推送链路</p>
      <div className="flex flex-wrap gap-2">
        {transitions.map((status) => (
          <button
            key={status}
            onClick={() => handleChange(status)}
            disabled={mutation.isPending}
            className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg text-xs font-medium bg-primary-50 text-primary-700 border border-primary-200 hover:bg-primary-100 disabled:opacity-50 transition-colors"
          >
            <Play size={12} />
            变更为「{ORDER_STATUS_LABELS[status]}」
          </button>
        ))}
      </div>
    </div>
  )
}
