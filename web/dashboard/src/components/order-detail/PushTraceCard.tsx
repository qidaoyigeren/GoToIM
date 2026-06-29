import { AlertTriangle, CheckCircle2, Clock3, RotateCcw, Route, ShieldCheck } from 'lucide-react'
import { CARD_BASE, CARD_LG } from '@/components/ui/cardStyles'
import type { NotificationTrace } from '@/types/notification'

type Props = {
  trace?: NotificationTrace | null
}

export default function PushTraceCard({ trace }: Props) {
  if (!trace) {
    return (
      <section className={CARD_LG}>
        <div className="flex items-center gap-2 text-sm font-semibold text-gray-700">
          <Route size={16} />
          通知 Trace
        </div>
        <p className="mt-3 text-sm text-gray-500">当前订单还没有可展示的通知 trace。</p>
      </section>
    )
  }

  const attempts = Array.isArray(trace.attempts) ? trace.attempts : []
  const acks = Array.isArray(trace.acks) ? trace.acks : []
  const notification = trace.notification
  const ackStatus = trace.ack_policy_status
  const dlq = trace.dlq

  return (
    <section className={CARD_BASE}>
      <div className="border-b border-gray-100 px-5 py-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <div className="flex items-center gap-2 text-sm font-semibold text-gray-800">
              <Route size={16} />
              通知 Trace
            </div>
            <p className="mt-1 text-xs text-gray-500">{notification?.title || trace.trace_id}</p>
          </div>
          <div className="flex flex-wrap gap-2">
            <Badge label={notification?.status || 'pending'} tone={notification?.status === 'acked' ? 'green' : 'gray'} />
            <Badge label={trace.delivery_path || 'pending'} tone="blue" />
            <Badge label={notification?.priority || 'normal'} tone={notification?.priority === 'critical' ? 'red' : 'blue'} />
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3 p-5 lg:grid-cols-4">
        <Metric icon={RotateCcw} label="重试次数" value={(trace.retry_count ?? 0).toString()} />
        <Metric icon={ShieldCheck} label="ACK 策略" value={ackStatus?.satisfied ? '已满足' : ackStatus?.policy || 'pending'} />
        <Metric icon={Clock3} label="TTL" value={`${notification?.ttl_seconds ?? 0}s`} />
        <Metric icon={dlq ? AlertTriangle : CheckCircle2} label="DLQ" value={dlq ? dlq.reason : '无'} tone={dlq ? 'red' : 'green'} />
      </div>

      <div className="border-t border-gray-100 px-5 py-4">
        <div className="space-y-3">
          {attempts.map((attempt) => (
            <div key={attempt.attempt_id} className="flex items-start justify-between gap-4 rounded-md border border-gray-100 bg-gray-50 px-3 py-2">
              <div>
                <p className="text-sm font-medium text-gray-800">{attempt.path || attempt.channel}</p>
                <p className="mt-0.5 text-xs text-gray-500">
                  {attempt.target_node || attempt.target} / attempt {attempt.attempt_no || 1}
                </p>
                {attempt.error_message && <p className="mt-1 text-xs text-red-600">{attempt.error_message}</p>}
              </div>
              <div className="text-right text-xs text-gray-500">
                <p>{attempt.status}</p>
                <p>{Math.round(attempt.latency_ms || 0)}ms</p>
              </div>
            </div>
          ))}
          {acks.map((ack) => (
            <div key={ack.ack_id} className="flex items-center justify-between rounded-md border border-emerald-100 bg-emerald-50 px-3 py-2">
              <span className="text-sm font-medium text-emerald-800">收到 ACK</span>
              <span className="text-xs text-emerald-700">{Math.round(ack.latency_ms ?? 0)}ms</span>
            </div>
          ))}
          {attempts.length === 0 && acks.length === 0 && (
            <p className="rounded-md border border-gray-100 bg-gray-50 px-3 py-3 text-sm text-gray-500">
              outbox 尚未产生投递 attempt 或客户端 ACK。
            </p>
          )}
        </div>
      </div>
    </section>
  )
}

function Metric({
  icon: Icon,
  label,
  value,
  tone = 'gray',
}: {
  icon: typeof CheckCircle2
  label: string
  value: string
  tone?: 'gray' | 'green' | 'red'
}) {
  const colors = {
    gray: 'bg-gray-50 text-gray-700',
    green: 'bg-emerald-50 text-emerald-700',
    red: 'bg-red-50 text-red-700',
  }
  return (
    <div className={`rounded-md px-3 py-3 ${colors[tone]}`}>
      <Icon size={16} />
      <p className="mt-2 text-xs opacity-75">{label}</p>
      <p className="mt-1 text-sm font-semibold">{value}</p>
    </div>
  )
}

function Badge({ label, tone }: { label: string; tone: 'gray' | 'green' | 'blue' | 'red' }) {
  const colors = {
    gray: 'bg-gray-100 text-gray-700',
    green: 'bg-emerald-100 text-emerald-700',
    blue: 'bg-blue-100 text-blue-700',
    red: 'bg-red-100 text-red-700',
  }
  return <span className={`rounded-full px-2.5 py-1 text-xs font-medium ${colors[tone]}`}>{label}</span>
}
