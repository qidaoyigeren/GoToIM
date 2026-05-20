import { useQuery, useMutation } from '@tanstack/react-query'
import { getUserNotifications, sendAck } from '@/api/notify'
import { useNotificationStore } from '@/stores/notificationStore'
import config from '@/config'

export function useNotifications(userId?: string) {
  const uid = userId || config.defaultUserId
  const setNotifications = useNotificationStore((s) => s.setNotifications)

  return useQuery({
    queryKey: ['notifications', uid],
    queryFn: async () => {
      const list = await getUserNotifications(uid)
      const safe = list ?? []
      setNotifications(safe)
      return safe
    },
    refetchInterval: 10000,
  })
}

export function useAcknowledge() {
  const markAsRead = useNotificationStore((s) => s.markAsRead)

  return useMutation({
    mutationFn: (notifyId: string) => sendAck(notifyId),
    onSuccess: (_data, notifyId) => {
      markAsRead(notifyId)
    },
  })
}
