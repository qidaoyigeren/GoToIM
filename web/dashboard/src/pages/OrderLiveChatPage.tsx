import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { CheckCheck, Clock3, MessageSquareText, SendHorizontal, Wifi } from 'lucide-react'
import {
  createChatConversation,
  listChatConversations,
  listChatMessages,
  sendChatMessage,
  updateChatMessageStatus,
} from '@/api/chat'
import { useChatStore } from '@/stores/chatStore'
import { useConnectionStore } from '@/stores/connectionStore'
import { useIdentityStore } from '@/stores/identityStore'
import type { ChatConversation, ChatMessage } from '@/types/chat'

const CUSTOMER_UID = 10001
const MERCHANT_UID = 90001
const DEFAULT_ORDER_ID = 'ORD-LIVE-CHAT-001'

const quickActions = [
  { role: 'customer', text: '我的包裹到哪里了？' },
  { role: 'customer', text: '请帮我确认一下收货地址' },
  { role: 'merchant', text: '您的订单已发货，物流很快会更新。' },
  { role: 'merchant', text: '我现在可以帮您核对收货地址。' },
] as const

export default function OrderLiveChatPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const role = useIdentityStore((s) => s.role)
  const userId = useIdentityStore((s) => s.userId)
  const peerId = useIdentityStore((s) => s.peerId)
  const setRole = useIdentityStore((s) => s.setRole)
  const connState = useConnectionStore((s) => s.state)
  const conversations = useChatStore((s) => s.conversations)
  const setConversations = useChatStore((s) => s.setConversations)
  const activeConversationId = useChatStore((s) => s.activeConversationId)
  const setActiveConversation = useChatStore((s) => s.setActiveConversation)
  const messagesByConversation = useChatStore((s) => s.messagesByConversation)
  const setMessages = useChatStore((s) => s.setMessages)
  const upsertMessage = useChatStore((s) => s.upsertMessage)

  const [orderId, setOrderId] = useState(searchParams.get('order_id') || DEFAULT_ORDER_ID)
  const [draft, setDraft] = useState('')
  const [loading, setLoading] = useState(false)
  const [sending, setSending] = useState(false)

  const activeConversation = useMemo(
    () => conversations.find((item) => item.conversation_id === activeConversationId) ?? conversations[0],
    [conversations, activeConversationId]
  )
  const messages = useMemo(
    () => (activeConversation ? messagesByConversation[activeConversation.conversation_id] ?? [] : []),
    [activeConversation, messagesByConversation]
  )
  const unreadCount = conversations.reduce((sum, item) => sum + (item.unread_count ?? 0), 0)
  const lastMessageAt = activeConversation?.last_message_at || messages[messages.length - 1]?.created_at

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const conv = await createChatConversation(orderId, CUSTOMER_UID, MERCHANT_UID)
        const list = await listChatConversations(userId)
        if (cancelled) return
        setConversations(list.length ? list : [conv])
        setActiveConversation(conv.conversation_id)
        const history = await listChatMessages(conv.conversation_id, userId)
        if (!cancelled) setMessages(conv.conversation_id, history)
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [orderId, setActiveConversation, setConversations, setMessages, userId])

  useEffect(() => {
    if (!activeConversation) return
    const unread = messages.filter((msg) => msg.receiver_uid === userId && msg.status !== 'read')
    unread.forEach((msg) => {
      updateChatMessageStatus(msg.message_id, 'read').catch(() => {})
    })
  }, [activeConversation, messages, userId])

  const openOrder = () => {
    const next = orderId.trim() || DEFAULT_ORDER_ID
    setOrderId(next)
    setSearchParams({ order_id: next })
  }

  const send = async (body = draft) => {
    if (!activeConversation || !body.trim()) return
    setSending(true)
    try {
      const msg = await sendChatMessage(activeConversation.conversation_id, userId, body.trim())
      upsertMessage(msg)
      setDraft('')
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="space-y-6 animate-fade-in">
      <div className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-600">GoIM 直连场景</p>
          <h1 className="mt-1 text-2xl font-bold text-gray-950">订单在线客服</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-gray-600">
            客户与商家围绕订单实时沟通，消息直接经过 Logic、Router、Comet、ACK 和离线追踪链路，不进入通知 outbox 投递流水线。
          </p>
        </div>
        <div className="grid grid-cols-2 gap-2 rounded-lg border border-gray-200 bg-white p-2 shadow-sm sm:grid-cols-4">
          <Signal label="连接状态" value={connectionLabel(connState)} tone={connState === 'connected' ? 'green' : 'amber'} />
          <Signal label="当前 UID" value={String(userId)} tone="blue" />
          <Signal label="对方 UID" value={String(peerId)} tone="slate" />
          <Signal label="未读数" value={String(unreadCount)} tone={unreadCount > 0 ? 'amber' : 'slate'} />
        </div>
      </div>

      <section className="grid grid-cols-1 gap-4 rounded-lg border border-gray-200 bg-white p-4 shadow-sm xl:grid-cols-[300px_minmax(0,1fr)]">
        <aside className="space-y-4 border-gray-100 xl:border-r xl:pr-4">
          <div>
            <label className="text-xs font-medium uppercase text-gray-400">演示身份</label>
            <div className="mt-2 grid grid-cols-2 gap-2">
              <RoleButton active={role === 'customer'} onClick={() => setRole('customer')} label="客户" detail="UID 10001" />
              <RoleButton active={role === 'merchant'} onClick={() => setRole('merchant')} label="商家客服" detail="UID 90001" />
            </div>
          </div>

          <div>
            <label className="text-xs font-medium uppercase text-gray-400">订单入口</label>
            <div className="mt-2 flex gap-2">
              <input
                value={orderId}
                onChange={(e) => setOrderId(e.target.value)}
                className="min-w-0 flex-1 rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
              />
              <button
                type="button"
                onClick={openOrder}
                className="rounded-lg bg-gray-900 px-3 py-2 text-sm font-medium text-white hover:bg-gray-800"
              >
                打开
              </button>
            </div>
          </div>

          <div>
            <div className="mb-2 flex items-center justify-between">
              <h2 className="text-sm font-semibold text-gray-900">订单会话</h2>
              {loading && <span className="text-xs text-gray-400">加载中</span>}
            </div>
            <div className="space-y-2">
              {conversations.map((conv) => (
                <ConversationRow
                  key={conv.conversation_id}
                  conversation={conv}
                  active={activeConversation?.conversation_id === conv.conversation_id}
                  onClick={() => setActiveConversation(conv.conversation_id)}
                />
              ))}
            </div>
          </div>
        </aside>

        <div className="flex min-h-[620px] flex-col">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 pb-4">
            <div>
              <div className="flex items-center gap-2">
                <MessageSquareText size={18} className="text-emerald-600" />
                <h2 className="text-base font-semibold text-gray-950">{activeConversation?.room_id ?? '未选择房间'}</h2>
              </div>
              <p className="mt-1 text-xs text-gray-500">
                订单 {activeConversation?.order_id ?? orderId} · 直连 IM 房间 · 最后一条消息 {lastMessageAt ? new Date(lastMessageAt).toLocaleString() : '暂无'}
              </p>
            </div>
            <div className="inline-flex items-center gap-2 rounded-lg border border-emerald-100 bg-emerald-50 px-3 py-2 text-xs font-medium text-emerald-700">
              <Wifi size={14} />
              Logic / Router / Comet
            </div>
          </div>

          <div className="flex-1 space-y-3 overflow-y-auto py-4">
            {messages.length === 0 ? (
              <div className="flex h-full min-h-[360px] items-center justify-center text-sm text-gray-400">
                从一个订单咨询问题开始这次客服会话。
              </div>
            ) : (
              messages.map((msg) => <MessageBubble key={msg.message_id} message={msg} own={msg.sender_uid === userId} />)
            )}
          </div>

          <div className="border-t border-gray-100 pt-4">
            <div className="mb-3 flex flex-wrap gap-2">
              {quickActions
                .filter((item) => item.role === role)
                .map((item) => (
                  <button
                    key={item.text}
                    type="button"
                    onClick={() => setDraft(item.text)}
                    className="rounded-full border border-gray-200 bg-gray-50 px-3 py-1.5 text-xs text-gray-600 hover:border-blue-200 hover:bg-blue-50 hover:text-blue-700"
                  >
                    {item.text}
                  </button>
                ))}
            </div>
            <div className="flex gap-3">
              <textarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey) {
                    e.preventDefault()
                    void send()
                  }
                }}
                placeholder={role === 'customer' ? '咨询这个订单...' : '回复客户问题...'}
                className="min-h-[72px] flex-1 resize-none rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
              />
              <button
                type="button"
                disabled={sending || !draft.trim()}
                onClick={() => void send()}
                title="发送直连 IM 消息"
                className="inline-flex h-[72px] w-12 items-center justify-center rounded-lg bg-blue-600 text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-gray-300"
              >
                <SendHorizontal size={18} />
              </button>
            </div>
          </div>
        </div>
      </section>
    </div>
  )
}

function RoleButton({ active, onClick, label, detail }: { active: boolean; onClick: () => void; label: string; detail: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-lg border px-3 py-2 text-left transition-colors ${
        active ? 'border-blue-300 bg-blue-50 text-blue-800' : 'border-gray-200 bg-white text-gray-600 hover:bg-gray-50'
      }`}
    >
      <div className="text-sm font-semibold">{label}</div>
      <div className="mt-0.5 text-xs opacity-75">{detail}</div>
    </button>
  )
}

function ConversationRow({ conversation, active, onClick }: { conversation: ChatConversation; active: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`w-full rounded-lg border px-3 py-3 text-left transition-colors ${
        active ? 'border-blue-200 bg-blue-50' : 'border-gray-100 bg-gray-50 hover:bg-white'
      }`}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-sm font-semibold text-gray-900">{conversation.order_id}</span>
        {(conversation.unread_count ?? 0) > 0 && (
          <span className="rounded-full bg-amber-500 px-2 py-0.5 text-xs font-semibold text-white">{conversation.unread_count}</span>
        )}
      </div>
      <div className="mt-1 text-xs text-gray-500">{conversation.room_id}</div>
      <div className="mt-2 text-xs text-gray-400">
        {conversation.last_message_at ? new Date(conversation.last_message_at).toLocaleString() : '暂无消息'}
      </div>
    </button>
  )
}

function MessageBubble({ message, own }: { message: ChatMessage; own: boolean }) {
  return (
    <div className={`flex ${own ? 'justify-end' : 'justify-start'}`}>
      <div className={`max-w-[74%] rounded-lg border px-4 py-3 ${own ? 'border-blue-100 bg-blue-50' : 'border-gray-200 bg-white'}`}>
        <div className="mb-1 flex flex-wrap items-center gap-2 text-xs">
          <span className={`font-semibold ${own ? 'text-blue-700' : 'text-gray-800'}`}>
            {message.sender_role === 'customer' ? '客户' : '商家客服'} · UID {message.sender_uid}
          </span>
          <span className="text-gray-400">{new Date(message.created_at).toLocaleTimeString()}</span>
          <StatusChip status={message.status} />
        </div>
        <p className="whitespace-pre-wrap break-words text-sm leading-6 text-gray-800">{message.body}</p>
        {message.delivery_path && <p className="mt-2 text-[11px] text-gray-400">链路：{message.delivery_path}</p>}
      </div>
    </div>
  )
}

function StatusChip({ status }: { status: ChatMessage['status'] }) {
  const tone = {
    pending: 'bg-amber-50 text-amber-700',
    sent: 'bg-blue-50 text-blue-700',
    delivered: 'bg-emerald-50 text-emerald-700',
    read: 'bg-gray-100 text-gray-600',
    failed: 'bg-red-50 text-red-700',
  }[status]
  const Icon = status === 'pending' ? Clock3 : CheckCheck
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 ${tone}`}>
      <Icon size={11} />
      {statusLabel(status)}
    </span>
  )
}

function statusLabel(status: ChatMessage['status']) {
  return {
    pending: '待投递',
    sent: '已发送',
    delivered: '已送达',
    read: '已读',
    failed: '失败',
  }[status]
}

function connectionLabel(state: string) {
  return {
    connected: '已连接',
    connecting: '连接中',
    reconnecting: '重连中',
    disconnected: '未连接',
  }[state] ?? state
}

function Signal({ label, value, tone }: { label: string; value: string; tone: 'green' | 'amber' | 'blue' | 'slate' }) {
  const color = {
    green: 'text-emerald-700',
    amber: 'text-amber-700',
    blue: 'text-blue-700',
    slate: 'text-gray-700',
  }[tone]
  return (
    <div className="min-w-[120px] px-3 py-2">
      <div className="text-[10px] font-medium uppercase text-gray-400">{label}</div>
      <div className={`mt-1 truncate text-xs font-semibold ${color}`}>{value}</div>
    </div>
  )
}
