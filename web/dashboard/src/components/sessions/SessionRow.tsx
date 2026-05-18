import { useState } from 'react'
import type { Session } from '@/types/online'
import { Smartphone, Monitor, Globe } from 'lucide-react'

const platformIcons = {
  web: Monitor,
  android: Smartphone,
  ios: Smartphone,
}

type Props = { session: Session }

export default function SessionRow({ session }: Props) {
  const Icon = platformIcons[session.platform] || Globe
  const [now] = useState(() => Date.now())

  const timeAgo = (ts: number) => {
    const diff = now - ts
    if (diff < 5000) return '在线'
    const sec = Math.floor(diff / 1000)
    if (sec < 60) return `${sec}s 前`
    return `${Math.floor(sec / 60)}m 前`
  }

  const hbText = timeAgo(session.last_hb)

  return (
    <tr className="border-b border-gray-50 hover:bg-gray-50/50 transition-colors">
      <td className="py-3 px-4">
        <div className="flex items-center gap-2">
          <div className={`w-2 h-2 rounded-full ${session.online ? 'bg-green-500 animate-pulse-dot' : 'bg-gray-300'}`} />
          <span className="text-sm text-gray-800 font-medium">{session.uid}</span>
        </div>
      </td>
      <td className="py-3 px-4 text-xs text-gray-500 font-mono">{session.sid.slice(0, 12)}...</td>
      <td className="py-3 px-4">
        <div className="flex items-center gap-1.5">
          <Icon size={14} className="text-gray-400" />
          <span className="text-xs text-gray-600 capitalize">{session.platform}</span>
        </div>
      </td>
      <td className="py-3 px-4 text-xs text-gray-500">{session.device_id}</td>
      <td className="py-3 px-4 text-xs text-gray-400 font-mono">{session.server}</td>
      <td className="py-3 px-4">
        <span className={`text-xs font-medium ${session.online ? 'text-green-600' : 'text-gray-400'}`}>
          {session.online ? hbText : '离线'}
        </span>
      </td>
      <td className="py-3 px-4 text-xs text-gray-400 font-mono">{session.room_id}</td>
    </tr>
  )
}
