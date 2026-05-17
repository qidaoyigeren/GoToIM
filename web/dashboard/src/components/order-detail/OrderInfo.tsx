import type { Order } from '@/types/order'
import StatusBadge from '@/components/ui/StatusBadge'

type Props = { order: Order }

export default function OrderInfo({ order }: Props) {
  return (
    <div className="bg-white rounded-xl border border-gray-100 p-6 shadow-sm">
      <div className="flex items-start justify-between mb-5">
        <div>
          <h2 className="text-lg font-bold text-gray-900">{order.order_id}</h2>
          <p className="text-sm text-gray-500 mt-0.5">用户 {order.user_id}</p>
        </div>
        <StatusBadge status={order.status} size="md" />
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <p className="text-xs text-gray-400 uppercase tracking-wider">创建时间</p>
          <p className="text-sm font-medium text-gray-700 mt-0.5">
            {new Date(order.created_at).toLocaleString('zh-CN')}
          </p>
        </div>
        <div>
          <p className="text-xs text-gray-400 uppercase tracking-wider">最近更新</p>
          <p className="text-sm font-medium text-gray-700 mt-0.5">
            {new Date(order.updated_at).toLocaleString('zh-CN')}
          </p>
        </div>
      </div>

      <div className="mt-5 pt-4 border-t border-gray-100">
        <p className="text-xs text-gray-400 uppercase tracking-wider mb-2">商品明细</p>
        <div className="space-y-2">
          {order.items.map((item, i) => (
            <div key={i} className="flex items-center justify-between text-sm">
              <span className="text-gray-700">
                {item.product_name}
                <span className="text-gray-400 ml-1">x{item.quantity}</span>
              </span>
              <span className="text-gray-800 font-medium">¥{(item.price * item.quantity).toLocaleString()}</span>
            </div>
          ))}
        </div>
        <div className="mt-3 pt-3 border-t border-gray-100 flex items-center justify-between">
          <span className="text-sm font-medium text-gray-700">合计</span>
          <span className="text-lg font-bold text-gray-900">¥{order.total.toLocaleString()}</span>
        </div>
      </div>
    </div>
  )
}
