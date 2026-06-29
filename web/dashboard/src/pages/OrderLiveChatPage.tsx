import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { CheckCheck, Circle, Clock3, MessageSquareText, SendHorizontal, UserRound, UsersRound, Wifi } from 'lucide-react'
import {
  createChatConversation,
  joinMerchantGroup,
  listChatConversations,
  listChatMessages,
  sendChatMessage,
  updateChatMessageStatus,
} from '@/api/chat'
import { useChatStore } from '@/stores/chatStore'
import { useConnectionStore } from '@/stores/connectionStore'
import { useIdentityStore } from '@/stores/identityStore'
import { CARD_SM } from '@/components/ui/cardStyles'
import type { ChatConversation, ChatMessage } from '@/types/chat'

const CUSTOMER_UID = 10001
const MERCHANT_UID = 90001
const DEFAULT_ORDER_ID = 'ORD-LIVE-CHAT-001'

const quickActions = {
  customer: [
    '这个订单可以今天处理吗？',
    '我想确认一下收货地址和配送时间。',
  ],
  merchant: [
    '商家已收到订单，正在安排处理。',
    '如果需要加急，我会同步更新订单状态。',
  ],
}

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
  const [roomId, setRoomId] = useState(searchParams.get('room_id') || '')
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
        let targetConversation: ChatConversation | null = null
        if (roomId) {
          targetConversation = await joinMerchantGroup(roomId, userId)
        } else {
          targetConversation = await createChatConversation(orderId, CUSTOMER_UID, MERCHANT_UID)
        }
        const list = await listChatConversations(userId)
        if (cancelled || !targetConversation) return
        const merged = list.some((item) => item.conversation_id === targetConversation?.conversation_id)
          ? list
          : [targetConversation, ...list]
        setConversations(merged)
        setActiveConversation(targetConversation.conversation_id)
        const history = await listChatMessages(targetConversation.conversation_id, userId)
        if (!cancelled) setMessages(targetConversation.conversation_id, history)
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [orderId, roomId, setActiveConversation, setConversations, setMessages, userId])

  useEffect(() => {
    if (!activeConversation) return
    const unread = messages.filter((message) => message.receiver_uid === userId && message.status !== 'read')
    unread.forEach((message) => {
      updateChatMessageStatus(message.message_id, 'read').catch(() => {})
    })
  }, [activeConversation, messages, userId])

  const openPrivate = () => {
    const next = orderId.trim() || DEFAULT_ORDER_ID
    setOrderId(next)
    setRoomId('')
    setSearchParams({ order_id: next })
  }

  const joinGroup = () => {
    const next = roomId.trim()
    if (!next) return
    setRoomId(next)
    setSearchParams({ room_id: next })
  }

  const send = async (body = draft) => {
    if (!activeConversation || !body.trim()) return
    setSending(true)
    try {
      const message = await sendChatMessage(activeConversation.conversation_id, userId, body.trim())
      upsertMessage(message)
      setDraft('')
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="space-y-6 animate-fade-in">
      <div className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-600">GoIM 消息会话</p>
          <h1 className="mt-1 text-2xl font-bold text-gray-950">订单私聊与商家群聊</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-gray-600">
            订单私聊走用户 direct push；商家群聊走 room push。消息状态展示 Logic / Router / Comet 投递结果。
          </p>
        </div>
        <div className={`${CARD_SM} grid grid-cols-2 gap-2 sm:grid-cols-4`}>
          <Signal label="连接状态" value={connectionLabel(connState)} tone={connState === 'connected' ? 'green' : 'amber'} />
          <Signal label="当前 UID" value={String(userId)} tone="blue" />
          <Signal label="对方 UID" value={String(peerId)} tone="slate" />
          <Signal label="未读数" value={String(unreadCount)} tone={unreadCount > 0 ? 'amber' : 'slate'} />
        </div>
      </div>

      <section className={`${CARD_SM} grid grid-cols-1 gap-4 xl:grid-cols-[300px_minmax(0,1fr)_280px]`}>
        <aside className="space-y-4 border-gray-100 xl:border-r xl:pr-4">
          <div>
            <label className="text-xs font-medium uppercase text-gray-400">演示身份</label>
            <div className="mt-2 grid grid-cols-2 gap-2">
              <RoleButton active={role === 'customer'} onClick={() => setRole('customer')} label="买家" detail="UID 10001" />
              <RoleButton active={role === 'merchant'} onClick={() => setRole('merchant')} label="商家" detail="UID 90001" />
            </div>
          </div>

          <div>
            <label className="text-xs font-medium uppercase text-gray-400">订单私聊</label>
            <div className="mt-2 flex gap-2">
              <input
                value={orderId}
                onChange={(event) => setOrderId(event.target.value)}
                className="min-w-0 flex-1 rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
              />
              <button type="button" onClick={openPrivate} className="rounded-lg bg-gray-900 px-3 py-2 text-sm font-medium text-white hover:bg-gray-800">
                打开
              </button>
            </div>
          </div>

          <div>
            <label className="text-xs font-medium uppercase text-gray-400">商家群聊 Room</label>
            <div className="mt-2 flex gap-2">
              <input
                value={roomId}
                onChange={(event) => setRoomId(event.target.value)}
                placeholder="merchant:m_apple_store:buyers"
                className="min-w-0 flex-1 rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
              />
              <button type="button" onClick={joinGroup} className="rounded-lg bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700">
                加入
              </button>
            </div>
          </div>

          <div>
            <div className="mb-2 flex items-center justify-between">
              <h2 className="text-sm font-semibold text-gray-900">会话</h2>
              {loading && <span className="text-xs text-gray-400">加载中</span>}
            </div>
            <div className="space-y-2">
              {conversations.map((conversation) => (
                <ConversationRow
                  key={conversation.conversation_id}
                  conversation={conversation}
                  active={activeConversation?.conversation_id === conversation.conversation_id}
                  onClick={() => setActiveConversation(conversation.conversation_id)}
                />
              ))}
            </div>
          </div>
        </aside>

        <div className="flex min-h-[680px] flex-col">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 pb-4">
            <div>
              <div className="flex items-center gap-2">
                {activeConversation?.type === 'group' ? <UsersRound size={18} className="text-blue-600" /> : <MessageSquareText size={18} className="text-emerald-600" />}
                <h2 className="text-base font-semibold text-gray-950">{activeConversation?.title || activeConversation?.room_id || '未选择会话'}</h2>
              </div>
              <p className="mt-1 text-xs text-gray-500">
                {activeConversation?.type === 'group' ? 'room push' : 'direct push'} / {activeConversation?.room_id ?? '-'} / 最近消息 {lastMessageAt ? new Date(lastMessageAt).toLocaleString('zh-CN') : '暂无'}
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
                发送一条消息，观察 direct push 或 room push 的投递状态。
              </div>
            ) : (
              messages.map((message) => <MessageBubble key={message.message_id} message={message} own={message.sender_uid === userId} />)
            )}
          </div>

          <div className="border-t border-gray-100 pt-4">
            <div className="mb-3 flex flex-wrap gap-2">
              {quickActions[role === 'merchant' ? 'merchant' : 'customer'].map((text) => (
                <button
                  key={text}
                  type="button"
                  onClick={() => setDraft(text)}
                  className="rounded-full border border-gray-200 bg-gray-50 px-3 py-1.5 text-xs text-gray-600 hover:border-blue-200 hover:bg-blue-50 hover:text-blue-700"
                >
                  {text}
                </button>
              ))}
            </div>
            <div className="flex gap-3">
              <textarea
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.shiftKey) {
                    event.preventDefault()
                    void send()
                  }
                }}
                placeholder={role === 'customer' ? '向商家咨询订单...' : '回复买家或群聊消息...'}
                className="min-h-[72px] flex-1 resize-none rounded-xl border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
              />
              <button
                type="button"
                disabled={sending || !draft.trim()}
                onClick={() => void send()}
                title="发送 GoIM 消息"
                className="inline-flex h-[72px] w-12 items-center justify-center rounded-lg bg-blue-600 text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-gray-300"
              >
                <SendHorizontal size={18} />
              </button>
            </div>
          </div>
        </div>

        <ConversationInfo conversation={activeConversation} messages={messages} currentUserId={userId} />
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
        <span className="truncate text-sm font-semibold text-gray-900">{conversation.title || conversation.order_id || conversation.room_id}</span>
        {(conversation.unread_count ?? 0) > 0 && (
          <span className="rounded-full bg-amber-500 px-2 py-0.5 text-xs font-semibold text-white">{conversation.unread_count}</span>
        )}
      </div>
      <div className="mt-1 text-xs text-gray-500">{conversation.type === 'group' ? '群聊' : '订单私聊'} / {conversation.room_id}</div>
      <div className="mt-2 text-xs text-gray-400">
        {conversation.last_message_at ? new Date(conversation.last_message_at).toLocaleString('zh-CN') : '暂无消息'}
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
            {roleLabel(message.sender_role)} / UID {message.sender_uid}
          </span>
          <span className="text-gray-400">{new Date(message.created_at).toLocaleTimeString('zh-CN')}</span>
          <StatusChip status={message.status} />
        </div>
        <p className="whitespace-pre-wrap break-words text-sm leading-6 text-gray-800">{message.body}</p>
        {message.delivery_path && <p className="mt-2 text-[11px] text-gray-400">链路：{message.delivery_path}</p>}
      </div>
    </div>
  )
}

function ConversationInfo({
  conversation,
  messages,
  currentUserId,
}: {
  conversation?: ChatConversation
  messages: ChatMessage[]
  currentUserId: number
}) {
  const members = buildMembers(conversation, currentUserId)
  const delivered = messages.filter((item) => item.status === 'delivered' || item.status === 'read').length
  const read = messages.filter((item) => item.status === 'read').length

  return (
    <aside className="space-y-4 border-gray-100 xl:border-l xl:pl-4">
      <section className="rounded-lg border border-gray-100 bg-gray-50 p-4">
        <div className="flex items-center gap-2 text-sm font-semibold text-gray-900">
          {conversation?.type === 'group' ? <UsersRound size={16} /> : <UserRound size={16} />}
          会话信息
        </div>
        <div className="mt-3 space-y-2 text-xs text-gray-500">
          <InfoLine label="类型" value={conversation?.type === 'group' ? '商家群聊' : '订单私聊'} />
          <InfoLine label="Room" value={conversation?.room_id || '-'} />
          <InfoLine label="订单" value={conversation?.order_id || '-'} />
          <InfoLine label="商家 UID" value={conversation?.merchant_uid ? String(conversation.merchant_uid) : '-'} />
        </div>
      </section>

      <section className="rounded-lg border border-gray-100 bg-white p-4">
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-gray-900">{conversation?.type === 'group' ? '群成员' : '参与方'}</h3>
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500">{members.length}</span>
        </div>
        <div className="space-y-2">
          {members.map((member) => (
            <div key={`${member.uid}-${member.role}`} className="flex items-center justify-between rounded-lg bg-gray-50 px-3 py-2">
              <div className="flex min-w-0 items-center gap-2">
                <div className={`flex h-8 w-8 items-center justify-center rounded-full ${member.tone}`}>
                  <UserRound size={14} />
                </div>
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium text-gray-800">{member.name}</div>
                  <div className="text-xs text-gray-400">UID {member.uid}</div>
                </div>
              </div>
              <span className="inline-flex items-center gap-1 text-xs text-emerald-600">
                <Circle size={8} fill="currentColor" />
                在线
              </span>
            </div>
          ))}
        </div>
      </section>

      <section className="rounded-lg border border-gray-100 bg-white p-4">
        <h3 className="text-sm font-semibold text-gray-900">消息状态</h3>
        <div className="mt-3 grid grid-cols-3 gap-2">
          <Stat label="消息" value={String(messages.length)} />
          <Stat label="送达" value={String(delivered)} />
          <Stat label="已读" value={String(read)} />
        </div>
        <p className="mt-3 text-xs leading-5 text-gray-500">
          私聊使用用户 direct push；群聊使用 room push。送达、已读状态由后端消息状态和 ACK 流程驱动。
        </p>
      </section>
    </aside>
  )
}

function InfoLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-2">
      <span className="shrink-0 text-gray-400">{label}</span>
      <span className="min-w-0 truncate text-right text-gray-700">{value}</span>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg bg-gray-50 px-2 py-2 text-center">
      <div className="text-sm font-bold text-gray-900">{value}</div>
      <div className="mt-0.5 text-[11px] text-gray-400">{label}</div>
    </div>
  )
}

function buildMembers(conversation: ChatConversation | undefined, currentUserId: number) {
  if (!conversation) return []
  if (conversation.type === 'group') {
    return [
      { uid: conversation.merchant_uid, role: 'merchant', name: '商家客服', tone: 'bg-blue-50 text-blue-700' },
      { uid: currentUserId, role: 'member', name: '当前用户', tone: 'bg-emerald-50 text-emerald-700' },
      { uid: 90011, role: 'support', name: '履约专员', tone: 'bg-amber-50 text-amber-700' },
      { uid: 90012, role: 'support', name: '售后专员', tone: 'bg-gray-100 text-gray-600' },
    ]
  }
  return [
    { uid: conversation.customer_uid || 10001, role: 'customer', name: '买家', tone: 'bg-emerald-50 text-emerald-700' },
    { uid: conversation.merchant_uid, role: 'merchant', name: '商家客服', tone: 'bg-blue-50 text-blue-700' },
  ]
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
    pending: '等待投递',
    sent: '已发送',
    delivered: '已送达',
    read: '已读',
    failed: '失败',
  }[status]
}

function roleLabel(role: ChatMessage['sender_role']) {
  return {
    customer: '买家',
    merchant: '商家',
    member: '群成员',
  }[role]
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
