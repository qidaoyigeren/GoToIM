import EventStream from '@/components/dashboard/EventStream'
import MessageFlowViz from '@/components/realtime/MessageFlowViz'
import OrderScenarioConsole from '@/components/realtime/OrderScenarioConsole'
import { useConnectionStore } from '@/stores/connectionStore'
import { useOnlineStore } from '@/stores/onlineStore'
import { useRealtimeStore } from '@/stores/realtimeStore'
import { Activity, Radio, Server, ShieldCheck, Zap } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

export default function RealtimeWorkbenchPage() {
  const stats = useRealtimeStore((s) => s.stats)
  const onlineStats = useOnlineStore((s) => s.stats)
  const connState = useConnectionStore((s) => s.state)
  const latency = useConnectionStore((s) => s.latencyMs)

  return (
    <div className="space-y-6 animate-fade-in">
      <div className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-primary-600">Order Operations</p>
          <h1 className="mt-1 text-2xl font-bold text-gray-950">实时订单状态与通知业务工作台</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-gray-600">
            面向电商履约、大促峰值和高价值客户触达，验证业务事件从 Notify API、Logic Router、Comet Push 到客户端 ACK 的完整链路。
          </p>
        </div>
        <div className="grid grid-cols-3 gap-2 rounded-lg border border-gray-200 bg-white p-2 shadow-sm">
          <MiniSignal label="链路目标" value="订单状态一致" />
          <MiniSignal label="可靠路径" value="直推 + 回退" />
          <MiniSignal label="用户体验" value="秒级可感知" />
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Metric icon={Activity} label="实时吞吐" value={stats ? `${stats.push_rate_per_sec.toFixed(0)}/s` : '—'} />
        <Metric icon={Radio} label="ACK 确认" value={stats ? `${(stats.ack_rate * 100).toFixed(0)}%` : '—'} />
        <Metric icon={Server} label="gRPC 直连" value={stats ? `${(stats.delivery_path.grpc_direct * 100).toFixed(0)}%` : '—'} />
        <Metric icon={Zap} label="WS 延迟" value={connState === 'connected' ? `${latency}ms` : '—'} />
      </div>

      <OrderScenarioConsole />

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <div className="space-y-6 lg:col-span-2">
          <MessageFlowViz />
          <EventStream />
        </div>
        <section className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
          <div className="flex items-center gap-2">
            <ShieldCheck size={17} className="text-emerald-600" />
            <h2 className="text-sm font-semibold text-gray-900">业务保障目标</h2>
          </div>
          <div className="mt-4 space-y-3 text-sm">
            <Goal label="状态一致" value={`${onlineStats?.offline_pending ?? 0} 条离线待补推`} tone="amber" />
            <Goal label="及时触达" value={stats ? `P99 ${stats.latency_p99_ms}ms` : '等待统计'} tone="blue" />
            <Goal
              label="服务韧性"
              value={stats ? `Kafka 回退 ${(stats.delivery_path.kafka_fallback * 100).toFixed(1)}%` : '等待统计'}
              tone="green"
            />
          </div>
        </section>
      </div>
    </div>
  )
}

function MiniSignal({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-[116px] px-3 py-2">
      <div className="text-[10px] font-medium uppercase text-gray-400">{label}</div>
      <div className="mt-1 text-xs font-semibold text-gray-800">{value}</div>
    </div>
  )
}

function Metric({
  icon: Icon,
  label,
  value,
}: {
  icon: LucideIcon
  label: string
  value: string
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-gray-200 bg-white px-4 py-3 shadow-sm">
      <Icon size={16} className="text-primary-600" />
      <div>
        <p className="text-[10px] uppercase text-gray-400">{label}</p>
        <p className="text-sm font-bold text-gray-800">{value}</p>
      </div>
    </div>
  )
}

function Goal({ label, value, tone }: { label: string; value: string; tone: 'amber' | 'blue' | 'green' }) {
  const dot = {
    amber: 'bg-amber-500',
    blue: 'bg-blue-500',
    green: 'bg-emerald-500',
  }[tone]

  return (
    <div className="flex items-center justify-between gap-3 rounded-lg border border-gray-100 bg-gray-50 px-3 py-2">
      <span className="flex items-center gap-2 text-gray-600">
        <span className={`h-2 w-2 rounded-full ${dot}`} />
        {label}
      </span>
      <strong className="text-right text-gray-900">{value}</strong>
    </div>
  )
}
