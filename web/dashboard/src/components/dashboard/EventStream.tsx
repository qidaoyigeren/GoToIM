import { useRealtimeStore } from '@/stores/realtimeStore'
import { Zap, CheckCheck, AlertCircle, ArrowRightLeft } from 'lucide-react'
import type { RealtimeEvent } from '@/types/message'

const iconMap = {
  push_sent: { icon: Zap, className: 'text-blue-500 bg-blue-50' },
  push_delivered: { icon: ArrowRightLeft, className: 'text-emerald-500 bg-emerald-50' },
  ack_received: { icon: CheckCheck, className: 'text-green-500 bg-green-50' },
  push_failed: { icon: AlertCircle, className: 'text-red-500 bg-red-50' },
  order_status_change: { icon: ArrowRightLeft, className: 'text-purple-500 bg-purple-50' },
}

export default function EventStream() {
  const events = useRealtimeStore((s) => s.events)

  return (
    <div className="bg-white rounded-xl border border-gray-100 shadow-sm flex flex-col h-[420px]">
      <div className="px-5 py-3 border-b border-gray-100 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-gray-700">实时事件流</h3>
        <span className="text-xs text-gray-400">{events.length} 条记录</span>
      </div>
      <div className="flex-1 overflow-auto">
        {events.length === 0 ? (
          <div className="flex items-center justify-center h-full text-sm text-gray-400">
            等待实时事件...
          </div>
        ) : (
          <div className="divide-y divide-gray-50">
            {events.map((event) => (
              <EventRow key={event.id} event={event} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function EventRow({ event }: { event: RealtimeEvent }) {
  const { icon: Icon, className } = iconMap[event.type] || iconMap.pull_sent

  return (
    <div className="px-5 py-2.5 flex items-center gap-3 animate-fade-in-up hover:bg-gray-50 transition-colors">
      <div className={`w-7 h-7 rounded-lg flex items-center justify-center flex-shrink-0 ${className}`}>
        <Icon size={13} />
      </div>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-xs font-medium text-gray-800 truncate">{event.title}</span>
          {event.delivery_path && (
            <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium ${
              event.delivery_path === 'grpc_direct'
                ? 'bg-emerald-50 text-emerald-600'
                : 'bg-amber-50 text-amber-600'
            }`}>
              {event.delivery_path === 'grpc_direct' ? 'gRPC' : 'Kafka'}
            </span>
          )}
        </div>
        <p className="text-[11px] text-gray-400 truncate mt-0.5">{event.detail}</p>
      </div>
      <span className="text-[10px] text-gray-400 flex-shrink-0 font-mono">
        {new Date(event.timestamp).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
      </span>
    </div>
  )
}
