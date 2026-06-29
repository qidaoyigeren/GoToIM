import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  CheckCheck,
  Circle,
  Clock3,
  MessageSquareText,
  Search,
  SendHorizontal,
  UserRound,
  UsersRound,
  Wifi,
} from 'lucide-react'
import {
  createChatConversation,
  joinMerchantGroup,
  listChatConversations,
  listChatMessages,
  sendChatMessage,
  updateChatMessageStatus,
} from '@/api/chat'
import { listMerchantGroups } from '@/api/notify'
import { useChatStore } from '@/stores/chatStore'
import { useConnectionStore } from '@/stores/connectionStore'
import { useIdentityStore } from '@/stores/identityStore'
import type { ChatConversation, ChatMessage, ChatType } from '@/types/chat'

const defaultOrderID = 'ORD-DEMO-CHAT-001'
const defaultRoomID = 'merchant:m_apple_store:buyers'
const customerUID = 10001
const merchantUID = 90001

const quickTexts = {
  customer: ['这个订单什么时候可以处理？', '我需要补充收货要求，请商家确认。'],
  merchant: ['已收到订单，正在安排处理。', '需要更多信息时会在这里同步。'],
}

export default function ChatPage() {
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

  const [orderId, setOrderId] = useState(searchParams.get('order_id') || defaultOrderID)
  const [roomId, setRoomId] = useState(searchParams.get('room_id') || defaultRoomID)
  const [filter, setFilter] = useState<ChatType | 'all'>('all')
  const [draft, setDraft] = useState('')
  const [loading, setLoading] = useState(false)
  const [sending, setSending] = useState(false)

  const groupsQuery = useQuery({ queryKey: ['market', 'groups'], queryFn: () => listMerchantGroups() })
  const groups = groupsQuery.data ?? []

  const activeConversation = useMemo(
    () => conversations.find((item) => item.conversation_id === activeConversationId) ?? conversations[0],
    [activeConversationId, conversations]
  )
  const messages = useMemo(
    () => (activeConversation ? messagesByConversation[activeConversation.conversation_id] ?? [] : []),
    [activeConversation, messagesByConversation]
  )
  const filteredConversations = conversations.filter((item) => filter === 'all' || item.type === filter)
  const unreadCount = conversations.reduce((sum, item) => sum + (item.unread_count ?? 0), 0)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const queryConversationId = searchParams.get('conversation_id')
        const queryOrderId = searchParams.get('order_id')
        const queryRoomId = searchParams.get('room_id')
        let target: ChatConversation | null = null

        if (queryRoomId) {
          target = await joinMerchantGroup(queryRoomId, userId)
        } else if (queryOrderId) {
          target = await createChatConversation(queryOrderId, customerUID, merchantUID)
        }

        const list = await listChatConversations(userId)
        if (cancelled) return
        const merged = target && !list.some((item) => item.conversation_id === target?.conversation_id)
          ? [target, ...list]
          : list
        if (merged.length === 0) {
          target = await createChatConversation(defaultOrderID, customerUID, merchantUID)
          merged.unshift(target)
        }
        setConversations(merged)
        const nextActive =
          target?.conversation_id ||
          queryConversationId ||
          activeConversationId ||
          merged[0]?.conversation_id ||
          ''
        setActiveConversation(nextActive)
        if (nextActive) {
          const history = await listChatMessages(nextActive, userId)
          if (!cancelled) setMessages(nextActive, history)
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [activeConversationId, searchParams, setActiveConversation, setConversations, setMessages, userId])

  useEffect(() => {
    if (!activeConversation) return
    const unread = messages.filter((message) => message.receiver_uid === userId && message.status !== 'read')
    unread.forEach((message) => updateChatMessageStatus(message.message_id, 'read').catch(() => {}))
  }, [activeConversation, messages, userId])

  const openPrivate = async () => {
    const next = orderId.trim() || defaultOrderID
    setOrderId(next)
    setSearchParams({ order_id: next })
  }

  const joinGroup = async (nextRoomID = roomId) => {
    const next = nextRoomID.trim() || defaultRoomID
    setRoomId(next)
    setSearchParams({ room_id: next })
  }

  const selectConversation = async (conversation: ChatConversation) => {
    setActiveConversation(conversation.conversation_id)
    setSearchParams({ conversation_id: conversation.conversation_id })
    const history = await listChatMessages(conversation.conversation_id, userId)
    setMessages(conversation.conversation_id, history)
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
    <div className="space-y-5 animate-fade-in">
      <header className="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-600">Chat</p>
          <h1 className="mt-1 text-2xl font-bold text-gray-950">聊天页面</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-gray-600">
            左侧是会话列表，中间是消息窗口，右侧展示订单或群信息。私聊走 direct push，商家群聊走 room push。
          </p>
        </div>
        <div className="grid grid-cols-4 gap-2 rounded-lg border border-gray-100 bg-white p-3 shadow-sm">
          <Signal label="连接" value={connectionLabel(connState)} tone={connState === 'connected' ? 'green' : 'amber'} />
          <Signal label="当前 UID" value={String(userId)} tone="blue" />
          <Signal label="对方 UID" value={String(peerId)} tone="slate" />
          <Signal label="未读" value={String(unreadCount)} tone={unreadCount > 0 ? 'amber' : 'slate'} />
        </div>
      </header>

      <section className="grid min-h-[720px] grid-cols-1 gap-4 rounded-lg border border-gray-100 bg-white p-4 shadow-sm xl:grid-cols-[300px_minmax(0,1fr)_300px]">
        <aside className="space-y-4 border-gray-100 xl:border-r xl:pr-4">
          <div>
            <div className="mb-2 flex items-center justify-between">
              <h2 className="text-sm font-semibold text-gray-950">会话列表</h2>
              {loading && <span className="text-xs text-gray-400">加载中</span>}
            </div>
            <div className="grid grid-cols-3 gap-1 rounded-lg bg-gray-100 p-1">
              <FilterButton active={filter === 'all'} onClick={() => setFilter('all')}>最近</FilterButton>
              <FilterButton active={filter === 'private'} onClick={() => setFilter('private')}>私聊</FilterButton>
              <FilterButton active={filter === 'group'} onClick={() => setFilter('group')}>群聊</FilterButton>
            </div>
          </div>

          <div className="space-y-2">
            {filteredConversations.map((conversation) => (
              <ConversationRow
                key={conversation.conversation_id}
                conversation={conversation}
                active={activeConversation?.conversation_id === conversation.conversation_id}
                onClick={() => void selectConversation(conversation)}
              />
            ))}
            {filteredConversations.length === 0 && (
              <div className="rounded-lg border border-dashed border-gray-200 px-4 py-8 text-center text-sm text-gray-400">
                暂无会话
              </div>
            )}
          </div>

          <div className="space-y-3 rounded-lg border border-gray-100 bg-gray-50 p-3">
            <div className="flex items-center gap-2 text-xs font-semibold text-gray-600">
              <Search size={14} />
              基于订单打开私聊
            </div>
            <div className="flex gap-2">
              <input
                value={orderId}
                onChange={(event) => setOrderId(event.target.value)}
                className="min-w-0 flex-1 rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
              />
              <button type="button" onClick={() => void openPrivate()} className="rounded-lg bg-gray-900 px-3 py-2 text-sm font-medium text-white hover:bg-gray-800">
                打开
              </button>
            </div>
          </div>

          <div className="space-y-3 rounded-lg border border-gray-100 bg-gray-50 p-3">
            <div className="flex items-center gap-2 text-xs font-semibold text-gray-600">
              <UsersRound size={14} />
              加入商家群聊
            </div>
            <div className="flex gap-2">
              <input
                value={roomId}
                onChange={(event) => setRoomId(event.target.value)}
                className="min-w-0 flex-1 rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
              />
              <button type="button" onClick={() => void joinGroup()} className="rounded-lg bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700">
                加入
              </button>
            </div>
            <div className="flex flex-wrap gap-1">
              {groups.map((group) => (
                <button
                  key={group.room_id}
                  type="button"
                  onClick={() => void joinGroup(group.room_id)}
                  className="rounded-full border border-gray-200 bg-white px-2.5 py-1 text-[11px] text-gray-600 hover:border-blue-200 hover:text-blue-700"
                >
                  {group.name}
                </button>
              ))}
            </div>
          </div>
        </aside>

        <main className="flex min-h-[680px] flex-col">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 pb-4">
            <div>
              <div className="flex items-center gap-2">
                {activeConversation?.type === 'group' ? <UsersRound size={18} className="text-blue-600" /> : <MessageSquareText size={18} className="text-emerald-600" />}
                <h2 className="text-base font-semibold text-gray-950">{conversationTitle(activeConversation)}</h2>
              </div>
              <p className="mt-1 text-xs text-gray-500">
                {activeConversation?.type === 'group' ? 'room push' : 'direct push'} / {activeConversation?.room_id ?? '-'}
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
                发送一条消息，观察 direct push 或 room push 的状态。
              </div>
            ) : (
              messages.map((message) => <MessageBubble key={message.message_id} message={message} own={message.sender_uid === userId} room={activeConversation?.type === 'group'} />)
            )}
          </div>

          <div className="border-t border-gray-100 pt-4">
            <div className="mb-3 flex flex-wrap gap-2">
              {quickTexts[role === 'merchant' ? 'merchant' : 'customer'].map((text) => (
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
                placeholder={role === 'customer' ? '向商家发送订单私聊或群聊消息' : '回复买家消息'}
                className="min-h-[72px] flex-1 resize-none rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
              />
              <button
                type="button"
                disabled={sending || !draft.trim()}
                onClick={() => void send()}
                title="发送消息"
                className="inline-flex h-[72px] w-12 items-center justify-center rounded-lg bg-blue-600 text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-gray-300"
              >
                <SendHorizontal size={18} />
              </button>
            </div>
          </div>
        </main>

        <ConversationInfo conversation={activeConversation} messages={messages} currentUserId={userId} setRole={setRole} role={role} />
      </section>
    </div>
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
        <span className="truncate text-sm font-semibold text-gray-950">{conversationTitle(conversation)}</span>
        {(conversation.unread_count ?? 0) > 0 && (
          <span className="rounded-full bg-amber-500 px-2 py-0.5 text-xs font-semibold text-white">{conversation.unread_count}</span>
        )}
      </div>
      <div className="mt-1 flex items-center gap-2 text-xs text-gray-500">
        <span>{conversation.type === 'group' ? '商家群聊' : '订单私聊'}</span>
        <span>{conversation.type === 'group' ? 'room push' : 'direct push'}</span>
      </div>
      <div className="mt-2 truncate text-xs text-gray-400">{conversation.room_id}</div>
    </button>
  )
}

function MessageBubble({ message, own, room }: { message: ChatMessage; own: boolean; room?: boolean }) {
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
        <p className="mt-2 text-[11px] text-gray-400">
          {room ? 'room push' : 'direct push'} / {message.delivery_path || 'pending'}
        </p>
      </div>
    </div>
  )
}

function ConversationInfo({
  conversation,
  messages,
  currentUserId,
  setRole,
  role,
}: {
  conversation?: ChatConversation
  messages: ChatMessage[]
  currentUserId: number
  setRole: (role: 'customer' | 'merchant' | 'member') => void
  role: 'customer' | 'merchant' | 'member'
}) {
  const delivered = messages.filter((item) => item.status === 'delivered' || item.status === 'read' || item.status === 'sent').length
  const failed = messages.filter((item) => item.status === 'failed').length
  const latestPath = messages[messages.length - 1]?.delivery_path || (conversation?.type === 'group' ? 'room_push' : 'direct_push')
  const members = conversation?.type === 'group'
    ? [
        { uid: conversation.merchant_uid, name: '商家', tone: 'bg-blue-50 text-blue-700' },
        { uid: currentUserId, name: '当前用户', tone: 'bg-emerald-50 text-emerald-700' },
        { uid: 90011, name: '客服', tone: 'bg-amber-50 text-amber-700' },
      ]
    : [
        { uid: conversation?.customer_uid || customerUID, name: '买家', tone: 'bg-emerald-50 text-emerald-700' },
        { uid: conversation?.merchant_uid || merchantUID, name: '商家', tone: 'bg-blue-50 text-blue-700' },
      ]

  return (
    <aside className="space-y-4 border-gray-100 xl:border-l xl:pl-4">
      <section className="rounded-lg border border-gray-100 bg-gray-50 p-4">
        <div className="text-sm font-semibold text-gray-950">会话信息</div>
        <div className="mt-3 space-y-2 text-xs text-gray-500">
          <InfoLine label="类型" value={conversation?.type === 'group' ? '商家群聊' : '订单私聊'} />
          <InfoLine label="订单" value={conversation?.order_id || '-'} />
          <InfoLine label="Room" value={conversation?.room_id || '-'} />
          <InfoLine label="商家 UID" value={conversation?.merchant_uid ? String(conversation.merchant_uid) : '-'} />
        </div>
      </section>

      <section className="rounded-lg border border-gray-100 bg-white p-4">
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-gray-950">{conversation?.type === 'group' ? '群成员' : '双方信息'}</h3>
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500">{members.length}</span>
        </div>
        <div className="space-y-2">
          {members.map((member) => (
            <div key={`${member.uid}-${member.name}`} className="flex items-center justify-between rounded-lg bg-gray-50 px-3 py-2">
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
        <h3 className="text-sm font-semibold text-gray-950">当前投递链路</h3>
        <div className="mt-3 rounded-lg bg-gray-50 p-3 text-xs text-gray-600">
          {conversation?.type === 'group' ? 'room push -> Router room fanout -> Comet room' : 'direct push -> Router direct -> Comet channel'}
        </div>
        <div className="mt-3 grid grid-cols-3 gap-2">
          <Stat label="消息" value={String(messages.length)} />
          <Stat label="送达" value={String(delivered)} />
          <Stat label="失败" value={String(failed)} />
        </div>
        <p className="mt-3 text-xs leading-5 text-gray-500">最近路径：{latestPath}</p>
      </section>

      <section className="rounded-lg border border-gray-100 bg-white p-4">
        <h3 className="text-sm font-semibold text-gray-950">身份切换</h3>
        <div className="mt-3 grid grid-cols-2 gap-2">
          <RoleButton active={role === 'customer'} onClick={() => setRole('customer')} label="买家" />
          <RoleButton active={role === 'merchant'} onClick={() => setRole('merchant')} label="商家" />
        </div>
      </section>
    </aside>
  )
}

function FilterButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-md px-2 py-1.5 text-xs font-medium transition-colors ${
        active ? 'bg-white text-blue-700 shadow-sm' : 'text-gray-500 hover:text-gray-800'
      }`}
    >
      {children}
    </button>
  )
}

function RoleButton({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-lg border px-3 py-2 text-sm font-medium transition-colors ${
        active ? 'border-blue-300 bg-blue-50 text-blue-700' : 'border-gray-200 text-gray-600 hover:bg-gray-50'
      }`}
    >
      {label}
    </button>
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
      <div className="text-sm font-bold text-gray-950">{value}</div>
      <div className="mt-0.5 text-[11px] text-gray-400">{label}</div>
    </div>
  )
}

function Signal({ label, value, tone }: { label: string; value: string; tone: 'green' | 'amber' | 'blue' | 'slate' }) {
  const color = {
    green: 'text-emerald-700',
    amber: 'text-amber-700',
    blue: 'text-blue-700',
    slate: 'text-gray-700',
  }[tone]
  return (
    <div className="min-w-[90px] px-2">
      <div className="text-[10px] font-medium uppercase text-gray-400">{label}</div>
      <div className={`mt-1 truncate text-xs font-semibold ${color}`}>{value}</div>
    </div>
  )
}

function conversationTitle(conversation?: ChatConversation) {
  if (!conversation) return '未选择会话'
  if (conversation.title) return conversation.title
  if (conversation.type === 'group') return conversation.room_id
  return conversation.order_id || conversation.conversation_id
}

function statusLabel(status: ChatMessage['status']) {
  return {
    pending: '待发送',
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
