import { useRealtimeStore } from '@/stores/realtimeStore'
import { useOnlineStore } from '@/stores/onlineStore'
import { Server, Zap, CheckCheck, Database } from 'lucide-react'

const stages = [
  {
    key: 'logic',
    icon: Server,
    label: 'Logic Router',
    desc: '消息路由 / 用户查找 / 订阅匹配',
    getValue: (stats: ReturnType<typeof useRealtimeStore.getState>['stats'], online: ReturnType<typeof useOnlineStore.getState>['stats']) =>
      stats ? `${stats.push_rate_per_sec} msg/s` : '—',
  },
  {
    key: 'delivery',
    icon: Zap,
    label: 'Comet Push',
    desc: 'gRPC 直连 / Kafka 回退 / 房间广播',
    getValue: (stats: ReturnType<typeof useRealtimeStore.getState>['stats'], _online: ReturnType<typeof useOnlineStore.getState>['stats']) =>
      stats ? `gRPC ${(stats.delivery_path.grpc_direct * 100).toFixed(0)}%` : '—',
  },
  {
    key: 'ack',
    icon: CheckCheck,
    label: 'Client ACK',
    desc: '消息确认 / 离线队列移除',
    getValue: (stats: ReturnType<typeof useRealtimeStore.getState>['stats'], _online: ReturnType<typeof useOnlineStore.getState>['stats']) =>
      stats ? `ACK ${(stats.ack_rate * 100).toFixed(1)}%` : '—',
  },
  {
    key: 'persist',
    icon: Database,
    label: '持久化',
    desc: '消息落库 / 离线补偿',
    getValue: (_stats: ReturnType<typeof useRealtimeStore.getState>['stats'], online: ReturnType<typeof useOnlineStore.getState>['stats']) =>
      online ? `${online.offline_pending} 条待补` : '—',
  },
]

export default function MessageFlowViz() {
  const stats = useRealtimeStore((s) => s.stats)
  const onlineStats = useOnlineStore((s) => s.stats)
  const events = useRealtimeStore((s) => s.events)
  const hasData = stats && stats.push_rate_per_sec > 0

  return (
    <div className="bg-white rounded-xl border border-gray-100 p-5 shadow-sm">
      <h3 className="text-sm font-semibold text-gray-700 mb-4">消息链路实时状态</h3>
      <p className="text-xs text-gray-400 mb-4">
        {hasData ? '后端真实数据 · 消息从生产到 ACK 的完整链路' : '等待后端推送数据...'}
      </p>

      {/* Pipeline stages */}
      <div className="flex items-start justify-between mb-4">
        {stages.map(({ key, icon: Icon, label, desc, getValue }, idx) => {
          const value = getValue(stats, onlineStats)
          const active = hasData
          return (
            <div key={key} className="flex items-start">
              <div className="flex flex-col items-center text-center w-[72px]">
                <div className={`w-10 h-10 rounded-xl flex items-center justify-center transition-all ${
                  active ? 'bg-emerald-50' : 'bg-gray-100'
                }`}>
                  <Icon size={18} className={active ? 'text-emerald-600' : 'text-gray-400'} />
                </div>
                <span className="text-[10px] font-medium mt-1 text-gray-700">{label}</span>
                <span className="text-[9px] text-gray-400 leading-tight">{desc}</span>
                <span className={`text-[10px] font-bold mt-1 ${active ? 'text-emerald-600' : 'text-gray-400'}`}>
                  {value}
                </span>
              </div>
              {idx < stages.length - 1 && (
                <div className={`w-8 h-0.5 mt-5 ${active ? 'bg-emerald-300' : 'bg-gray-200'}`} />
              )}
            </div>
          )
        })}
      </div>

      {/* Delivery path breakdown */}
      {stats && (
        <div className="mt-4 pt-4 border-t border-gray-100">
          <p className="text-[10px] text-gray-400 uppercase tracking-wider mb-2">推送通道分布</p>
          <div className="flex items-center gap-3">
            <div className="flex-1">
              <div className="flex justify-between text-xs mb-1">
                <span className="text-emerald-600 font-medium">gRPC 直连</span>
                <span className="text-gray-500">{(stats.delivery_path.grpc_direct * 100).toFixed(1)}%</span>
              </div>
              <div className="h-2 bg-gray-100 rounded-full overflow-hidden">
                <div
                  className="h-full bg-emerald-500 rounded-full transition-all duration-500"
                  style={{ width: `${stats.delivery_path.grpc_direct * 100}%` }}
                />
              </div>
            </div>
            <div className="flex-1">
              <div className="flex justify-between text-xs mb-1">
                <span className="text-amber-600 font-medium">Kafka 回退</span>
                <span className="text-gray-500">{(stats.delivery_path.kafka_fallback * 100).toFixed(1)}%</span>
              </div>
              <div className="h-2 bg-gray-100 rounded-full overflow-hidden">
                <div
                  className="h-full bg-amber-500 rounded-full transition-all duration-500"
                  style={{ width: `${stats.delivery_path.kafka_fallback * 100}%` }}
                />
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Latency info */}
      {stats && (
        <div className="mt-3 flex items-center justify-between text-[10px] text-gray-400">
          <span>P50 延迟: <strong className="text-gray-600">{stats.latency_p50_ms}ms</strong></span>
          <span>P99 延迟: <strong className="text-gray-600">{stats.latency_p99_ms}ms</strong></span>
          <span>最大: <strong className="text-gray-600">{stats.latency_max_ms}ms</strong></span>
          <span>累计推送: <strong className="text-gray-600">{stats.total_pushed.toLocaleString()}</strong></span>
        </div>
      )}
    </div>
  )
}
