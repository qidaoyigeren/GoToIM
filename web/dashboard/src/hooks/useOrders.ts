import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  changeOrderStatus,
  createPurchaseOrder,
  getPurchaseOrder,
  getUserPurchaseOrders,
  listMerchants,
  listProducts,
} from '@/api/notify'
import { useNotificationStore } from '@/stores/notificationStore'
import { useOrderStore } from '@/stores/orderStore'
import type { OrderStatus, PurchaseOrderInput } from '@/types/order'
import config from '@/config'

type LegacyCreateOrderInput = {
  items: Array<{ product_name: string; quantity: number; price: number }>
  total?: number
  userId?: string
}

type CreateOrderInput = PurchaseOrderInput | LegacyCreateOrderInput

export function useOrders(userId?: string) {
  const uid = userId || config.defaultUserId
  const setOrders = useOrderStore((s) => s.setOrders)

  return useQuery({
    queryKey: ['purchase-orders', uid],
    queryFn: async () => {
      const orders = await getUserPurchaseOrders(uid)
      setOrders(Array.isArray(orders) ? orders : [])
      return orders ?? []
    },
    refetchInterval: 15000,
  })
}

export function useOrderDetail(orderId: string) {
  return useQuery({
    queryKey: ['purchase-order', orderId],
    queryFn: () => getPurchaseOrder(orderId),
    enabled: !!orderId,
  })
}

export function useMarket() {
  const merchants = useQuery({
    queryKey: ['market', 'merchants'],
    queryFn: listMerchants,
  })
  const activeMerchantId = merchants.data?.[0]?.merchant_id
  const products = useQuery({
    queryKey: ['market', 'products', activeMerchantId],
    queryFn: () => listProducts(activeMerchantId),
    enabled: !!activeMerchantId,
  })
  return { merchants, products }
}

export function useCreateOrder() {
  const queryClient = useQueryClient()
  const addOrder = useOrderStore((s) => s.addOrder)
  const addNotification = useNotificationStore((s) => s.addNotification)

  return useMutation({
    mutationFn: (input: CreateOrderInput) => createPurchaseOrder(toPurchaseOrderInput(input)),
    onSuccess: (data) => {
      addOrder(data.order)
      data.notifications?.forEach(addNotification)
      queryClient.invalidateQueries({ queryKey: ['purchase-orders'] })
      queryClient.invalidateQueries({ queryKey: ['orders'] })
    },
  })
}

function toPurchaseOrderInput(input: CreateOrderInput): PurchaseOrderInput {
  if ('merchant_id' in input) return input
  return {
    user_id: input.userId || config.defaultUserId,
    merchant_id: 'm_apple_store',
    merchant_uid: 90001,
    order_type: 'normal',
    importance: 'high',
    buyer_note: input.items.map((item) => item.product_name).join(', '),
    items: [
      {
        product_id: 'p_iphone_case',
        quantity: input.items[0]?.quantity || 1,
      },
    ],
  }
}

export function useChangeOrderStatus() {
  const queryClient = useQueryClient()
  const updateOrderStatus = useOrderStore((s) => s.updateOrderStatus)
  const addNotification = useNotificationStore((s) => s.addNotification)

  return useMutation({
    mutationFn: (params: {
      orderId: string
      newStatus: OrderStatus
      extra?: Record<string, string>
    }) => changeOrderStatus(params.orderId, params.newStatus, params.extra),
    onSuccess: (data, variables) => {
      updateOrderStatus(variables.orderId, variables.newStatus, data.order.updated_at || new Date().toISOString())
      addNotification(data.notification)
      queryClient.invalidateQueries({ queryKey: ['purchase-order', variables.orderId] })
      queryClient.invalidateQueries({ queryKey: ['purchase-orders'] })
    },
  })
}
