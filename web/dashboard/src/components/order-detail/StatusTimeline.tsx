import type { OrderStatus } from '@/types/order'
import { ORDER_STATUS_LABELS, ORDER_STATUS_SEQUENCE } from '@/types/order'
import { Check, Clock, Circle } from 'lucide-react'

type Props = {
  currentStatus: OrderStatus
  statusTimestamps: Partial<Record<OrderStatus, string>>
}

export default function StatusTimeline({ currentStatus, statusTimestamps }: Props) {
  const currentIdx = ORDER_STATUS_SEQUENCE.indexOf(currentStatus)

  return (
    <div className="bg-white rounded-xl border border-gray-100 p-6 shadow-sm">
      <h3 className="text-sm font-semibold text-gray-700 mb-5">状态时间线</h3>
      <div className="space-y-0">
        {ORDER_STATUS_SEQUENCE.map((status, idx) => {
          const isPast = idx <= currentIdx && currentIdx >= 0
          const isCurrent = idx === currentIdx
          const ts = statusTimestamps[status]

          return (
            <div key={status} className="flex items-stretch gap-3">
              {/* Timeline line & dot */}
              <div className="flex flex-col items-center">
                <div className={`w-8 h-8 rounded-full flex items-center justify-center border-2 transition-colors ${
                  isCurrent
                    ? 'border-primary-500 bg-primary-50'
                    : isPast
                    ? 'border-emerald-500 bg-emerald-50'
                    : 'border-gray-200 bg-white'
                }`}>
                  {isPast ? (
                    <Check size={14} className={isCurrent ? 'text-primary-600' : 'text-emerald-600'} />
                  ) : (
                    <Circle size={14} className="text-gray-300" />
                  )}
                </div>
                {idx < ORDER_STATUS_SEQUENCE.length - 1 && (
                  <div className={`w-0.5 flex-1 min-h-[20px] ${
                    idx < currentIdx ? 'bg-emerald-300' : 'bg-gray-200'
                  }`} />
                )}
              </div>

              {/* Content */}
              <div className={`pb-5 ${idx === ORDER_STATUS_SEQUENCE.length - 1 ? 'pb-0' : ''}`}>
                <p className={`text-sm font-medium ${
                  isCurrent ? 'text-primary-700' : isPast ? 'text-gray-800' : 'text-gray-400'
                }`}>
                  {ORDER_STATUS_LABELS[status]}
                </p>
                {ts && (
                  <p className="text-xs text-gray-400 mt-0.5 flex items-center gap-1">
                    <Clock size={10} />
                    {new Date(ts).toLocaleString('zh-CN')}
                  </p>
                )}
                {!ts && isPast && (
                  <p className="text-xs text-gray-400 mt-0.5">—</p>
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
