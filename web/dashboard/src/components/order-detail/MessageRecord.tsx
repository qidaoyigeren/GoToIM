import type { Notification } from '@/types/notification'
import { NOTIFY_TYPE_LABELS } from '@/types/notification'
import { MessageSquare } from 'lucide-react'

type Props = { notification: Notification }

export default function MessageRecord({ notification }: Props) {
  return (
    <div className="flex items-start gap-3 p-3 bg-gray-50 rounded-lg border border-gray-100">
      <div className="w-8 h-8 rounded-lg bg-primary-50 flex items-center justify-center flex-shrink-0">
        <MessageSquare size={14} className="text-primary-600" />
      </div>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-xs font-medium text-gray-800">{notification.title}</span>
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-gray-200 text-gray-500">
            {NOTIFY_TYPE_LABELS[notification.type]}
          </span>
        </div>
        <p className="text-xs text-gray-500 mt-0.5">{notification.content}</p>
        <div className="flex items-center gap-3 mt-1.5">
          <span className="text-[10px] text-gray-400">
            {new Date(notification.created_at).toLocaleString('zh-CN')}
          </span>
          <span className={`text-[10px] font-medium ${
            notification.status === 'acked' ? 'text-green-600' :
            notification.status === 'delivered' ? 'text-blue-600' :
            notification.status === 'failed' ? 'text-red-500' :
            'text-amber-600'
          }`}>
            {notification.status === 'acked' ? '已 ACK' :
             notification.status === 'delivered' ? '已送达' :
             notification.status === 'failed' ? '失败' : '等待中'}
          </span>
        </div>
      </div>
    </div>
  )
}
