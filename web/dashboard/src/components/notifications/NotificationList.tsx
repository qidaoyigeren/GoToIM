import { useMemo } from 'react'
import { useNotificationStore } from '@/stores/notificationStore'
import type { Notification, NotifyType } from '@/types/notification'
import { NOTIFY_TYPE_LABELS } from '@/types/notification'
import NotificationCard from './NotificationCard'
import EmptyState from '@/components/ui/EmptyState'
import { BellOff } from 'lucide-react'

const EMPTY_NOTIFICATIONS: Notification[] = []

export default function NotificationList() {
  const notifications = useNotificationStore((s) => s.notifications) ?? EMPTY_NOTIFICATIONS
  const typeFilter = useNotificationStore((s) => s.typeFilter)
  const setTypeFilter = useNotificationStore((s) => s.setTypeFilter)
  const markAllRead = useNotificationStore((s) => s.markAllRead)
  const unreadCount = useNotificationStore((s) => s.unreadCount)

  const filtered = useMemo(() => {
    if (typeFilter === 'all') return notifications
    return notifications.filter((n) => n.type === typeFilter)
  }, [notifications, typeFilter])

  const types: (NotifyType | 'all')[] = ['all', 'order_status', 'flash_sale', 'logistics', 'system']

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-1.5">
          {types.map((t) => (
            <button
              key={t}
              onClick={() => setTypeFilter(t)}
              className={`px-3 py-1.5 text-xs font-medium rounded-lg transition-colors ${
                typeFilter === t
                  ? 'bg-primary-50 text-primary-700 border border-primary-200'
                  : 'text-gray-500 hover:bg-gray-100 border border-transparent'
              }`}
            >
              {t === 'all' ? '全部' : NOTIFY_TYPE_LABELS[t]}
            </button>
          ))}
        </div>
        {unreadCount > 0 && (
          <button
            onClick={markAllRead}
            className="text-xs text-primary-600 hover:text-primary-700 font-medium"
          >
            全部已读 ({unreadCount})
          </button>
        )}
      </div>

      {filtered.length === 0 ? (
        <EmptyState icon={BellOff} title="暂无通知" description="当有订单状态变更时，通知将在此显示" />
      ) : (
        <div className="space-y-2">
          {filtered.map((n) => (
            <NotificationCard key={n.notify_id} notification={n} />
          ))}
        </div>
      )}
    </div>
  )
}
