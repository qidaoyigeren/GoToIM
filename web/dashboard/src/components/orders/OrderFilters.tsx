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
    <div className="flex items-center gap-3 flex-wrap">
      <div className="relative">
        <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
        <input
          type="text"
          placeholder="搜索订单号..."
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          className="pl-9 pr-4 py-2 text-sm border border-gray-200 rounded-lg w-56 focus:outline-none focus:ring-2 focus:ring-primary-500/20 focus:border-primary-300 bg-white"
        />
      </div>
      <div className="flex items-center gap-1.5">
        <SlidersHorizontal size={14} className="text-gray-400" />
        <select
          value={statusFilter}
          onChange={(e) => onStatusChange(e.target.value as OrderStatus | 'all')}
          className="text-sm border border-gray-200 rounded-lg px-3 py-2 bg-white focus:outline-none focus:ring-2 focus:ring-primary-500/20"
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
