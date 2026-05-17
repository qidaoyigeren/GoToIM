import { useNotifications } from '@/hooks/useNotifications'
import NotificationList from '@/components/notifications/NotificationList'
import { useNotificationStore } from '@/stores/notificationStore'
import Skeleton from '@/components/ui/Skeleton'
import ErrorState from '@/components/ui/ErrorState'

export default function NotificationsPage() {
  const { isLoading, error, refetch } = useNotifications()
  const unreadCount = useNotificationStore((s) => s.unreadCount)

  return (
    <div className="space-y-6 animate-fade-in">
      <div>
        <h1 className="text-xl font-bold text-gray-900">通知中心</h1>
        <p className="text-sm text-gray-500 mt-1">
          {isLoading ? '加载中...' : `${unreadCount} 条未读通知`}
        </p>
      </div>

      {error ? (
        <ErrorState message="加载通知失败" onRetry={() => refetch()} />
      ) : isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      ) : (
        <NotificationList />
      )}
    </div>
  )
}
