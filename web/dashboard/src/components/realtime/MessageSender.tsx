import { useState } from 'react'
import { useCreateOrder } from '@/hooks/useOrders'
import { useRealtimeStore } from '@/stores/realtimeStore'
import { Send, ShoppingCart } from 'lucide-react'

const mockItems = [
  { product_name: 'iPhone 16 Pro Max', quantity: 1, price: 9999 },
  { product_name: 'AirPods Pro 2', quantity: 1, price: 1899 },
  { product_name: 'MacBook Pro 14"', quantity: 1, price: 14999 },
]

export default function MessageSender() {
  const [selectedItem, setSelectedItem] = useState(0)
  const createOrder = useCreateOrder()
  const addEvent = useRealtimeStore((s) => s.addEvent)

  const handleCreateOrder = () => {
    const item = mockItems[selectedItem]
    createOrder.mutate(
      { items: [item], total: item.price * item.quantity },
      {
        onSuccess: (data) => {
          addEvent({
            id: `evt_${Date.now()}`,
            type: 'push_sent',
            msg_id: `msg_${Date.now()}`,
            order_id: data.order.order_id,
            title: '订单创建消息已发送',
            detail: `新订单 ${data.order.order_id} 已通过 gRPC 直连推送给用户`,
            timestamp: Date.now(),
            delivery_path: 'grpc_direct',
          })
        },
      }
    )
  }

  return (
    <div className="bg-white rounded-xl border border-gray-100 p-5 shadow-sm">
      <h3 className="text-sm font-semibold text-gray-700 mb-3">手动发送消息</h3>
      <p className="text-xs text-gray-400 mb-4">创建新订单并触发实时推送，观察消息链路</p>

      <div className="space-y-3">
        <div>
          <label className="text-xs font-medium text-gray-500">选择商品</label>
          <select
            value={selectedItem}
            onChange={(e) => setSelectedItem(Number(e.target.value))}
            className="mt-1 w-full text-sm border border-gray-200 rounded-lg px-3 py-2 bg-white"
          >
            {mockItems.map((item, i) => (
              <option key={i} value={i}>
                {item.product_name} — ¥{item.price.toLocaleString()}
              </option>
            ))}
          </select>
        </div>

        <button
          onClick={handleCreateOrder}
          disabled={createOrder.isPending}
          className="w-full inline-flex items-center justify-center gap-2 px-4 py-2.5 bg-primary-600 text-white text-sm font-medium rounded-lg hover:bg-primary-700 disabled:opacity-50 transition-colors"
        >
          <Send size={14} />
          {createOrder.isPending ? '发送中...' : '创建订单并推送消息'}
        </button>

        {createOrder.data && (
          <div className="p-3 bg-green-50 border border-green-200 rounded-lg text-xs text-green-700 animate-fade-in">
            <p className="font-medium">订单创建成功</p>
            <p className="mt-0.5">订单号: {createOrder.data.order.order_id}</p>
            <p>通知已推送至用户 {createOrder.data.order.user_id}</p>
          </div>
        )}
      </div>
    </div>
  )
}
