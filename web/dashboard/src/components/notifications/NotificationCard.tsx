import { useState } from 'react'
import { ChevronDown, ChevronUp, CheckCheck, Clock, Package, ShoppingBag, Truck, Zap } from 'lucide-react'
import { useAcknowledge } from '@/hooks/useNotifications'
import type { Notification, NotifyType } from '@/types/notification'
import { NOTIFY_TYPE_LABELS } from '@/types/notification'

const typeIcons: Record<NotifyType, typeof Clock> = {
  order_status: Package,
  purchase_order: ShoppingBag,
  flash_sale: Zap,
  logistics: Truck,
  system: Clock,
}

const typeColors: Record<NotifyType, string> = {
  order_status: 'bg-primary-50 text-primary-600',
  purchase_order: 'bg-blue-50 text-blue-600',
  flash_sale: 'bg-amber-50 text-amber-600',
  logistics: 'bg-emerald-50 text-emerald-600',
  system: 'bg-gray-100 text-gray-500',
}

type Props = { notification: Notification }

export default function NotificationCard({ notification }: Props) {
  const [expanded, setExpanded] = useState(false)
  const ackMutation = useAcknowledge()
  const Icon = typeIcons[notification.type] || Clock

  return (
    <div
      className={`rounded-lg border bg-white p-4 transition-all duration-200 hover:shadow-sm ${
        notification.status !== 'acked'
          ? 'border-l-2 border-l-primary-500 border-gray-100 shadow-sm'
          : 'border-gray-100'
      }`}
    >
      <div className="flex items-start gap-3">
        <div className={`flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg ${typeColors[notification.type]}`}>
          <Icon size={16} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-sm font-medium text-gray-800">{notification.title}</span>
              <span className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ${statusTone(notification.status)}`}>
                {statusLabel(notification.status)}
              </span>
              {notification.priority && (
                <span className="rounded-full bg-blue-50 px-1.5 py-0.5 text-[10px] font-medium text-blue-700">
                  {notification.priority}
                </span>
              )}
            </div>
            <span className="flex-shrink-0 text-[10px] text-gray-400">
              {new Date(notification.created_at).toLocaleString('zh-CN')}
            </span>
          </div>
          <p className="mt-1 text-sm text-gray-500">{notification.content}</p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <span className="rounded bg-gray-50 px-1.5 py-0.5 text-[10px] text-gray-400">
              {NOTIFY_TYPE_LABELS[notification.type]}
            </span>
            {notification.order_id && (
              <span className="font-mono text-[10px] text-gray-400">{notification.order_id}</span>
            )}
            {notification.ack_policy && (
              <span className="text-[10px] text-gray-400">ACK {notification.ack_policy}</span>
            )}
          </div>

          {expanded && (
            <div className="mt-3 space-y-1 border-t border-gray-100 pt-3 text-xs text-gray-500 animate-fade-in">
              <p>通知 ID: <span className="font-mono">{notification.notify_id}</span></p>
              <p>目标用户: {notification.user_id}</p>
              <p>状态: {notification.status} / {NOTIFY_TYPE_LABELS[notification.type]}</p>
              {notification.priority && (
                <p>策略: {notification.priority} / TTL {notification.ttl_seconds ?? '-'}s / ACK {notification.ack_policy ?? '-'}</p>
              )}
            </div>
          )}
        </div>

        <div className="flex flex-shrink-0 flex-col items-center gap-1">
          {notification.status !== 'acked' && (
            <button
              onClick={() => ackMutation.mutate(notification.notify_id)}
              className="rounded-md p-1.5 text-gray-400 transition-colors hover:bg-green-50 hover:text-green-600"
              title="标记已读 / ACK"
            >
              <CheckCheck size={14} />
            </button>
          )}
          <button
            onClick={() => setExpanded(!expanded)}
            className="rounded-md p-1.5 text-gray-400 transition-colors hover:bg-gray-100"
          >
            {expanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
          </button>
        </div>
      </div>
    </div>
  )
}

function statusLabel(status: Notification['status']) {
  return {
    acked: '已 ACK',
    delivered: '已送达',
    queued: '已入队',
    failed: '失败',
    pending: '等待投递',
  }[status] ?? status
}

function statusTone(status: Notification['status']) {
  if (status === 'acked') return 'bg-green-50 text-green-600'
  if (status === 'delivered' || status === 'queued') return 'bg-blue-50 text-blue-600'
  if (status === 'failed') return 'bg-red-50 text-red-600'
  return 'bg-amber-50 text-amber-600'
}
