import { useOnlineStore } from '@/stores/onlineStore'
import { useConnectionStore } from '@/stores/connectionStore'
import { Activity, Server, Radio, Signal, Wifi } from 'lucide-react'

export default function SessionDetailPanel() {
  const stats = useOnlineStore((s) => s.stats)
  const connState = useConnectionStore((s) => s.state)
  const latencyMs = useConnectionStore((s) => s.latencyMs)
  const lastHeartbeat = useConnectionStore((s) => s.lastHeartbeat)

  const panels = [
    {
      icon: Activity,
      label: '连接状态',
      value: connState === 'connected' ? '已连接' : connState === 'reconnecting' ? '重连中' : '未连接',
      color: connState === 'connected' ? 'text-green-600' : 'text-amber-600',
    },
    {
      icon: Signal,
      label: '心跳延迟',
      value: latencyMs > 0 ? `${latencyMs}ms` : '—',
      color: latencyMs < 30 ? 'text-green-600' : 'text-amber-600',
    },
    {
      icon: Server,
      label: 'Comet 节点',
      value: stats ? 'comet:3109' : '—',
      color: 'text-gray-700',
    },
    {
      icon: Radio,
      label: '房间广播',
      value: `${stats?.ip_count ?? 0} IP`,
      color: 'text-gray-700',
    },
    {
      icon: Wifi,
      label: '协议',
      value: 'WebSocket (二进制)',
      color: 'text-gray-700',
    },
  ]

  return (
    <div className="bg-white rounded-xl border border-gray-100 p-5 shadow-sm">
      <h3 className="text-sm font-semibold text-gray-700 mb-4">连接摘要</h3>
      <div className="space-y-3">
        {panels.map((p) => {
          const Icon = p.icon
          return (
            <div key={p.label} className="flex items-center justify-between">
              <div className="flex items-center gap-2 text-xs text-gray-500">
                <Icon size={13} className="text-gray-400" />
                {p.label}
              </div>
              <span className={`text-xs font-medium ${p.color}`}>{p.value}</span>
            </div>
          )
        })}
        <div className="pt-3 border-t border-gray-100">
          <div className="flex items-center justify-between">
            <span className="text-xs text-gray-400">最后心跳</span>
            <span className="text-xs font-mono text-gray-500">
              {lastHeartbeat ? new Date(lastHeartbeat).toLocaleTimeString('zh-CN') : '—'}
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}
