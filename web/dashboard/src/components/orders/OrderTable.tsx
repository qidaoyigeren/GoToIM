import { useState, useMemo } from 'react'
import { useOrderStore } from '@/stores/orderStore'
import type { OrderStatus } from '@/types/order'
import OrderFilters from './OrderFilters'
import OrderRow from './OrderRow'
import EmptyState from '@/components/ui/EmptyState'
import { PackageSearch } from 'lucide-react'

export default function OrderTable() {
  const orders = useOrderStore((s) => s.orders)
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState<OrderStatus | 'all'>('all')
  const [page, setPage] = useState(1)
  const pageSize = 10

  const filtered = useMemo(() => {
    return orders.filter((o) => {
      if (search && !o.order_id.toLowerCase().includes(search.toLowerCase())) return false
      if (statusFilter !== 'all' && o.status !== statusFilter) return false
      return true
    })
  }, [orders, search, statusFilter])

  const paged = useMemo(() => {
    const start = (page - 1) * pageSize
    return filtered.slice(start, start + pageSize)
  }, [filtered, page])

  const totalPages = Math.ceil(filtered.length / pageSize)

  return (
    <div className="bg-white rounded-xl border border-gray-100 shadow-sm">
      <div className="px-5 py-4 border-b border-gray-100">
        <OrderFilters
          search={search}
          onSearchChange={(v) => { setSearch(v); setPage(1) }}
          statusFilter={statusFilter}
          onStatusChange={(v) => { setStatusFilter(v); setPage(1) }}
        />
      </div>

      {filtered.length === 0 ? (
        <EmptyState icon={PackageSearch} title="暂无订单" description="尝试调整筛选条件或创建新订单" />
      ) : (
        <>
          <table className="w-full">
            <thead>
              <tr className="border-b border-gray-100">
                <th className="text-left text-xs font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">订单号</th>
                <th className="text-left text-xs font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">用户</th>
                <th className="text-left text-xs font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">金额</th>
                <th className="text-left text-xs font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">状态</th>
                <th className="text-left text-xs font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">商品</th>
                <th className="text-left text-xs font-medium text-gray-400 uppercase tracking-wider py-2.5 px-4">更新时间</th>
              </tr>
            </thead>
            <tbody>
              {paged.map((order, i) => (
                <OrderRow key={order.order_id} order={order} index={i} />
              ))}
            </tbody>
          </table>

          {totalPages > 1 && (
            <div className="px-5 py-3 border-t border-gray-100 flex items-center justify-between">
              <span className="text-xs text-gray-400">
                共 {filtered.length} 条，第 {page}/{totalPages} 页
              </span>
              <div className="flex gap-1">
                <button
                  onClick={() => setPage((p) => Math.max(1, p - 1))}
                  disabled={page === 1}
                  className="px-3 py-1.5 text-xs rounded-md border border-gray-200 disabled:opacity-40 hover:bg-gray-50"
                >
                  上一页
                </button>
                <button
                  onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                  disabled={page === totalPages}
                  className="px-3 py-1.5 text-xs rounded-md border border-gray-200 disabled:opacity-40 hover:bg-gray-50"
                >
                  下一页
                </button>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )
}
