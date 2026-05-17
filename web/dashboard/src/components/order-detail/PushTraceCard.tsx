import { CheckCheck, Clock, AlertTriangle, RefreshCw } from 'lucide-react'

type TraceRecord = {
  msg_id: string
  target: string
  channel: 'grpc_direct' | 'kafka_fallback'
  status: 'delivered' | 'acked' | 'pending' | 'failed'
  timestamp: string
  retry_count: number
  failure_reason?: string
}

type Props = { traces: TraceRecord[] }

const statusIcons = {
  delivered: { icon: CheckCheck, className: 'text-emerald-500' },
  acked: { icon: CheckCheck, className: 'text-green-600' },
  pending: { icon: Clock, className: 'text-amber-500' },
  failed: { icon: AlertTriangle, className: 'text-red-500' },
}

const statusLabels = {
  delivered: '已送达',
  acked: '已确认',
  pending: '等待中',
  failed: '失败',
}

export default function PushTraceCard({ traces }: Props) {
  return (
    <div className="bg-white rounded-xl border border-gray-100 shadow-sm">
      <div className="px-5 py-3 border-b border-gray-100">
        <h3 className="text-sm font-semibold text-gray-700">推送追踪</h3>
        <p className="text-xs text-gray-400 mt-0.5">消息从生产到消费的完整链路</p>
      </div>
      <div className="overflow-auto">
        <table className="w-full">
          <thead>
            <tr className="border-b border-gray-100">
              <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2 px-4">消息 ID</th>
              <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2 px-4">目标</th>
              <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2 px-4">通道</th>
              <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2 px-4">状态</th>
              <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2 px-4">时间</th>
              <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2 px-4">重试</th>
            </tr>
          </thead>
          <tbody>
            {traces.map((t) => {
              const { icon: Icon, className } = statusIcons[t.status]
              return (
                <tr key={t.msg_id} className="border-b border-gray-50 hover:bg-gray-50/50 transition-colors">
                  <td className="py-2.5 px-4">
                    <span className="text-xs font-mono text-gray-600">{t.msg_id.slice(0, 16)}...</span>
                  </td>
                  <td className="py-2.5 px-4 text-xs text-gray-600">{t.target}</td>
                  <td className="py-2.5 px-4">
                    <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium ${
                      t.channel === 'grpc_direct'
                        ? 'bg-emerald-50 text-emerald-600'
                        : 'bg-amber-50 text-amber-600'
                    }`}>
                      {t.channel === 'grpc_direct' ? 'gRPC 直连' : 'Kafka'}
                    </span>
                  </td>
                  <td className="py-2.5 px-4">
                    <div className="flex items-center gap-1.5">
                      <Icon size={12} className={className} />
                      <span className="text-xs text-gray-600">{statusLabels[t.status]}</span>
                    </div>
                  </td>
                  <td className="py-2.5 px-4 text-xs text-gray-400">
                    {new Date(t.timestamp).toLocaleTimeString('zh-CN')}
                  </td>
                  <td className="py-2.5 px-4">
                    {t.retry_count > 0 ? (
                      <span className="flex items-center gap-1 text-xs text-amber-600">
                        <RefreshCw size={10} />
                        {t.retry_count}
                      </span>
                    ) : (
                      <span className="text-xs text-gray-300">—</span>
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
