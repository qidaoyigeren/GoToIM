import { useConnectionStore, type ConnectionState } from '@/stores/connectionStore'
import { Wifi, WifiOff, Loader2, AlertTriangle } from 'lucide-react'

const config: Record<ConnectionState, { label: string; icon: typeof Wifi; className: string }> = {
  disconnected: { label: 'WS 未连接', icon: WifiOff, className: 'text-gray-400 bg-gray-100' },
  connecting: { label: 'WS 连接中...', icon: Loader2, className: 'text-yellow-600 bg-yellow-50' },
  connected: { label: 'WS 已连接', icon: Wifi, className: 'text-green-700 bg-green-50' },
  reconnecting: { label: 'WS 重连中...', icon: AlertTriangle, className: 'text-orange-600 bg-orange-50' },
}

export default function ConnectionBadge() {
  const state = useConnectionStore((s) => s.state)
  const latencyMs = useConnectionStore((s) => s.latencyMs)
  const reconnectCount = useConnectionStore((s) => s.reconnectCount)
  const { label, icon: Icon, className } = config[state]

  return (
    <div className={`flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium ${className}`}>
      <Icon size={12} className={state === 'connecting' || state === 'reconnecting' ? 'animate-spin' : ''} />
      <span>{label}</span>
      {state === 'connected' && latencyMs > 0 && (
        <span className="opacity-75">{latencyMs}ms</span>
      )}
      {reconnectCount > 0 && (
        <span className="opacity-60">(重连{reconnectCount}次)</span>
      )}
    </div>
  )
}
