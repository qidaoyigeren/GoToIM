import config from '@/config'
import {
  OP_AUTH, OP_AUTH_REPLY, OP_HEARTBEAT, OP_HEARTBEAT_REPLY,
  OP_PUSH_MSG_ACK, OP_SYNC_REPLY, OP_RAW, OP_KICK_CONNECTION,
  buildProto, parseProto, buildAuthBody, parsePushBody,
} from './protocol'
import type { ConnectionState } from '@/stores/connectionStore'

export type WSMessageHandler = (event: {
  type: 'push' | 'ack' | 'sync' | 'heartbeat' | 'kick'
  data: Record<string, unknown>
}) => void

export type WSStatusHandler = (state: ConnectionState) => void

export class GoimWSClient {
  private ws: WebSocket | null = null
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private seq = 0
  private reconnectAttempts = 0
  private intentionalClose = false
  private url: string
  private userId: string
  private onMessage: WSMessageHandler | null = null
  private onStatusChange: WSStatusHandler | null = null
  private lastHeartbeatSent = 0

  constructor(url?: string, userId?: string) {
    this.url = url || config.wsUrl
    this.userId = userId || config.defaultUserId
  }

  setMessageHandler(handler: WSMessageHandler) { this.onMessage = handler }
  setStatusHandler(handler: WSStatusHandler) { this.onStatusChange = handler }

  connect() {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return
    }

    this.intentionalClose = false
    this.onStatusChange?.('connecting')

    try {
      this.ws = new WebSocket(this.url)
    } catch {
      this.onStatusChange?.('disconnected')
      this.scheduleReconnect()
      return
    }

    this.ws.binaryType = 'arraybuffer'

    this.ws.onopen = () => {
      console.log('[GoimWS] socket open, sending auth for user', this.userId)
      this.reconnectAttempts = 0
      this.sendAuth()
    }

    this.ws.onmessage = (event) => {
      if (!(event.data instanceof ArrayBuffer)) return
      this.handleMessage(event.data)
    }

    this.ws.onclose = (e) => {
      console.log('[GoimWS] socket closed, code:', e.code, 'intentional:', this.intentionalClose)
      this.stopHeartbeat()
      if (!this.intentionalClose) {
        this.onStatusChange?.('reconnecting')
        this.scheduleReconnect()
      } else {
        this.onStatusChange?.('disconnected')
      }
    }

    this.ws.onerror = () => {
      console.warn(
        `[GoimWS] WebSocket connection to ${this.url} failed. ` +
        'If the backend comet server is not running, enable demo mode in the dashboard settings.'
      )
    }
  }

  disconnect() {
    this.intentionalClose = true
    this.stopHeartbeat()
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.ws?.close()
    this.ws = null
    this.onStatusChange?.('disconnected')
  }

  private sendAuth() {
    const key = `uid:${this.userId}`
    const body = buildAuthBody(this.userId, key, 'order_room', 'web', 'web-dashboard', [], 0)
    this.send(OP_AUTH, body)
  }

  private handleAuthReply(body: Uint8Array) {
    void body
    console.log('[GoimWS] auth success, starting heartbeat')
    this.onStatusChange?.('connected')
    this.startHeartbeat()
  }

  private handlePushMessage(body: Uint8Array) {
    const parsed = parsePushBody(body)
    if (parsed) {
      this.onMessage?.({
        type: 'push',
        data: parsed as unknown as Record<string, unknown>,
      })
      // Send ACK back
      this.sendAck(parsed.notify_id, 0)
    }
  }

  private sendAck(msgId: string, seq: number) {
    const enc = new TextEncoder()
    const ackData = JSON.stringify({ msg_id: msgId, seq })
    this.send(OP_PUSH_MSG_ACK, enc.encode(ackData))
  }

  private startHeartbeat() {
    this.stopHeartbeat()
    this.heartbeatTimer = setInterval(() => {
      this.lastHeartbeatSent = Date.now()
      this.send(OP_HEARTBEAT, new Uint8Array(0))
    }, config.wsHeartbeatInterval)
  }

  private stopHeartbeat() {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
  }

  private handleHeartbeatReply() {
    const latency = Date.now() - this.lastHeartbeatSent
    this.onMessage?.({ type: 'heartbeat', data: { latency_ms: latency } })
  }

  private send(op: number, body: Uint8Array) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
    this.seq++
    const buf = buildProto(op, body, this.seq)
    this.ws.send(buf)
  }

  private handleMessage(buf: ArrayBuffer) {
    try {
      const proto = parseProto(buf)
      switch (proto.op) {
        case OP_AUTH_REPLY:
          this.handleAuthReply(proto.body)
          break
        case OP_HEARTBEAT_REPLY:
          this.handleHeartbeatReply()
          break
        case OP_RAW:
          // Unwrap inner proto
          if (proto.body.length > 16) {
            const innerBuf = proto.body.buffer.slice(proto.body.byteOffset, proto.body.byteOffset + proto.body.byteLength) as ArrayBuffer
            const inner = parseProto(innerBuf)
            this.handlePushMessage(inner.body)
          }
          break
        case OP_SYNC_REPLY:
          this.onMessage?.({ type: 'sync', data: {} })
          break
        case OP_KICK_CONNECTION:
          this.onMessage?.({ type: 'kick', data: {} })
          break
        default:
          // Other op codes handled generically
          if (proto.body.length > 0) {
            try {
              const parsed = parsePushBody(proto.body)
              if (parsed) {
                this.onMessage?.({ type: 'push', data: parsed as unknown as Record<string, unknown> })
              }
            } catch { /* ignore parse errors */ }
          }
      }
    } catch {
      // Parse error — ignore corrupt frames
    }
  }

  private scheduleReconnect() {
    if (this.reconnectAttempts >= 20) return
    const delay = Math.min(
      config.wsReconnectBaseDelay * Math.pow(config.wsReconnectBackoffFactor, this.reconnectAttempts),
      config.wsReconnectMaxDelay
    )
    this.reconnectAttempts++

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      this.connect()
    }, delay)
  }

  getState(): ConnectionState {
    if (!this.ws) return 'disconnected'
    switch (this.ws.readyState) {
      case WebSocket.CONNECTING: return 'connecting'
      case WebSocket.OPEN: return 'connected'
      case WebSocket.CLOSING: return 'disconnected'
      default: return 'disconnected'
    }
  }
}
