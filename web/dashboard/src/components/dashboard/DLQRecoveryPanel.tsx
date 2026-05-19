import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, History, RotateCcw, Search } from 'lucide-react'
import { bulkReplayDLQ, bulkResolveDLQ, listDLQ, replayDLQ, resolveDLQ } from '@/api/notify'
import type { DLQFilters, NotificationDLQ } from '@/types/notification'

type ActionState = {
  note: string
  resolution: string
  bulkConfirm: string
}

export default function DLQRecoveryPanel() {
  const queryClient = useQueryClient()
  const [filters, setFilters] = useState<DLQFilters>({ resolved: 'false', limit: 50 })
  const [action, setAction] = useState<ActionState>({
    note: '',
    resolution: 'resolved',
    bulkConfirm: '',
  })

  const { data = [] } = useQuery({
    queryKey: ['dlq', filters],
    queryFn: () => listDLQ(filters),
    refetchInterval: 10_000,
  })

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['dlq'] })
  const replayOne = useMutation({
    mutationFn: (id: string) => replayDLQ(id, action.note || 'Replayed from dashboard'),
    onSuccess: refresh,
  })
  const resolveOne = useMutation({
    mutationFn: (id: string) => resolveDLQ(id, action.resolution || 'resolved', action.note || 'Resolved from dashboard'),
    onSuccess: refresh,
  })
  const replayBulk = useMutation({
    mutationFn: () => bulkReplayDLQ(filters, action.note || 'Bulk replay from dashboard'),
    onSuccess: refresh,
  })
  const resolveBulk = useMutation({
    mutationFn: () => bulkResolveDLQ(filters, action.resolution || 'resolved', action.note || 'Bulk resolve from dashboard'),
    onSuccess: refresh,
  })

  const explicitBulkReady = action.bulkConfirm === 'CONFIRM'
  const openCount = useMemo(() => data.filter((item) => !item.resolved_at).length, [data])

  return (
    <section className="rounded-lg border border-gray-200 bg-white shadow-sm">
      <div className="border-b border-gray-100 px-5 py-4">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
          <div>
            <div className="flex items-center gap-2 text-sm font-semibold text-gray-800">
              <AlertTriangle size={16} />
              DLQ recovery operations
            </div>
            <p className="mt-1 text-xs text-gray-500">{openCount} unresolved items in the current filter</p>
          </div>
          <div className="grid grid-cols-2 gap-2 text-xs sm:grid-cols-3 lg:w-[520px]">
            <select className="rounded-md border border-gray-200 px-2 py-2" value={filters.resolved ?? 'false'} onChange={(e) => setFilters({ ...filters, resolved: e.target.value as DLQFilters['resolved'] })}>
              <option value="false">Unresolved</option>
              <option value="true">Resolved</option>
              <option value="">All</option>
            </select>
            <input className="rounded-md border border-gray-200 px-2 py-2" placeholder="Reason" value={filters.reason ?? ''} onChange={(e) => setFilters({ ...filters, reason: e.target.value })} />
            <input className="rounded-md border border-gray-200 px-2 py-2" placeholder="Business type" value={filters.business_type ?? ''} onChange={(e) => setFilters({ ...filters, business_type: e.target.value })} />
            <input className="rounded-md border border-gray-200 px-2 py-2" placeholder="Older than seconds" value={filters.older_than_seconds ?? ''} onChange={(e) => setFilters({ ...filters, older_than_seconds: Number(e.target.value) || undefined })} />
            <input className="rounded-md border border-gray-200 px-2 py-2" placeholder="Operator" value={filters.operator ?? ''} onChange={(e) => setFilters({ ...filters, operator: e.target.value })} />
            <input className="rounded-md border border-gray-200 px-2 py-2" placeholder="Limit" value={filters.limit ?? 50} onChange={(e) => setFilters({ ...filters, limit: Number(e.target.value) || 50 })} />
          </div>
        </div>

        <div className="mt-4 grid gap-2 lg:grid-cols-[1fr_180px_120px]">
          <input className="rounded-md border border-gray-200 px-3 py-2 text-sm" placeholder="Operator note for replay or resolve" value={action.note} onChange={(e) => setAction({ ...action, note: e.target.value })} />
          <input className="rounded-md border border-gray-200 px-3 py-2 text-sm" placeholder="Resolution" value={action.resolution} onChange={(e) => setAction({ ...action, resolution: e.target.value })} />
          <input className="rounded-md border border-gray-200 px-3 py-2 text-sm" placeholder="CONFIRM" value={action.bulkConfirm} onChange={(e) => setAction({ ...action, bulkConfirm: e.target.value })} />
        </div>
        <div className="mt-3 flex flex-wrap gap-2">
          <button disabled={!explicitBulkReady} className="inline-flex items-center gap-1 rounded-md bg-gray-900 px-3 py-2 text-xs font-medium text-white disabled:cursor-not-allowed disabled:bg-gray-300" onClick={() => replayBulk.mutate()}>
            <RotateCcw size={13} />
            Bulk replay filtered
          </button>
          <button disabled={!explicitBulkReady} className="inline-flex items-center gap-1 rounded-md border border-gray-200 px-3 py-2 text-xs font-medium text-gray-700 disabled:cursor-not-allowed disabled:text-gray-300" onClick={() => resolveBulk.mutate()}>
            <CheckCircle2 size={13} />
            Bulk resolve filtered
          </button>
          <span className="inline-flex items-center gap-1 text-xs text-gray-500">
            <Search size={13} />
            Type CONFIRM to enable bulk actions.
          </span>
        </div>
      </div>

      <div className="divide-y divide-gray-100">
        {data.slice(0, 8).map((item) => (
          <DLQRow key={item.dlq_id} item={item} onReplay={() => replayOne.mutate(item.dlq_id)} onResolve={() => resolveOne.mutate(item.dlq_id)} />
        ))}
        {data.length === 0 && <p className="px-5 py-6 text-sm text-gray-500">No DLQ items match the current filters.</p>}
      </div>
    </section>
  )
}

function DLQRow({ item, onReplay, onResolve }: { item: NotificationDLQ; onReplay: () => void; onResolve: () => void }) {
  return (
    <div className="px-5 py-4">
      <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <p className="truncate text-sm font-medium text-gray-800">{item.reason}</p>
            <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-600">{item.business_type || 'unknown'}</span>
            {item.resolved_at && <span className="rounded-full bg-emerald-50 px-2 py-0.5 text-xs text-emerald-700">resolved</span>}
          </div>
          <p className="mt-1 truncate text-xs text-gray-500">{item.order_id || item.notify_id} · retried {item.retry_count} times · trace {item.trace_id || item.notify_id}</p>
          {item.latest_audit && (
            <p className="mt-1 inline-flex items-center gap-1 text-xs text-blue-700">
              <History size={12} />
              Latest audit: {item.latest_audit.action} by {item.latest_audit.operator} · {item.latest_audit.note || item.latest_audit.resolution || 'no note'}
            </p>
          )}
        </div>
        <div className="flex gap-2">
          <button className="inline-flex items-center gap-1 rounded-md bg-gray-900 px-3 py-2 text-xs font-medium text-white hover:bg-gray-800" onClick={onReplay}>
            <RotateCcw size={13} />
            Replay
          </button>
          <button className="inline-flex items-center gap-1 rounded-md border border-gray-200 px-3 py-2 text-xs font-medium text-gray-700 hover:bg-gray-50" onClick={onResolve}>
            <CheckCircle2 size={13} />
            Resolve
          </button>
        </div>
      </div>
    </div>
  )
}
