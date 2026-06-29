import { useMemo, useState } from 'react'
import { PackageSearch } from 'lucide-react'
import EmptyState from '@/components/ui/EmptyState'
import { useOrderStore } from '@/stores/orderStore'
import type { Order, OrderStatus } from '@/types/order'
import OrderFilters from './OrderFilters'
import OrderRow from './OrderRow'

const EMPTY_ORDERS: Order[] = []

export default function OrderTable() {
  const orders = useOrderStore((s) => s.orders) ?? EMPTY_ORDERS
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState<OrderStatus | 'all'>('all')
  const [page, setPage] = useState(1)
  const pageSize = 8

  const filtered = useMemo(() => {
    const keyword = search.trim().toLowerCase()
    return orders.filter((order) => {
      const haystack = [
        order.order_id,
        order.user_id,
        order.merchant_name,
        order.merchant_id,
        ...order.items.map((item) => item.product_name),
      ].join(' ').toLowerCase()
      if (keyword && !haystack.includes(keyword)) return false
      if (statusFilter !== 'all' && order.status !== statusFilter) return false
      return true
    })
  }, [orders, search, statusFilter])

  const paged = useMemo(() => {
    const start = (page - 1) * pageSize
    return filtered.slice(start, start + pageSize)
  }, [filtered, page])

  const totalPages = Math.ceil(filtered.length / pageSize)

  return (
    <div className="rounded-xl border border-gray-100 bg-white shadow-sm">
      <div className="border-b border-gray-100 px-5 py-4">
        <OrderFilters
          search={search}
          onSearchChange={(value) => { setSearch(value); setPage(1) }}
          statusFilter={statusFilter}
          onStatusChange={(value) => { setStatusFilter(value); setPage(1) }}
        />
      </div>

      {filtered.length === 0 ? (
        <EmptyState icon={PackageSearch} title="暂无订单" description="选择商家和商品后创建一笔购买订单。" />
      ) : (
        <>
          <div className="grid grid-cols-1 gap-3 p-5 xl:grid-cols-2">
            {paged.map((order, index) => (
              <OrderRow key={order.order_id} order={order} index={index} />
            ))}
          </div>

          {totalPages > 1 && (
            <div className="flex items-center justify-between border-t border-gray-100 px-5 py-3">
              <span className="text-xs text-gray-400">共 {filtered.length} 条，第 {page}/{totalPages} 页</span>
              <div className="flex gap-1">
                <button
                  onClick={() => setPage((value) => Math.max(1, value - 1))}
                  disabled={page === 1}
                  className="rounded-md border border-gray-200 px-3 py-1.5 text-xs hover:bg-gray-50 disabled:opacity-40"
                >
                  上一页
                </button>
                <button
                  onClick={() => setPage((value) => Math.min(totalPages, value + 1))}
                  disabled={page === totalPages}
                  className="rounded-md border border-gray-200 px-3 py-1.5 text-xs hover:bg-gray-50 disabled:opacity-40"
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
