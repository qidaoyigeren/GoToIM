import { useOnlineStore } from '@/stores/onlineStore'
import SessionRow from './SessionRow'
import EmptyState from '@/components/ui/EmptyState'
import { WifiOff } from 'lucide-react'

export default function SessionTable() {
  const sessions = useOnlineStore((s) => s.sessions)

  if (sessions.length === 0) {
    return <EmptyState icon={WifiOff} title="暂无活跃会话" description="等待用户连接或开启模拟" />
  }

  return (
    <div className="bg-white rounded-xl border border-gray-100 shadow-sm overflow-auto">
      <table className="w-full">
        <thead>
          <tr className="border-b border-gray-100">
            <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">用户</th>
            <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">会话 ID</th>
            <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">平台</th>
            <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">设备</th>
            <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">Comet 节点</th>
            <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">状态</th>
            <th className="text-left text-[10px] font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">房间</th>
          </tr>
        </thead>
        <tbody>
          {sessions.map((s) => (
            <SessionRow key={s.sid} session={s} />
          ))}
        </tbody>
      </table>
    </div>
  )
}
