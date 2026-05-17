import { AlertTriangle, Clock, WifiOff } from 'lucide-react'

const alerts = [
  {
    id: '1',
    icon: Clock,
    title: '离线消息积压',
    detail: '用户 10005 有 12 条消息等待推送，已超过 2 分钟',
    severity: 'warning' as const,
  },
  {
    id: '2',
    icon: WifiOff,
    title: '设备断连恢复',
    detail: '用户 10002 Web 端在 30s 前断线后自动重连，offline sync 完成',
    severity: 'info' as const,
  },
  {
    id: '3',
    icon: AlertTriangle,
    title: 'Kafka 回退比例上升',
    detail: '最近 5 分钟 Kafka fallback 占比 18.5%，超过阈值 15%',
    severity: 'danger' as const,
  },
]

const severityStyles = {
  warning: 'border-l-amber-400 bg-amber-50/50',
  info: 'border-l-blue-400 bg-blue-50/50',
  danger: 'border-l-red-400 bg-red-50/50',
}

const iconStyles = {
  warning: 'text-amber-600',
  info: 'text-blue-600',
  danger: 'text-red-600',
}

export default function AlertCards() {
  return (
    <div className="space-y-2">
      <h3 className="text-sm font-semibold text-gray-700 mb-3">系统告警</h3>
      {alerts.map((alert) => {
        const Icon = alert.icon
        return (
          <div
            key={alert.id}
            className={`border-l-4 rounded-r-lg p-3 ${severityStyles[alert.severity]} animate-fade-in-up`}
          >
            <div className="flex items-start gap-2.5">
              <Icon size={15} className={`flex-shrink-0 mt-0.5 ${iconStyles[alert.severity]}`} />
              <div>
                <p className="text-xs font-medium text-gray-800">{alert.title}</p>
                <p className="text-[11px] text-gray-500 mt-0.5">{alert.detail}</p>
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}
