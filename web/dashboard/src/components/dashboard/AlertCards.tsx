import { AlertTriangle, Clock } from 'lucide-react'
import { useRealtimeStore } from '@/stores/realtimeStore'

type AlertSeverity = 'warning' | 'danger'

type AlertItem = {
  id: string
  icon: typeof AlertTriangle
  title: string
  detail: string
  severity: AlertSeverity
}

const severityStyles: Record<AlertSeverity, string> = {
  warning: 'border-l-amber-400 bg-amber-50/50',
  danger: 'border-l-red-400 bg-red-50/50',
}

const iconStyles: Record<AlertSeverity, string> = {
  warning: 'text-amber-600',
  danger: 'text-red-600',
}

export default function AlertCards() {
  const stats = useRealtimeStore((s) => s.stats)
  const offlineQueue = stats?.offline_pending ?? 0
  const kafkaFallbackRatio = stats?.delivery_path?.kafka_fallback ?? 0
  const alerts: AlertItem[] = []

  if (offlineQueue > 10) {
    alerts.push({
      id: 'offline_queue',
      icon: Clock,
      title: '离线队列积压',
      detail: `当前有 ${offlineQueue.toLocaleString()} 条离线消息等待补推，已超过阈值 10 条。`,
      severity: 'warning',
    })
  }

  if (kafkaFallbackRatio > 0.15) {
    alerts.push({
      id: 'kafka_fallback_ratio',
      icon: AlertTriangle,
      title: 'Kafka 回退比例过高',
      detail: `当前 Kafka fallback 占比 ${(kafkaFallbackRatio * 100).toFixed(1)}%，已超过阈值 15%。`,
      severity: 'danger',
    })
  }

  return (
    <div className="space-y-2">
      <h3 className="mb-3 text-sm font-semibold text-gray-700">系统告警</h3>
      {alerts.length === 0 && (
        <div className="rounded-xl border border-gray-100 bg-white px-4 py-5 text-sm text-gray-500 shadow-sm">
          暂无告警
        </div>
      )}
      {alerts.map((alert) => {
        const Icon = alert.icon
        return (
          <div
            key={alert.id}
            className={`animate-fade-in-up rounded-r-lg border-l-4 p-3 ${severityStyles[alert.severity]}`}
          >
            <div className="flex items-start gap-2.5">
              <Icon size={15} className={`mt-0.5 shrink-0 ${iconStyles[alert.severity]}`} />
              <div>
                <p className="text-xs font-medium text-gray-800">{alert.title}</p>
                <p className="mt-0.5 text-[11px] text-gray-500">{alert.detail}</p>
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}
