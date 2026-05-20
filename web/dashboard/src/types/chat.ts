export type ChatRole = 'customer' | 'merchant'
export type ChatStatus = 'pending' | 'sent' | 'delivered' | 'read' | 'failed'

export interface ChatConversation {
  conversation_id: string
  order_id: string
  customer_uid: number
  merchant_uid: number
  room_id: string
  last_message_id?: string
  last_message_at?: string
  unread_count?: number
  created_at: string
  updated_at: string
}

export interface ChatMessage {
  message_id: string
  conversation_id: string
  order_id: string
  sender_uid: number
  receiver_uid: number
  sender_role: ChatRole
  body: string
  status: ChatStatus
  delivery_path?: string
  created_at: string
  delivered_at?: string
  read_at?: string
}

export interface ChatPushPayload {
  type: 'chat_message'
  message_id: string
  conversation_id: string
  order_id: string
  room_id: string
  sender_uid: number
  receiver_uid: number
  sender_role: ChatRole
  body: string
  status?: ChatStatus
  timestamp: number
}
