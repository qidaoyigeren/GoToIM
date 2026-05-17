import { useMemo } from 'react'
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Cell } from 'recharts'
import { useOrderStore } from '@/stores/orderStore'
import { ORDER_STATUS_LABELS, type OrderStatus } from '@/types/order'

const statusColors: Record<OrderStatus, string> = {
  created: '#3b82f6',
  paid: '#f59e0b',
  confirmed: '#6366f1',
  shipped: '#10b981',
  delivered: '#22c55e',
  cancelled: '#ef4444',
  delivery_failed: '#ec4899',
}

const order: OrderStatus[] = ['created', 'paid', 'confirmed', 'shipped', 'delivered']

export default function OrderStatusFunnel() {
  const orders = useOrderStore((s) => s.orders)

  const data = useMemo(() => {
    const counts: Record<string, number> = {}
    orders.forEach((o) => {
      counts[o.status] = (counts[o.status] || 0) + 1
    })
    return order.map((s) => ({
      name: ORDER_STATUS_LABELS[s],
      value: counts[s] || 0,
      color: statusColors[s],
    }))
  }, [orders])

  return (
    <div className="bg-white rounded-xl border border-gray-100 p-5 shadow-sm">
      <h3 className="text-sm font-semibold text-gray-700 mb-4">订单状态分布</h3>
      <ResponsiveContainer width="100%" height={200}>
        <BarChart data={data} barSize={36}>
          <CartesianGrid strokeDasharray="3 3" stroke="#f1f5f9" />
          <XAxis dataKey="name" tick={{ fontSize: 12, fill: '#94a3b8' }} axisLine={false} tickLine={false} />
          <YAxis tick={{ fontSize: 12, fill: '#94a3b8' }} axisLine={false} tickLine={false} />
          <Tooltip
            contentStyle={{ borderRadius: 8, border: '1px solid #e2e8f0', boxShadow: '0 4px 12px rgba(0,0,0,0.08)' }}
            cursor={{ fill: '#f8fafc' }}
          />
          <Bar dataKey="value" radius={[4, 4, 0, 0]}>
            {data.map((d, i) => (
              <Cell key={i} fill={d.color} />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}
