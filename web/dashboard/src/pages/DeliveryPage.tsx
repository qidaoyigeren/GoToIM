import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCheck, Clock3, Info, MessageSquareText, RefreshCw, Search, X } from 'lucide-react'
import { getDeliveryMessage, listDeliveryMessages } from '@/api/delivery'
import type { DeliveryMessage, DeliveryMessageDetail } from '@/types/delivery'

const statusTone: Record<string, string> = {
  pending: 'bg-amber-50 text-amber-700',
  sent: 'bg-blue-50 text-blue-700',
  delivered: 'bg-emerald-50 text-emerald-700',
  acked: 'bg-gray-100 text-gray-700',
  read: 'bg-gray-100 text-gray-700',
  failed: 'bg-red-50 text-red-700',
  dlq: 'bg-red-100 text-red-800',
}

export default function DeliveryPage() {
  const [searchParams] = useSearchParams()
  const [selectedId, setSelectedId] = useState('')
  const [query, setQuery] = useState(searchParams.get('order_id') || '')
  const messagesQuery = useQuery({
    queryKey: ['delivery', 'messages'],
    queryFn: () => listDeliveryMessages(300),
    refetchInterval: 8000,
  })
  const detailQuery = useQuery({
    queryKey: ['delivery', 'message', selectedId],
    queryFn: () => getDeliveryMessage(selectedId),
    enabled: !!selectedId,
  })

  const messages = messagesQuery.data ?? []
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return messages
    return messages.filter((item) =>
      [
        item.id,
        item.message_id,
        item.type,
        item.order_id,
        item.sender,
        item.target,
        item.delivery_method,
        item.status,
        item.priority,
        item.business_type,
        item.event_type,
        item.trace_id,
      ].some((value) => String(value ?? '').toLowerCase().includes(needle))
    )
  }, [messages, query])

  useEffect(() => {
    if (!selectedId && filtered[0]) setSelectedId(filtered[0].id)
  }, [filtered, selectedId])

  const stats = useMemo(() => {
    return {
      total: filtered.length,
      delivered: filtered.filter((item) => ['delivered', 'acked', 'read'].includes(item.status)).length,
      failed: filtered.filter((item) => ['failed', 'dlq'].includes(item.status)).length,
      retry: filtered.filter((item) => item.record_kind === 'retry' || (item.retry_count ?? 0) > 0).length,
    }
  }, [filtered])

  return (
    <div className="space-y-5 animate-fade-in">
      <header className="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-blue-600">Delivery</p>
          <h1 className="mt-1 text-2xl font-bold text-gray-950">总体消息投递状态页面</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-gray-600">
            聚合购买通知、商家通知、私聊、群聊、ACK、失败、DLQ 和重试消息。点击任一行查看 trace、payload、attempts 与关联订单。
          </p>
        </div>
        <div className="grid grid-cols-4 gap-2 rounded-lg border border-gray-100 bg-white p-3 shadow-sm">
          <Stat label="总数" value={stats.total} />
          <Stat label="已投递" value={stats.delivered} />
          <Stat label="失败/DLQ" value={stats.failed} danger />
          <Stat label="重试" value={stats.retry} />
        </div>
      </header>

      <section className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1fr)_420px]">
        <div className="rounded-lg border border-gray-100 bg-white shadow-sm">
          <div className="flex flex-col gap-3 border-b border-gray-100 p-4 md:flex-row md:items-center md:justify-between">
            <div className="flex min-w-0 items-center gap-2 rounded-lg border border-gray-200 px-3 py-2">
              <Search size={15} className="text-gray-400" />
              <input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="搜索消息 ID、订单、发送方、状态、trace_id"
                className="min-w-[260px] flex-1 text-sm outline-none"
              />
            </div>
            <button
              type="button"
              onClick={() => void messagesQuery.refetch()}
              className="inline-flex items-center justify-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              <RefreshCw size={15} />
              刷新
            </button>
          </div>

          <div className="overflow-x-auto">
            <table className="min-w-[1180px] w-full text-left text-sm">
              <thead className="bg-gray-50 text-xs font-semibold uppercase text-gray-500">
                <tr>
                  <Th>消息 ID</Th>
                  <Th>类型</Th>
                  <Th>关联订单</Th>
                  <Th>发送方</Th>
                  <Th>接收方/房间</Th>
                  <Th>投递方式</Th>
                  <Th>状态</Th>
                  <Th>priority</Th>
                  <Th>TTL</Th>
                  <Th>ACK 策略</Th>
                  <Th>创建时间</Th>
                  <Th>最近更新</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {filtered.map((item) => (
                  <DeliveryRow key={item.id} item={item} active={selectedId === item.id} onClick={() => setSelectedId(item.id)} />
                ))}
                {!messagesQuery.isLoading && filtered.length === 0 && (
                  <tr>
                    <td colSpan={12} className="px-4 py-12 text-center text-sm text-gray-400">
                      暂无匹配消息
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>

        <DetailPanel detail={detailQuery.data} loading={detailQuery.isLoading} onClose={() => setSelectedId('')} />
      </section>
    </div>
  )
}

function DeliveryRow({ item, active, onClick }: { item: DeliveryMessage; active: boolean; onClick: () => void }) {
  return (
    <tr
      onClick={onClick}
      className={`cursor-pointer transition-colors ${active ? 'bg-blue-50' : 'hover:bg-gray-50'}`}
    >
      <Td>
        <div className="max-w-[160px] truncate font-mono text-xs text-gray-800">{item.message_id}</div>
        <div className="mt-1 text-[11px] text-gray-400">{item.record_kind}</div>
      </Td>
      <Td>{item.type}</Td>
      <Td><span className="font-mono text-xs">{item.order_id || '-'}</span></Td>
      <Td>{item.sender || '-'}</Td>
      <Td><span className="max-w-[190px] truncate">{item.target || item.room_id || '-'}</span></Td>
      <Td><PathLabel value={item.delivery_method || '-'} /></Td>
      <Td><StatusLabel status={item.status} /></Td>
      <Td>{item.priority || '-'}</Td>
      <Td>{item.ttl_seconds ? `${item.ttl_seconds}s` : '-'}</Td>
      <Td>{item.ack_policy || '-'}</Td>
      <Td>{formatTime(item.created_at)}</Td>
      <Td>{formatTime(item.updated_at || item.created_at)}</Td>
    </tr>
  )
}

function DetailPanel({ detail, loading, onClose }: { detail?: DeliveryMessageDetail; loading: boolean; onClose: () => void }) {
  if (!detail && !loading) {
    return (
      <aside className="rounded-lg border border-gray-100 bg-white p-6 text-center text-sm text-gray-400 shadow-sm">
        选择一条消息查看详情
      </aside>
    )
  }

  const message = detail?.message
  const attempts = detail?.attempts ?? detail?.notification_trace?.attempts ?? []
  const acks = detail?.acks ?? detail?.notification_trace?.acks ?? []
  const rawPayload = detail?.raw_payload ?? detail?.notification_trace?.outbox?.payload_json ?? detail?.chat_message ?? {}

  return (
    <aside className="max-h-[calc(100vh-120px)] overflow-y-auto rounded-lg border border-gray-100 bg-white shadow-sm">
      <div className="sticky top-0 z-10 flex items-center justify-between border-b border-gray-100 bg-white px-4 py-3">
        <div className="flex items-center gap-2">
          <Info size={16} className="text-blue-600" />
          <h2 className="text-sm font-semibold text-gray-950">消息详情</h2>
        </div>
        <button type="button" onClick={onClose} title="关闭详情" className="rounded-lg p-1.5 text-gray-400 hover:bg-gray-50 hover:text-gray-700">
          <X size={16} />
        </button>
      </div>

      {loading ? (
        <div className="p-5 text-sm text-gray-400">加载详情中...</div>
      ) : message && detail ? (
        <div className="space-y-4 p-4">
          <section>
            <h3 className="mb-2 text-sm font-semibold text-gray-950">基础信息</h3>
            <div className="space-y-2 rounded-lg bg-gray-50 p-3 text-xs text-gray-600">
              <InfoLine label="消息 ID" value={message.message_id} />
              <InfoLine label="类型" value={`${message.record_kind} / ${message.type}`} />
              <InfoLine label="订单" value={message.order_id || '-'} />
              <InfoLine label="发送方" value={message.sender || '-'} />
              <InfoLine label="接收方/房间" value={message.target || message.room_id || '-'} />
              <InfoLine label="状态" value={message.status} />
              <InfoLine label="投递路径" value={detail.delivery_path || message.delivery_method || '-'} />
              <InfoLine label="business_type" value={detail.business_type || message.business_type || '-'} />
              <InfoLine label="event_type" value={detail.event_type || message.event_type || '-'} />
              <InfoLine label="trace_id" value={detail.trace_id || message.trace_id || '-'} />
            </div>
          </section>

          <section>
            <h3 className="mb-2 text-sm font-semibold text-gray-950">投递链路</h3>
            <div className="rounded-lg border border-gray-100 p-3 text-xs text-gray-600">
              <InfoLine label="retry_count" value={String(detail.retry_count ?? message.retry_count ?? 0)} />
              <InfoLine label="target_node" value={detail.target_node || message.target_node || '-'} />
              <InfoLine label="latency" value={detail.latency_ms || message.latency_ms ? `${detail.latency_ms ?? message.latency_ms}ms` : '-'} />
              <InfoLine label="失败原因" value={detail.failure_reason || detail.last_error || message.last_error || '暂无失败原因'} />
              <InfoLine label="last_error" value={detail.last_error || message.last_error || '-'} />
            </div>
          </section>

          <section>
            <h3 className="mb-2 text-sm font-semibold text-gray-950">attempts 列表</h3>
            <div className="space-y-2">
              {attempts.length === 0 ? (
                <EmptyLine text="暂无 attempts" />
              ) : (
                attempts.map((attempt) => (
                  <div key={attempt.attempt_id} className="rounded-lg border border-gray-100 p-3 text-xs text-gray-600">
                    <div className="mb-1 flex items-center justify-between gap-2">
                      <span className="font-mono text-gray-800">{attempt.attempt_id}</span>
                      <StatusLabel status={attempt.status} />
                    </div>
                    <InfoLine label="path" value={attempt.path || '-'} />
                    <InfoLine label="target" value={attempt.target || '-'} />
                    <InfoLine label="target_node" value={attempt.target_node || '-'} />
                    <InfoLine label="latency" value={attempt.latency_ms ? `${attempt.latency_ms}ms` : '-'} />
                    <InfoLine label="error" value={attempt.error_message || '暂无失败原因'} />
                  </div>
                ))
              )}
            </div>
          </section>

          <section>
            <h3 className="mb-2 text-sm font-semibold text-gray-950">ACK 列表</h3>
            <div className="space-y-2">
              {acks.length === 0 ? (
                <EmptyLine text="暂无 ACK" />
              ) : (
                acks.map((ack) => (
                  <div key={ack.ack_id} className="rounded-lg border border-gray-100 p-3 text-xs text-gray-600">
                    <InfoLine label="ack_id" value={ack.ack_id} />
                    <InfoLine label="user_id" value={ack.user_id} />
                    <InfoLine label="msg_id" value={ack.msg_id || '-'} />
                    <InfoLine label="ack_key" value={ack.ack_key || '-'} />
                    <InfoLine label="latency" value={ack.latency_ms ? `${ack.latency_ms}ms` : '-'} />
                  </div>
                ))
              )}
            </div>
          </section>

          {detail.dlq && (
            <section>
              <h3 className="mb-2 text-sm font-semibold text-gray-950">DLQ 信息</h3>
              <div className="rounded-lg border border-red-100 bg-red-50 p-3 text-xs text-red-800">
                <InfoLine label="dlq_id" value={detail.dlq.dlq_id} />
                <InfoLine label="reason" value={detail.dlq.reason} />
                <InfoLine label="last_error" value={detail.dlq.last_error} />
                <InfoLine label="retry_count" value={String(detail.dlq.retry_count)} />
              </div>
            </section>
          )}

          <section>
            <h3 className="mb-2 text-sm font-semibold text-gray-950">关联订单信息</h3>
            {detail.order ? (
              <div className="rounded-lg border border-gray-100 p-3 text-xs text-gray-600">
                <InfoLine label="order_id" value={detail.order.order_id} />
                <InfoLine label="merchant" value={detail.order.merchant_name || '-'} />
                <InfoLine label="order_type" value={detail.order.order_type || '-'} />
                <InfoLine label="importance" value={detail.order.importance || '-'} />
                <InfoLine label="total" value={`¥${detail.order.total.toLocaleString()}`} />
              </div>
            ) : (
              <EmptyLine text="暂无关联订单" />
            )}
          </section>

          {detail.chat_message && (
            <section>
              <h3 className="mb-2 text-sm font-semibold text-gray-950">聊天字段</h3>
              <div className="rounded-lg border border-gray-100 p-3 text-xs text-gray-600">
                <InfoLine label="message_id" value={detail.chat_message.message_id} />
                <InfoLine label="conversation_id" value={detail.chat_message.conversation_id} />
                <InfoLine label="room_id" value={detail.conversation?.room_id || '-'} />
                <InfoLine label="sender_uid" value={String(detail.chat_message.sender_uid)} />
                <InfoLine label="receiver_uid" value={String(detail.chat_message.receiver_uid)} />
                <InfoLine label="sender_role" value={detail.chat_message.sender_role} />
                <InfoLine label="status" value={detail.chat_message.status} />
                <InfoLine label="created_at" value={formatTime(detail.chat_message.created_at)} />
                <InfoLine label="delivered_at" value={formatTime(detail.chat_message.delivered_at)} />
                <InfoLine label="read_at" value={formatTime(detail.chat_message.read_at)} />
              </div>
            </section>
          )}

          <section>
            <h3 className="mb-2 text-sm font-semibold text-gray-950">原始 payload</h3>
            <pre className="max-h-72 overflow-auto rounded-lg bg-gray-950 p-3 text-xs leading-5 text-gray-100">
              {formatJSON(rawPayload)}
            </pre>
          </section>
        </div>
      ) : null}
    </aside>
  )
}

function Th({ children }: { children: ReactNode }) {
  return <th className="whitespace-nowrap px-4 py-3">{children}</th>
}

function Td({ children }: { children: ReactNode }) {
  return <td className="whitespace-nowrap px-4 py-3 text-gray-600">{children}</td>
}

function StatusLabel({ status }: { status: string }) {
  const normalized = status === 'success' ? 'delivered' : status
  const tone = statusTone[normalized] || 'bg-gray-100 text-gray-600'
  const Icon = normalized === 'failed' || normalized === 'dlq' ? AlertTriangle : normalized === 'pending' ? Clock3 : CheckCheck
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ${tone}`}>
      <Icon size={11} />
      {status}
    </span>
  )
}

function PathLabel({ value }: { value: string }) {
  const tone = value.includes('room')
    ? 'bg-violet-50 text-violet-700'
    : value.includes('kafka') || value.includes('offline')
      ? 'bg-amber-50 text-amber-700'
      : 'bg-blue-50 text-blue-700'
  return <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${tone}`}>{value}</span>
}

function Stat({ label, value, danger }: { label: string; value: number; danger?: boolean }) {
  return (
    <div className="min-w-[84px] px-2">
      <div className="text-[10px] font-medium uppercase text-gray-400">{label}</div>
      <div className={`mt-1 text-sm font-bold ${danger ? 'text-red-700' : 'text-gray-950'}`}>{value.toLocaleString()}</div>
    </div>
  )
}

function InfoLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-3 py-0.5">
      <span className="shrink-0 text-gray-400">{label}</span>
      <span className="min-w-0 break-all text-right text-gray-700">{value}</span>
    </div>
  )
}

function EmptyLine({ text }: { text: string }) {
  return <div className="rounded-lg border border-dashed border-gray-200 px-3 py-4 text-center text-xs text-gray-400">{text}</div>
}

function formatTime(value?: string) {
  if (!value) return '-'
  const time = new Date(value)
  if (Number.isNaN(time.getTime())) return value
  return time.toLocaleString('zh-CN')
}

function formatJSON(value: unknown) {
  if (typeof value === 'string') {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }
  return JSON.stringify(value ?? {}, null, 2)
}
