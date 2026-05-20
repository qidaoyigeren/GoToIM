import { isDemoMode } from '@/config'
import { notifyClient } from './client'
import type { ApiResponse } from '@/types/api'
import type { ChatConversation, ChatMessage, ChatStatus } from '@/types/chat'

type ChatResponse<T> = ApiResponse<T>

const DEMO_KEY = 'gotoim_demo_chat'

export async function createChatConversation(
  orderId: string,
  customerUid = 10001,
  merchantUid = 90001
): Promise<ChatConversation> {
  if (isDemoMode()) return mockCreateConversation(orderId, customerUid, merchantUid)
  const res = await notifyClient.request<ChatResponse<ChatConversation>>('/chat/conversations', {
    method: 'POST',
    body: { order_id: orderId, customer_uid: customerUid, merchant_uid: merchantUid },
  })
  return res.data
}

export async function listChatConversations(userId: number): Promise<ChatConversation[]> {
  if (isDemoMode()) return mockListConversations(userId)
  const res = await notifyClient.request<ChatResponse<ChatConversation[]>>('/chat/conversations', {
    params: { user_id: userId },
  })
  return res.data ?? []
}

export async function listChatMessages(
  conversationId: string,
  userId: number
): Promise<ChatMessage[]> {
  if (isDemoMode()) return mockListMessages(conversationId)
  const res = await notifyClient.request<ChatResponse<ChatMessage[]>>(
    `/chat/conversations/${conversationId}/messages`,
    { params: { user_id: userId } }
  )
  return res.data ?? []
}

export async function sendChatMessage(
  conversationId: string,
  senderUid: number,
  body: string
): Promise<ChatMessage> {
  if (isDemoMode()) return mockSendMessage(conversationId, senderUid, body)
  const res = await notifyClient.request<ChatResponse<ChatMessage>>(
    `/chat/conversations/${conversationId}/messages`,
    { method: 'POST', body: { sender_uid: senderUid, body } }
  )
  return res.data
}

export async function updateChatMessageStatus(
  messageId: string,
  status: ChatStatus = 'read'
): Promise<void> {
  if (isDemoMode()) {
    mockUpdateStatus(messageId, status)
    return
  }
  await notifyClient.request(`/chat/messages/${messageId}/status`, {
    method: 'PATCH',
    body: { status },
  })
}

type DemoChatData = {
  conversations: ChatConversation[]
  messages: ChatMessage[]
}

function readDemo(): DemoChatData {
  try {
    const raw = localStorage.getItem(DEMO_KEY)
    if (raw) return JSON.parse(raw) as DemoChatData
  } catch {
    // ignore corrupted demo state
  }
  return { conversations: [], messages: [] }
}

function writeDemo(data: DemoChatData) {
  try {
    localStorage.setItem(DEMO_KEY, JSON.stringify(data))
  } catch {
    // ignore storage errors
  }
}

function mockCreateConversation(orderId: string, customerUid: number, merchantUid: number) {
  const data = readDemo()
  const existing = data.conversations.find(
    (item) => item.order_id === orderId && item.customer_uid === customerUid && item.merchant_uid === merchantUid
  )
  if (existing) return existing
  const now = new Date().toISOString()
  const conv: ChatConversation = {
    conversation_id: `CHAT-${orderId}-${customerUid}-${merchantUid}`,
    order_id: orderId,
    customer_uid: customerUid,
    merchant_uid: merchantUid,
    room_id: `order_chat:${orderId}`,
    unread_count: 0,
    created_at: now,
    updated_at: now,
  }
  data.conversations.unshift(conv)
  writeDemo(data)
  return conv
}

function mockListConversations(userId: number) {
  const data = readDemo()
  return data.conversations.filter((item) => item.customer_uid === userId || item.merchant_uid === userId)
}

function mockListMessages(conversationId: string) {
  return readDemo().messages.filter((item) => item.conversation_id === conversationId)
}

function mockSendMessage(conversationId: string, senderUid: number, body: string) {
  const data = readDemo()
  const conv = data.conversations.find((item) => item.conversation_id === conversationId)
  if (!conv) throw new Error('conversation not found')
  const receiverUid = senderUid === conv.customer_uid ? conv.merchant_uid : conv.customer_uid
  const msg: ChatMessage = {
    message_id: `CHM-${Date.now()}`,
    conversation_id: conversationId,
    order_id: conv.order_id,
    sender_uid: senderUid,
    receiver_uid: receiverUid,
    sender_role: senderUid === conv.customer_uid ? 'customer' : 'merchant',
    body,
    status: 'delivered',
    delivery_path: 'mock_direct',
    created_at: new Date().toISOString(),
    delivered_at: new Date().toISOString(),
  }
  data.messages.push(msg)
  conv.last_message_id = msg.message_id
  conv.last_message_at = msg.created_at
  conv.updated_at = msg.created_at
  writeDemo(data)
  return msg
}

function mockUpdateStatus(messageId: string, status: ChatStatus) {
  const data = readDemo()
  data.messages = data.messages.map((item) =>
    item.message_id === messageId
      ? { ...item, status, read_at: status === 'read' ? new Date().toISOString() : item.read_at }
      : item
  )
  writeDemo(data)
}
