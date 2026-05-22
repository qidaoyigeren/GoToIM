import { useOnlineStatus } from '@/hooks/useOnlineStatus'
import { useOnlineStore } from '@/stores/onlineStore'
import SessionTable from '@/components/sessions/SessionTable'
import SessionDetailPanel from '@/components/sessions/SessionDetailPanel'
import { CARD_SM } from '@/components/ui/cardStyles'
import { Activity, Server, Radio } from 'lucide-react'

export default function SessionsPage() {
  useOnlineStatus()
  const stats = useOnlineStore((s) => s.stats)
  const sessions = useOnlineStore((s) => s.sessions)
  const roomCount = new Set(sessions.filter((session) => session.room_id).map((session) => session.room_id)).size

  return (
    <div className="space-y-6 animate-fade-in">
      <div>
        <h1 className="text-xl font-bold text-gray-900">在线会话与订阅监控</h1>
        <p className="text-sm text-gray-500 mt-1">
          实时展示连接状态、心跳、订阅房间与消息通道
        </p>
      </div>

      {/* Quick stats */}
      <div className="grid grid-cols-3 gap-4">
        <div className={`${CARD_SM} flex items-center gap-3`}>
          <div className="w-10 h-10 rounded-lg bg-blue-50 flex items-center justify-center">
            <Activity size={18} className="text-blue-600" />
          </div>
          <div>
            <p className="text-xs text-gray-400">活跃会话</p>
            <p className="text-lg font-bold text-gray-900">{sessions.filter((s) => s.online).length}</p>
          </div>
        </div>
        <div className={`${CARD_SM} flex items-center gap-3`}>
          <div className="w-10 h-10 rounded-lg bg-emerald-50 flex items-center justify-center">
            <Server size={18} className="text-emerald-600" />
          </div>
          <div>
            <p className="text-xs text-gray-400">在线用户</p>
            <p className="text-lg font-bold text-gray-900">{stats?.user_count?.toLocaleString() ?? '—'}</p>
          </div>
        </div>
        <div className={`${CARD_SM} flex items-center gap-3`}>
          <div className="w-10 h-10 rounded-lg bg-amber-50 flex items-center justify-center">
            <Radio size={18} className="text-amber-600" />
          </div>
          <div>
            <p className="text-xs text-gray-400">订阅房间</p>
            <p className="text-lg font-bold text-gray-900">{roomCount > 0 ? roomCount.toLocaleString() : '—'}</p>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-4 gap-6">
        <div className="lg:col-span-3">
          <SessionTable />
        </div>
        <div>
          <SessionDetailPanel />
        </div>
      </div>
    </div>
  )
}
