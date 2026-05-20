import { create } from 'zustand'
import type { Notification, NotifyType } from '@/types/notification'

type NotificationStoreState = {
  notifications: Notification[]
  unreadCount: number
  typeFilter: NotifyType | 'all'
  setNotifications: (list: Notification[]) => void
  addNotification: (n: Notification) => void
  markAsRead: (notifyId: string) => void
  markAllRead: () => void
  setTypeFilter: (t: NotifyType | 'all') => void
  getFiltered: () => Notification[]
}

export const useNotificationStore = create<NotificationStoreState>((set, get) => ({
  notifications: [],
  unreadCount: 0,
  typeFilter: 'all',
  setNotifications: (list) => {
    const safeList = Array.isArray(list) ? list : []
    set({ notifications: safeList, unreadCount: safeList.filter((n) => n.status !== 'acked').length })
  },
  addNotification: (n) =>
    set((s) => ({
      notifications: [n, ...s.notifications],
      unreadCount: s.unreadCount + (n.status !== 'acked' ? 1 : 0),
    })),
  markAsRead: (notifyId) =>
    set((s) => {
      const list = s.notifications.map((n) =>
        n.notify_id === notifyId ? { ...n, status: 'acked' as const } : n
      )
      return { notifications: list, unreadCount: list.filter((n) => n.status !== 'acked').length }
    }),
  markAllRead: () =>
    set((s) => ({
      notifications: s.notifications.map((n) => ({ ...n, status: 'acked' as const })),
      unreadCount: 0,
    })),
  setTypeFilter: (t) => set({ typeFilter: t }),
  getFiltered: () => {
    const { notifications, typeFilter } = get()
    return typeFilter === 'all' ? notifications : notifications.filter((n) => n.type === typeFilter)
  },
}))
