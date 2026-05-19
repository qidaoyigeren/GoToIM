import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, Gauge, RotateCcw, Timer } from 'lucide-react'
import { getBusinessSLA } from '@/api/notify'

export default function SLACards() {
  const { data } = useQuery({
    queryKey: ['businessSLA', '24h'],
    queryFn: () => getBusinessSLA('24h'),
    refetchInterval: 15_000,
  })

  const sla = data
  return (
    <section className="space-y-4">
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-5">
        <SLAStat icon={CheckCircle2} label="Delivered" value={percent(sla?.notification_success_rate)} detail={`${sla?.successful_notifications ?? 0}/${sla?.total_notifications ?? 0}`} tone="green" />
        <SLAStat icon={Gauge} label="ACK satisfied" value={percent(sla?.ack_satisfaction_rate)} detail={`${sla?.ack_satisfied_count ?? 0} met policy`} tone="blue" />
        <SLAStat icon={AlertTriangle} label="DLQ risk" value={percent(sla?.dlq_rate)} detail={`${sla?.dlq_count ?? 0} needs recovery`} tone={(sla?.dlq_count ?? 0) > 0 ? 'red' : 'green'} />
        <SLAStat icon={RotateCcw} label="Retry pressure" value={percent(sla?.retry_rate)} detail={`${sla?.retried_notifications ?? 0} retried`} tone="amber" />
        <SLAStat icon={Timer} label="P99 latency" value={`${Math.round(sla?.delivery_latency_p99_ms ?? 0)}ms`} detail={`ACK P99 ${Math.round(sla?.ack_latency_p99_ms ?? 0)}ms`} tone="gray" />
      </div>
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
        <Drilldown title="Failure reasons" rows={(sla?.failure_reason_ranking ?? []).map((r) => ({ key: r.key, value: r.count.toString() }))} />
        <Drilldown title="DLQ reasons" rows={(sla?.dlq_reason_ranking ?? []).map((r) => ({ key: r.key, value: r.count.toString() }))} />
        <Drilldown title="Retry by business" rows={(sla?.retry_pressure_by_business_type ?? []).map((r) => ({ key: r.business_type, value: percent(r.retry_rate) }))} />
      </div>
    </section>
  )
}

function SLAStat({
  icon: Icon,
  label,
  value,
  detail,
  tone,
}: {
  icon: typeof CheckCircle2
  label: string
  value: string
  detail: string
  tone: 'green' | 'blue' | 'red' | 'amber' | 'gray'
}) {
  const colors = {
    green: 'bg-emerald-50 text-emerald-700',
    blue: 'bg-blue-50 text-blue-700',
    red: 'bg-red-50 text-red-700',
    amber: 'bg-amber-50 text-amber-700',
    gray: 'bg-gray-50 text-gray-700',
  }
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <div className={`flex h-9 w-9 items-center justify-center rounded-md ${colors[tone]}`}>
        <Icon size={17} />
      </div>
      <p className="mt-3 text-xs font-medium text-gray-500">{label}</p>
      <p className="mt-1 text-xl font-bold text-gray-950">{value}</p>
      <p className="mt-1 text-xs text-gray-500">{detail}</p>
    </div>
  )
}

function percent(value?: number) {
  return `${Math.round((value ?? 0) * 100)}%`
}

function Drilldown({ title, rows }: { title: string; rows: Array<{ key: string; value: string }> }) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <h3 className="text-sm font-semibold text-gray-800">{title}</h3>
      <div className="mt-3 space-y-2">
        {rows.slice(0, 5).map((row) => (
          <div key={row.key} className="flex items-center justify-between gap-3 text-sm">
            <span className="truncate text-gray-600">{row.key}</span>
            <span className="font-semibold text-gray-900">{row.value}</span>
          </div>
        ))}
        {rows.length === 0 && <p className="text-sm text-gray-500">No incidents in this window.</p>}
      </div>
    </div>
  )
}
