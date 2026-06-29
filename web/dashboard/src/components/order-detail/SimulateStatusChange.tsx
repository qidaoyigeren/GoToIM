import { Play } from 'lucide-react'
import { useChangeOrderStatus } from '@/hooks/useOrders'
import { getValidTransitions, ORDER_STATUS_LABELS, type OrderStatus } from '@/types/order'

type Props = {
  orderId: string
  currentStatus: OrderStatus
}

export default function SimulateStatusChange({ orderId, currentStatus }: Props) {
  const mutation = useChangeOrderStatus()
  const transitions = getValidTransitions(currentStatus)

  if (transitions.length === 0) return null

  return (
    <div className="rounded-xl border border-gray-100 bg-white p-5 shadow-sm">
      <h3 className="mb-3 text-sm font-semibold text-gray-700">模拟后续状态</h3>
      <p className="mb-3 text-xs leading-5 text-gray-400">
        用于继续观察订单状态通知如何进入 outbox、attempt、ACK 和 trace。
      </p>
      <div className="flex flex-wrap gap-2">
        {transitions.map((status) => (
          <button
            key={status}
            onClick={() => mutation.mutate({ orderId, newStatus: status })}
            disabled={mutation.isPending}
            className="inline-flex items-center gap-1.5 rounded-lg border border-blue-200 bg-blue-50 px-4 py-2 text-xs font-medium text-blue-700 transition-colors hover:bg-blue-100 disabled:opacity-50"
          >
            <Play size={12} />
            变更为 {ORDER_STATUS_LABELS[status]}
          </button>
        ))}
      </div>
    </div>
  )
}
