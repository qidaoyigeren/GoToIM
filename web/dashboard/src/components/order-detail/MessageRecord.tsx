import { MessageSquare } from 'lucide-react'
import type { Notification } from '@/types/notification'
import { NOTIFY_TYPE_LABELS } from '@/types/notification'

type Props = { notification: Notification }

export default function MessageRecord({ notification }: Props) {
  return (
    <div className="flex items-start gap-3 rounded-lg border border-gray-100 bg-gray-50 p-3">
      <div className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg bg-blue-50">
        <MessageSquare size={14} className="text-blue-600" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs font-semibold text-gray-800">{notification.title}</span>
          <span className="rounded bg-gray-200 px-1.5 py-0.5 text-[10px] text-gray-500">
            {NOTIFY_TYPE_LABELS[notification.type] ?? notification.type}
          </span>
          {notification.priority && (
            <span className="rounded bg-blue-100 px-1.5 py-0.5 text-[10px] text-blue-700">
              {notification.priority}
            </span>
          )}
        </div>
        <p className="mt-0.5 text-xs text-gray-500">{notification.content}</p>
        <div className="mt-1.5 flex flex-wrap items-center gap-3">
          <span className="text-[10px] text-gray-400">
            {new Date(notification.created_at).toLocaleString('zh-CN')}
          </span>
          <span className={`text-[10px] font-medium ${statusColor(notification.status)}`}>
            {statusLabel(notification.status)}
          </span>
          {notification.ack_policy && (
            <span className="text-[10px] text-gray-400">ACK {notification.ack_policy}</span>
          )}
        </div>
      </div>
    </div>
  )
}

function statusLabel(status: Notification['status']) {
  return {
    pending: '等待投递',
    queued: '已入队',
    delivered: '已送达',
    acked: '已 ACK',
    failed: '失败',
  }[status] ?? status
}

function statusColor(status: Notification['status']) {
  if (status === 'acked') return 'text-green-600'
  if (status === 'delivered') return 'text-blue-600'
  if (status === 'failed') return 'text-red-500'
  return 'text-amber-600'
}
