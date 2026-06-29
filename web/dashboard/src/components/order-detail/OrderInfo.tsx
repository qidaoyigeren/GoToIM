import StatusBadge from '@/components/ui/StatusBadge'
import type { Order, OrderImportance } from '@/types/order'
import { IMPORTANCE_LABELS, ORDER_TYPE_LABELS } from '@/types/order'
import { ShieldCheck, ShoppingBag, Truck } from 'lucide-react'

type Props = { order: Order }

const importanceClass: Record<OrderImportance, string> = {
  normal: 'bg-gray-50 text-gray-700',
  high: 'bg-blue-50 text-blue-700',
  urgent: 'bg-amber-50 text-amber-700',
  critical: 'bg-red-50 text-red-700',
}

export default function OrderInfo({ order }: Props) {
  const importance = order.importance || 'normal'
  return (
    <div className="rounded-xl border border-gray-100 bg-white p-6 shadow-sm">
      <div className="mb-5 flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-bold text-gray-900">{order.order_id}</h2>
          <p className="mt-0.5 text-sm text-gray-500">
            用户 {order.user_id} / 商家 {order.merchant_name || order.merchant_id || '-'}
          </p>
        </div>
        <StatusBadge status={order.status} size="md" />
      </div>

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Info label="订单形式" value={ORDER_TYPE_LABELS[order.order_type || 'normal']} />
        <Info label="重要度" value={IMPORTANCE_LABELS[importance]} className={importanceClass[importance]} />
        <Info label="商家 UID" value={order.merchant_uid ? String(order.merchant_uid) : '-'} />
        <Info label="履约方式" value={order.fulfillment_mode || '-'} />
        <Info label="创建时间" value={new Date(order.created_at).toLocaleString('zh-CN')} />
        <Info label="更新时间" value={new Date(order.updated_at).toLocaleString('zh-CN')} />
        <Info label="群聊 Room" value={order.support_room_id || '-'} />
        <Info label="私聊会话" value={order.private_conversation_id || '-'} />
      </div>

      {order.buyer_note && (
        <div className="mt-5 rounded-lg border border-gray-100 bg-gray-50 px-3 py-2 text-sm text-gray-600">
          买家备注：{order.buyer_note}
        </div>
      )}

      <div className="mt-5 grid grid-cols-1 gap-3 border-t border-gray-100 pt-4 md:grid-cols-3">
        <ServiceCard icon={ShoppingBag} label="下单方式" value="一键下单，直接触发消息" />
        <ServiceCard icon={Truck} label="履约模式" value={order.fulfillment_mode || 'physical'} />
        <ServiceCard icon={ShieldCheck} label="消息保障" value={`${IMPORTANCE_LABELS[importance]} / ACK`} />
      </div>

      <div className="mt-5 border-t border-gray-100 pt-4">
        <p className="mb-3 text-xs font-medium uppercase tracking-wider text-gray-400">商品明细</p>
        <div className="space-y-3">
          {order.items.map((item) => (
            <div key={`${item.product_id || item.product_name}-${item.sku_id || ''}`} className="flex gap-3 rounded-lg border border-gray-100 bg-gray-50 p-3">
              <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-lg bg-white text-blue-600">
                <ShoppingBag size={22} />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="truncate text-sm font-semibold text-gray-800">{item.product_name}</div>
                    <div className="mt-0.5 text-xs text-gray-400">{item.sku_id || item.product_id || 'demo sku'}</div>
                  </div>
                  <div className="text-right">
                    <div className="text-sm font-semibold text-gray-900">¥{(item.price * item.quantity).toLocaleString()}</div>
                    <div className="text-xs text-gray-400">x{item.quantity}</div>
                  </div>
                </div>
              </div>
            </div>
          ))}
        </div>
        <div className="mt-3 flex items-center justify-between border-t border-gray-100 pt-3">
          <span className="text-sm font-medium text-gray-700">合计</span>
          <span className="text-lg font-bold text-gray-900">¥{order.total.toLocaleString()}</span>
        </div>
      </div>
    </div>
  )
}

function Info({ label, value, className = 'bg-gray-50 text-gray-700' }: { label: string; value: string; className?: string }) {
  return (
    <div className={`min-w-0 rounded-lg px-3 py-2 ${className}`}>
      <p className="text-[11px] opacity-70">{label}</p>
      <p className="mt-1 truncate text-sm font-semibold">{value}</p>
    </div>
  )
}

function ServiceCard({ icon: Icon, label, value }: { icon: typeof ShoppingBag; label: string; value: string }) {
  return (
    <div className="rounded-lg bg-gray-50 px-3 py-3 text-gray-700">
      <Icon size={16} />
      <p className="mt-2 text-[11px] text-gray-400">{label}</p>
      <p className="mt-1 truncate text-sm font-semibold">{value}</p>
    </div>
  )
}
