import { Search, SlidersHorizontal } from 'lucide-react'
import type { OrderStatus } from '@/types/order'
import { ORDER_STATUS_LABELS } from '@/types/order'

type Props = {
  search: string
  onSearchChange: (v: string) => void
  statusFilter: OrderStatus | 'all'
  onStatusChange: (v: OrderStatus | 'all') => void
}

export default function OrderFilters({ search, onSearchChange, statusFilter, onStatusChange }: Props) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <div className="relative">
        <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
        <input
          type="text"
          placeholder="搜索订单号、商家或商品"
          value={search}
          onChange={(event) => onSearchChange(event.target.value)}
          className="w-64 rounded-lg border border-gray-200 bg-white py-2 pl-9 pr-4 text-sm outline-none focus:border-blue-300 focus:ring-2 focus:ring-blue-500/20"
        />
      </div>
      <div className="flex items-center gap-1.5">
        <SlidersHorizontal size={14} className="text-gray-400" />
        <select
          value={statusFilter}
          onChange={(event) => onStatusChange(event.target.value as OrderStatus | 'all')}
          className="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500/20"
        >
          <option value="all">全部状态</option>
          {Object.entries(ORDER_STATUS_LABELS).map(([key, label]) => (
            <option key={key} value={key}>{label}</option>
          ))}
        </select>
      </div>
    </div>
  )
}
