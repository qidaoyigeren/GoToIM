import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  getOrder,
  getUserOrders,
  createOrder,
  changeOrderStatus,
} from '@/api/notify'
import { useOrderStore } from '@/stores/orderStore'
import { useNotificationStore } from '@/stores/notificationStore'
import config from '@/config'

export function useOrders(userId?: string) {
  const uid = userId || config.defaultUserId
  const setOrders = useOrderStore((s) => s.setOrders)

  return useQuery({
    queryKey: ['orders', uid],
    queryFn: async () => {
      const orders = await getUserOrders(uid)
      if (Array.isArray(orders)) {
        setOrders(orders)
      }
      return orders ?? []
    },
    refetchInterval: 15000,
  })
}

export function useOrderDetail(orderId: string) {
  return useQuery({
    queryKey: ['order', orderId],
    queryFn: () => getOrder(orderId),
    enabled: !!orderId,
  })
}

export function useCreateOrder() {
  const queryClient = useQueryClient()
  const addOrder = useOrderStore((s) => s.addOrder)
  const addNotification = useNotificationStore((s) => s.addNotification)
  const uid = config.defaultUserId

  return useMutation({
    mutationFn: (params: {
      items: { product_name: string; quantity: number; price: number }[]
      total: number
      userId?: string
    }) => createOrder(params.userId || uid, params.items, params.total),
    onSuccess: (data) => {
      addOrder(data.order)
      addNotification(data.notification)
      queryClient.invalidateQueries({ queryKey: ['orders'] })
    },
  })
}

export function useChangeOrderStatus() {
  const queryClient = useQueryClient()
  const updateOrderStatus = useOrderStore((s) => s.updateOrderStatus)
  const addNotification = useNotificationStore((s) => s.addNotification)

  return useMutation({
    mutationFn: (params: {
      orderId: string
      newStatus: import('@/types/order').OrderStatus
      extra?: Record<string, string>
    }) => changeOrderStatus(params.orderId, params.newStatus, params.extra),
    onSuccess: (data, variables) => {
      updateOrderStatus(variables.orderId, variables.newStatus, new Date().toISOString())
      addNotification(data.notification)
      queryClient.invalidateQueries({ queryKey: ['order', variables.orderId] })
      queryClient.invalidateQueries({ queryKey: ['orders'] })
    },
  })
}
