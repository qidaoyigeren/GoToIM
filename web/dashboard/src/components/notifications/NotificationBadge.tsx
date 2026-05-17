import { useNotificationStore } from '@/stores/notificationStore'

export default function NotificationBadge() {
  const count = useNotificationStore((s) => s.unreadCount)
  if (count === 0) return null
  return (
    <span className="inline-flex items-center justify-center w-5 h-5 rounded-full bg-red-500 text-white text-[10px] font-bold">
      {count > 99 ? '99+' : count}
    </span>
  )
}
