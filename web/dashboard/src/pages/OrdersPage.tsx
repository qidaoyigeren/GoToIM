import { useNavigate } from 'react-router-dom'
import OrderTable from '@/components/orders/OrderTable'
import { useOrders, useCreateOrder } from '@/hooks/useOrders'
import { useOrderStore } from '@/stores/orderStore'
import { Plus } from 'lucide-react'

export default function OrdersPage() {
  const { isLoading } = useOrders()
  const orders = useOrderStore((s) => s.orders) ?? []
  const createOrder = useCreateOrder()
  const navigate = useNavigate()

  const handleQuickCreate = () => {
    createOrder.mutate(
      {
        items: [{ product_name: 'iPhone 16 Pro Max', quantity: 1, price: 9999 }],
        total: 9999,
      },
      {
        onSuccess: (data) => {
          navigate(`/orders/${data.order.order_id}`)
        },
      }
    )
  }

  return (
    <div className="space-y-6 animate-fade-in">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-gray-900">订单管理</h1>
          <p className="text-sm text-gray-500 mt-1">
            {isLoading ? '加载中...' : `共 ${orders.length} 个订单`}
          </p>
        </div>
        <button
          onClick={handleQuickCreate}
          disabled={createOrder.isPending}
          className="inline-flex items-center gap-2 px-4 py-2.5 bg-primary-600 text-white text-sm font-medium rounded-lg hover:bg-primary-700 disabled:opacity-50 transition-colors shadow-sm"
        >
          <Plus size={16} />
          {createOrder.isPending ? '创建中...' : '快速创建订单'}
        </button>
      </div>

      <OrderTable />
    </div>
  )
}
