import { useState } from 'react'
import { useStartSimulation, useStopSimulation, useSimulationStatus } from '@/hooks/useSimulation'
import { Play, Square, Zap, Loader2 } from 'lucide-react'

const MODES = [
  { value: 'normal', label: '常规流量', desc: '随机创建订单 + 状态变更' },
  { value: 'lifecycle', label: '订单生命周期', desc: '单订单走完完整状态机' },
  { value: 'peak', label: '峰值压测', desc: '高 QPS 大批量订单事件' },
  { value: 'flash_sale', label: '闪购广播', desc: '批量推送闪购通知' },
]

export default function BatchSimulator() {
  const [qps, setQps] = useState(10)
  const [users, setUsers] = useState(100)
  const [mode, setMode] = useState('normal')

  const { data: simStatus, refetch } = useSimulationStatus()
  const startMutation = useStartSimulation()
  const stopMutation = useStopSimulation()

  const isRunning = simStatus?.active ?? false
  const isLoading = startMutation.isPending || stopMutation.isPending

  const handleToggle = () => {
    if (isRunning) {
      stopMutation.mutate(undefined, { onSuccess: () => refetch() })
    } else {
      startMutation.mutate({ mode, qps, users }, { onSuccess: () => refetch() })
    }
  }

  return (
    <div className="bg-white rounded-xl border border-gray-100 p-5 shadow-sm">
      <h3 className="text-sm font-semibold text-gray-700 mb-3">后端流量模拟器</h3>
      <p className="text-xs text-gray-400 mb-4">
        {isRunning
          ? `运行中 · 模式 ${simStatus?.mode} · QPS ${simStatus?.qps} · 已运行 ${simStatus?.uptime_seconds}s`
          : '调用 Notify Server 真实模拟引擎，生成订单事件并走完整推送链路'}
      </p>

      <div className="space-y-3">
        {/* Mode selector */}
        <div>
          <label className="text-xs font-medium text-gray-500">模拟模式</label>
          <select
            value={mode}
            onChange={(e) => setMode(e.target.value)}
            disabled={isRunning}
            className="mt-1 w-full text-xs border border-gray-200 rounded-lg px-3 py-2 bg-white disabled:opacity-50"
          >
            {MODES.map((m) => (
              <option key={m.value} value={m.value}>{m.label} — {m.desc}</option>
            ))}
          </select>
        </div>

        {/* QPS slider */}
        <div>
          <label className="text-xs font-medium text-gray-500">QPS（每秒请求数）</label>
          <input
            type="range"
            min={1}
            max={500}
            value={qps}
            onChange={(e) => setQps(Number(e.target.value))}
            disabled={isRunning}
            className="w-full mt-1 disabled:opacity-50"
          />
          <div className="flex justify-between text-xs text-gray-400">
            <span>1</span>
            <span className="font-mono font-medium text-gray-600">{qps}</span>
            <span>500</span>
          </div>
        </div>

        {/* Users */}
        <div>
          <label className="text-xs font-medium text-gray-500">模拟用户数</label>
          <input
            type="number"
            min={10}
            max={100000}
            value={users}
            onChange={(e) => setUsers(Number(e.target.value))}
            disabled={isRunning}
            className="mt-1 w-full text-xs border border-gray-200 rounded-lg px-3 py-2 bg-white disabled:opacity-50"
          />
        </div>

        {/* Start/Stop */}
        <button
          onClick={handleToggle}
          disabled={isLoading}
          className={`w-full inline-flex items-center justify-center gap-2 px-4 py-2.5 text-sm font-medium rounded-lg transition-colors ${
            isRunning
              ? 'bg-red-50 text-red-600 border border-red-200 hover:bg-red-100'
              : 'bg-emerald-50 text-emerald-600 border border-emerald-200 hover:bg-emerald-100'
          } disabled:opacity-50`}
        >
          {isLoading ? <Loader2 size={14} className="animate-spin" /> : isRunning ? <Square size={14} /> : <Play size={14} />}
          {isLoading ? '请求中...' : isRunning ? '停止模拟' : '启动后端模拟'}
        </button>

        {isRunning && (
          <div className="flex items-center gap-2 text-xs text-emerald-600 font-medium">
            <Zap size={12} className="animate-pulse" />
            后端模拟引擎运行中，观察 Dashboard 和事件流...
          </div>
        )}

        {startMutation.isError && (
          <div className="text-xs text-red-500">
            启动失败：{(startMutation.error as Error)?.message || '未知错误'}
          </div>
        )}
      </div>
    </div>
  )
}
