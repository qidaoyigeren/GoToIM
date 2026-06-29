import { Check, Circle, Clock } from 'lucide-react'
import type { OrderStatus } from '@/types/order'
import { ORDER_STATUS_LABELS, ORDER_STATUS_SEQUENCE } from '@/types/order'

type Props = {
  currentStatus: OrderStatus
  statusTimestamps: Partial<Record<OrderStatus, string>>
}

export default function StatusTimeline({ currentStatus, statusTimestamps }: Props) {
  const currentIdx = ORDER_STATUS_SEQUENCE.indexOf(currentStatus)

  return (
    <div className="rounded-xl border border-gray-100 bg-white p-6 shadow-sm">
      <h3 className="mb-5 text-sm font-semibold text-gray-700">订单状态时间线</h3>
      <div className="space-y-0">
        {ORDER_STATUS_SEQUENCE.map((status, index) => {
          const isPast = currentIdx >= 0 && index <= currentIdx
          const isCurrent = index === currentIdx
          const timestamp = statusTimestamps[status]

          return (
            <div key={status} className="flex items-stretch gap-3">
              <div className="flex flex-col items-center">
                <div className={`flex h-8 w-8 items-center justify-center rounded-full border-2 transition-colors ${
                  isCurrent
                    ? 'border-blue-500 bg-blue-50'
                    : isPast
                      ? 'border-emerald-500 bg-emerald-50'
                      : 'border-gray-200 bg-white'
                }`}>
                  {isPast ? (
                    <Check size={14} className={isCurrent ? 'text-blue-600' : 'text-emerald-600'} />
                  ) : (
                    <Circle size={14} className="text-gray-300" />
                  )}
                </div>
                {index < ORDER_STATUS_SEQUENCE.length - 1 && (
                  <div className={`min-h-[20px] w-0.5 flex-1 ${index < currentIdx ? 'bg-emerald-300' : 'bg-gray-200'}`} />
                )}
              </div>

              <div className={`pb-5 ${index === ORDER_STATUS_SEQUENCE.length - 1 ? 'pb-0' : ''}`}>
                <p className={`text-sm font-medium ${isCurrent ? 'text-blue-700' : isPast ? 'text-gray-800' : 'text-gray-400'}`}>
                  {ORDER_STATUS_LABELS[status]}
                </p>
                {timestamp && (
                  <p className="mt-0.5 flex items-center gap-1 text-xs text-gray-400">
                    <Clock size={10} />
                    {new Date(timestamp).toLocaleString('zh-CN')}
                  </p>
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
