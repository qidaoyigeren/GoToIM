// Mock WebSocket — simulates goim push events without a real backend
import { generateRealtimeEvent } from '@/api/mock'
import type { WSMessageHandler, WSStatusHandler } from './client'

type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'reconnecting'

export class MockWSClient {
  private timer: ReturnType<typeof setInterval> | null = null
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private onMessage: WSMessageHandler | null = null
  private onStatusChange: WSStatusHandler | null = null
  private intentionalClose = false

  setMessageHandler(handler: WSMessageHandler) { this.onMessage = handler }
  setStatusHandler(handler: WSStatusHandler) { this.onStatusChange = handler }

  connect() {
    this.intentionalClose = false
    this.onStatusChange?.('connecting')

    // Simulate connection delay
    setTimeout(() => {
      if (this.intentionalClose) return
      this.onStatusChange?.('connected')

      // Start emitting push events
      this.timer = setInterval(() => {
        if (Math.random() > 0.05) {
          const event = generateRealtimeEvent()
          if (event.type === 'ack_received') {
            this.onMessage?.({
              type: 'push',
              data: {
                type: 'order_status',
                title: event.title,
                content: event.detail,
                notify_id: `ntf_mock_${Date.now()}`,
                order_id: event.order_id,
                timestamp: event.timestamp,
              },
            })
          } else if (event.type === 'push_sent' || event.type === 'push_delivered') {
            this.onMessage?.({
              type: 'push',
              data: {
                type: 'order_status',
                title: event.title,
                content: event.detail,
                notify_id: `ntf_mock_${Date.now()}`,
                order_id: event.order_id,
                timestamp: event.timestamp,
              },
            })
          }
        }
      }, 2000)

      // Start heartbeat simulation
      this.heartbeatTimer = setInterval(() => {
        this.onMessage?.({ type: 'heartbeat', data: { latency_ms: Math.floor(Math.random() * 20) + 5 } })
      }, 10000)
    }, 500)
  }

  disconnect() {
    this.intentionalClose = true
    if (this.timer) { clearInterval(this.timer); this.timer = null }
    if (this.heartbeatTimer) { clearInterval(this.heartbeatTimer); this.heartbeatTimer = null }
    this.onStatusChange?.('disconnected')
  }

  getState(): ConnectionState {
    return this.timer ? 'connected' : 'disconnected'
  }
}
