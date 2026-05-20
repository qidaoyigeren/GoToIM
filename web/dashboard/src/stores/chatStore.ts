import { create } from 'zustand'
import type { ChatConversation, ChatMessage, ChatPushPayload } from '@/types/chat'

type ChatStoreState = {
  conversations: ChatConversation[]
  messagesByConversation: Record<string, ChatMessage[]>
  activeConversationId: string
  setConversations: (list: ChatConversation[]) => void
  setActiveConversation: (id: string) => void
  setMessages: (conversationId: string, messages: ChatMessage[]) => void
  upsertMessage: (message: ChatMessage) => void
  receivePush: (payload: ChatPushPayload) => void
}

export const useChatStore = create<ChatStoreState>((set) => ({
  conversations: [],
  messagesByConversation: {},
  activeConversationId: '',
  setConversations: (list) => set({ conversations: Array.isArray(list) ? list : [] }),
  setActiveConversation: (id) => set({ activeConversationId: id }),
  setMessages: (conversationId, messages) =>
    set((s) => ({
      messagesByConversation: {
        ...s.messagesByConversation,
        [conversationId]: Array.isArray(messages) ? messages : [],
      },
    })),
  upsertMessage: (message) =>
    set((s) => ({
      messagesByConversation: upsertMessageInMap(s.messagesByConversation, message),
    })),
  receivePush: (payload) =>
    set((s) => {
      const message: ChatMessage = {
        message_id: payload.message_id,
        conversation_id: payload.conversation_id,
        order_id: payload.order_id,
        sender_uid: payload.sender_uid,
        receiver_uid: payload.receiver_uid,
        sender_role: payload.sender_role,
        body: payload.body,
        status: payload.status ?? 'delivered',
        delivery_path: 'websocket',
        created_at: new Date(payload.timestamp || Date.now()).toISOString(),
      }
      const conversations = s.conversations.map((conv) =>
        conv.conversation_id === payload.conversation_id
          ? {
              ...conv,
              last_message_id: payload.message_id,
              last_message_at: message.created_at,
              unread_count: s.activeConversationId === payload.conversation_id ? conv.unread_count ?? 0 : (conv.unread_count ?? 0) + 1,
            }
          : conv
      )
      return {
        conversations,
        messagesByConversation: upsertMessageInMap(s.messagesByConversation, message),
      }
    }),
}))

function upsertMessageInMap(
  map: Record<string, ChatMessage[]>,
  message: ChatMessage
): Record<string, ChatMessage[]> {
  const list = map[message.conversation_id] ?? []
  const idx = list.findIndex((item) => item.message_id === message.message_id)
  const next = idx >= 0
    ? list.map((item) => (item.message_id === message.message_id ? { ...item, ...message } : item))
    : [...list, message]
  next.sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime())
  return { ...map, [message.conversation_id]: next }
}
