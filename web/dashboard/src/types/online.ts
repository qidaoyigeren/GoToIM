export interface OnlineStats {
  ip_count: number
  conn_count: number
  user_count: number
  offline_pending: number
  direct_pushed: number
  kafka_fallback: number
}

export interface Session {
  sid: string
  uid: number
  key: string
  device_id: string
  platform: 'web' | 'android' | 'ios'
  server: string
  room_id: string
  online: boolean
  last_hb: number // unix timestamp ms
  created_at: string
}

export interface RoomInfo {
  room_id: string
  online_count: number
}

export interface CometNode {
  server: string
  domain: string
  tcp_port: number
  ws_port: number
  wss_port: number
  weight: number
  region: string
}
