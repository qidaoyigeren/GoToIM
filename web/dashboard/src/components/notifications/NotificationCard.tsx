import { useState } from 'react'
import type { Notification } from '@/types/notification'
import { NOTIFY_TYPE_LABELS } from '@/types/notification'
import { useAcknowledge } from '@/hooks/useNotifications'
import { ChevronDown, ChevronUp, CheckCheck, Clock, Package, Zap, Truck } from 'lucide-react'

const typeIcons = {
  order_status: Package,
  flash_sale: Zap,
  logistics: Truck,
  system: Clock,
}

const typeColors = {
  order_status: 'bg-primary-50 text-primary-600',
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
      className={`bg-white rounded-lg border p-4 transition-all duration-200 hover:shadow-sm ${
        notification.status !== 'acked'
          ? 'border-l-2 border-l-primary-500 border-gray-100'
          : 'border-gray-100'
      } ${notification.status !== 'acked' ? 'shadow-sm' : ''}`}
    >
      <div className="flex items-start gap-3">
        <div className={`w-9 h-9 rounded-lg flex items-center justify-center flex-shrink-0 ${typeColors[notification.type]}`}>
          <Icon size={16} />
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium text-gray-800">{notification.title}</span>
              <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium ${
                notification.status === 'acked' ? 'bg-green-50 text-green-600' :
                notification.status === 'delivered' ? 'bg-blue-50 text-blue-600' :
                'bg-amber-50 text-amber-600'
              }`}>
                {notification.status === 'acked' ? '已读' :
                 notification.status === 'delivered' ? '已送达' : '待处理'}
              </span>
            </div>
            <span className="text-[10px] text-gray-400 flex-shrink-0">
              {new Date(notification.created_at).toLocaleString('zh-CN')}
            </span>
          </div>
          <p className="text-sm text-gray-500 mt-1">{notification.content}</p>
          <div className="flex items-center gap-2 mt-2">
            <span className="text-[10px] text-gray-400 bg-gray-50 px-1.5 py-0.5 rounded">
              {NOTIFY_TYPE_LABELS[notification.type]}
            </span>
            {notification.order_id && (
              <span className="text-[10px] text-gray-400 font-mono">{notification.order_id}</span>
            )}
          </div>

          {expanded && (
            <div className="mt-3 pt-3 border-t border-gray-100 text-xs text-gray-500 space-y-1 animate-fade-in">
              <p>通知 ID: <span className="font-mono">{notification.notify_id}</span></p>
              <p>用户 ID: {notification.user_id}</p>
              <p>状态: {notification.status} / {NOTIFY_TYPE_LABELS[notification.type]}</p>
            </div>
          )}
        </div>

        <div className="flex flex-col items-center gap-1 flex-shrink-0">
          {notification.status !== 'acked' && (
            <button
              onClick={() => ackMutation.mutate(notification.notify_id)}
              className="p-1.5 rounded-md hover:bg-green-50 text-gray-400 hover:text-green-600 transition-colors"
              title="标记已读 / ACK"
            >
              <CheckCheck size={14} />
            </button>
          )}
          <button
            onClick={() => setExpanded(!expanded)}
            className="p-1.5 rounded-md hover:bg-gray-100 text-gray-400 transition-colors"
          >
            {expanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
          </button>
        </div>
      </div>
    </div>
  )
}
