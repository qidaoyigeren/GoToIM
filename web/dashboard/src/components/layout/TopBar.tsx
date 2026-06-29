import ConnectionBadge from './ConnectionBadge'
import { useConnectionStore } from '@/stores/connectionStore'
import { useOnlineStore } from '@/stores/onlineStore'
import { usePlatformStats } from '@/hooks/usePlatformStats'
import { useOnlineStatus } from '@/hooks/useOnlineStatus'
import { isDemoMode, toggleDemoMode } from '@/config'
import { useIdentityStore } from '@/stores/identityStore'
import { Monitor, Server, UserRound } from 'lucide-react'

export default function TopBar() {
  usePlatformStats()
  useOnlineStatus()
  const state = useConnectionStore((s) => s.state)
  const stats = useOnlineStore((s) => s.stats)
  const role = useIdentityStore((s) => s.role)
  const userId = useIdentityStore((s) => s.userId)
  const setRole = useIdentityStore((s) => s.setRole)

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
        <div className="hidden items-center gap-4 text-xs text-gray-500 xl:flex">
          <span>
            在线用户 <strong className="text-gray-700">{stats?.user_count?.toLocaleString() ?? '-'}</strong>
          </span>
          <span>
            连接数 <strong className="text-gray-700">{stats?.conn_count?.toLocaleString() ?? '-'}</strong>
          </span>
          <span>
            离线待补 <strong className="text-gray-700">{stats?.offline_pending ?? '-'}</strong>
          </span>
          <span>
            direct push <strong className="text-emerald-600">{(stats?.direct_pushed ?? 0).toLocaleString()}</strong>
          </span>
          <span>
            kafka fallback <strong className="text-amber-600">{(stats?.kafka_fallback ?? 0).toLocaleString()}</strong>
          </span>
        </div>
      </div>

      <div className="flex items-center gap-3">
        <div className="flex items-center rounded-lg border border-gray-100 bg-gray-50 p-0.5">
          <button
            type="button"
            onClick={() => setRole('customer')}
            title="使用买家身份 UID 10001"
            className={`inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
              role === 'customer' ? 'bg-white text-blue-700 shadow-sm' : 'text-gray-500 hover:text-gray-800'
            }`}
          >
            <UserRound size={12} />
            买家
          </button>
          <button
            type="button"
            onClick={() => setRole('merchant')}
            title="使用商家身份 UID 90001"
            className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
              role === 'merchant' ? 'bg-white text-emerald-700 shadow-sm' : 'text-gray-500 hover:text-gray-800'
            }`}
          >
            商家
          </button>
        </div>
        <span className="font-mono text-xs text-gray-400">UID {userId}</span>
        <button
          onClick={handleToggle}
          title={effectiveDemoMode ? '当前使用 Mock 数据，点击切换到真实后端' : '当前使用真实后端，点击切换到 Mock 数据'}
          className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium transition-colors ${
            effectiveDemoMode
              ? 'bg-purple-50 text-purple-600 border border-purple-200 hover:bg-purple-100'
              : 'bg-gray-50 text-gray-500 border border-gray-200 hover:bg-gray-100'
          }`}
        >
          {effectiveDemoMode ? <Monitor size={12} /> : <Server size={12} />}
          {effectiveDemoMode ? 'Mock 数据' : '真实后端'}
        </button>

        <div className={`w-2 h-2 rounded-full ${state === 'connected' ? 'bg-green-500' : state === 'reconnecting' ? 'bg-orange-400 animate-pulse' : 'bg-gray-300'}`} />
        <span className="text-xs text-gray-400 font-mono">
          {new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
        </span>
      </div>
    </header>
  )
}
