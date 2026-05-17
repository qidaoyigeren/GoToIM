import ConnectionBadge from './ConnectionBadge'
import { useConnectionStore } from '@/stores/connectionStore'
import { useOnlineStore } from '@/stores/onlineStore'
import { usePlatformStats } from '@/hooks/usePlatformStats'
import { useOnlineStatus } from '@/hooks/useOnlineStatus'
import { isDemoMode, toggleDemoMode } from '@/config'
import { Monitor, Server } from 'lucide-react'

export default function TopBar() {
  usePlatformStats()
  useOnlineStatus()
  const state = useConnectionStore((s) => s.state)
  const stats = useOnlineStore((s) => s.stats)

  const effectiveDemoMode = isDemoMode()

  const handleToggle = () => {
    toggleDemoMode()
    window.location.reload()
  }

  return (
    <header className="h-14 bg-white border-b border-gray-200 flex items-center justify-between px-6">
      <div className="flex items-center gap-4">
        <ConnectionBadge />
        <div className="h-4 w-px bg-gray-200" />
        <div className="flex items-center gap-4 text-xs text-gray-500">
          <span>
            在线用户 <strong className="text-gray-700">{stats?.user_count?.toLocaleString() ?? '—'}</strong>
          </span>
          <span>
            连接数 <strong className="text-gray-700">{stats?.conn_count?.toLocaleString() ?? '—'}</strong>
          </span>
          <span>
            离线待补 <strong className="text-gray-700">{stats?.offline_pending ?? '—'}</strong>
          </span>
          <span>
            gRPC直连 <strong className="text-emerald-600">{(stats?.direct_pushed ?? 0).toLocaleString()}</strong>
          </span>
          <span>
            Kafka回退 <strong className="text-amber-600">{(stats?.kafka_fallback ?? 0).toLocaleString()}</strong>
          </span>
        </div>
      </div>

      <div className="flex items-center gap-3">
        <button
          onClick={handleToggle}
          title={effectiveDemoMode ? '当前：演示模式（Mock 数据）— 点击切换为真实后端' : '当前：真实后端 — 点击切换为演示模式'}
          className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium transition-colors ${
            effectiveDemoMode
              ? 'bg-purple-50 text-purple-600 border border-purple-200 hover:bg-purple-100'
              : 'bg-gray-50 text-gray-500 border border-gray-200 hover:bg-gray-100'
          }`}
        >
          {effectiveDemoMode ? <Monitor size={12} /> : <Server size={12} />}
          {effectiveDemoMode ? '演示模式' : '真实后端'}
        </button>

        <div className={`w-2 h-2 rounded-full ${state === 'connected' ? 'bg-green-500' : state === 'reconnecting' ? 'bg-orange-400 animate-pulse' : 'bg-gray-300'}`} />
        <span className="text-xs text-gray-400 font-mono">
          {new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
        </span>
      </div>
    </header>
  )
}
