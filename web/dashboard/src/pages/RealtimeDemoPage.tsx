import MessageSender from '@/components/realtime/MessageSender'
import BatchSimulator from '@/components/realtime/BatchSimulator'
import MessageFlowViz from '@/components/realtime/MessageFlowViz'
import EventStream from '@/components/dashboard/EventStream'
import { useWebSocket } from '@/websocket/useWebSocket'
import { useRealtimeStore } from '@/stores/realtimeStore'
import { useOnlineStore } from '@/stores/onlineStore'
import { useConnectionStore } from '@/stores/connectionStore'
import { Zap, Activity, Radio, Server } from 'lucide-react'

export default function RealtimeDemoPage() {
  useWebSocket()
  const stats = useRealtimeStore((s) => s.stats)
  const onlineStats = useOnlineStore((s) => s.stats)
  const connState = useConnectionStore((s) => s.state)
  const latency = useConnectionStore((s) => s.latencyMs)

  return (
    <div className="space-y-6 animate-fade-in">
      <div>
        <h1 className="text-xl font-bold text-gray-900">实时消息演示面板</h1>
        <p className="text-sm text-gray-500 mt-1">
          手动发送消息 → 后端路由 → Comet 推送 → 客户端 ACK，全链路可观测
        </p>
      </div>

      {/* Real stats from backend */}
      <div className="grid grid-cols-4 gap-3">
        <div className="bg-white rounded-lg border border-gray-100 px-4 py-3 shadow-sm flex items-center gap-3">
          <Activity size={16} className="text-blue-500" />
          <div>
            <p className="text-[10px] text-gray-400 uppercase">推送速率</p>
            <p className="text-sm font-bold text-gray-800">{stats?.push_rate_per_sec ?? '—'}/s</p>
          </div>
        </div>
        <div className="bg-white rounded-lg border border-gray-100 px-4 py-3 shadow-sm flex items-center gap-3">
          <Radio size={16} className="text-green-500" />
          <div>
            <p className="text-[10px] text-gray-400 uppercase">ACK 率</p>
            <p className="text-sm font-bold text-gray-800">{stats ? `${(stats.ack_rate * 100).toFixed(1)}%` : '—'}</p>
          </div>
        </div>
        <div className="bg-white rounded-lg border border-gray-100 px-4 py-3 shadow-sm flex items-center gap-3">
          <Server size={16} className="text-emerald-500" />
          <div>
            <p className="text-[10px] text-gray-400 uppercase">gRPC 直连</p>
            <p className="text-sm font-bold text-gray-800">{stats ? `${(stats.delivery_path.grpc_direct * 100).toFixed(0)}%` : '—'}</p>
          </div>
        </div>
        <div className="bg-white rounded-lg border border-gray-100 px-4 py-3 shadow-sm flex items-center gap-3">
          <Zap size={16} className="text-amber-500" />
          <div>
            <p className="text-[10px] text-gray-400 uppercase">WS 延迟</p>
            <p className="text-sm font-bold text-gray-800">{connState === 'connected' ? `${latency}ms` : '—'}</p>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <div className="space-y-6">
          <MessageSender />
          <BatchSimulator />
        </div>
        <div className="lg:col-span-2 space-y-6">
          <MessageFlowViz />
          <EventStream />
        </div>
      </div>
    </div>
  )
}
