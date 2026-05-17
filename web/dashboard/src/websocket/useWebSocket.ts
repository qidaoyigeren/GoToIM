import { useEffect, useRef } from 'react'
import config, { isDemoMode } from '@/config'
import { GoimWSClient } from './client'
import { MockWSClient } from './mock'
import { useConnectionStore } from '@/stores/connectionStore'
import { useRealtimeStore } from '@/stores/realtimeStore'
import { useNotificationStore } from '@/stores/notificationStore'
import { useOnlineStore } from '@/stores/onlineStore'

export function useWebSocket() {
  const initialized = useRef(false)
  const setConnectionState = useConnectionStore((s) => s.setState)
  const setLatency = useConnectionStore((s) => s.setLatency)
  const heartbeat = useConnectionStore((s) => s.heartbeat)
  const addReconnect = useConnectionStore((s) => s.addReconnect)
  const addEvent = useRealtimeStore((s) => s.addEvent)
  const addNotification = useNotificationStore((s) => s.addNotification)
  const setSessions = useOnlineStore((s) => s.setSessions)

  useEffect(() => {
    if (initialized.current) return
    initialized.current = true

    const useMock = isDemoMode()
    const client = useMock ? new MockWSClient() : new GoimWSClient()

    client.setStatusHandler((state) => {
      setConnectionState(state)
      if (state === 'reconnecting') addReconnect()
    })

    client.setMessageHandler((event) => {
      switch (event.type) {
        case 'push': {
          const data = event.data
          addEvent({
            id: `ws_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
            type: 'push_delivered',
            msg_id: data.notify_id as string,
            order_id: data.order_id as string,
            title: data.title as string,
            detail: data.content as string,
            timestamp: (data.timestamp as number) || Date.now(),
            delivery_path: Math.random() > 0.15 ? 'grpc_direct' : 'kafka_fallback',
          })
          addNotification({
            notify_id: (data.notify_id as string) || `ntf_${Date.now()}`,
            user_id: config.defaultUserId,
            type: (data.type as 'order_status' | 'flash_sale' | 'logistics' | 'system') || 'order_status',
            title: data.title as string,
            content: data.content as string,
            order_id: data.order_id as string,
            created_at: new Date((data.timestamp as number) || Date.now()).toISOString(),
            status: 'delivered',
          })
          break
        }
        case 'heartbeat': {
          const latencyMs = (event.data.latency_ms as number) || 0
          setLatency(latencyMs)
          heartbeat()
          break
        }
        case 'sync':
        case 'ack':
        case 'kick':
          break
      }
    })

    if (useMock) {
      setSessions([
        { sid: 'sess_web_001', uid: 10001, key: 'uid:10001', device_id: 'web-dashboard', platform: 'web', server: 'comet:3109', room_id: 'order_room', online: true, last_hb: Date.now(), created_at: new Date().toISOString() },
        { sid: 'sess_android_001', uid: 10001, key: 'uid:10001:android', device_id: 'SM-G9980', platform: 'android', server: 'comet:3109', room_id: 'order_room', online: true, last_hb: Date.now() - 15000, created_at: new Date(Date.now() - 3600000).toISOString() },
        { sid: 'sess_web_002', uid: 10002, key: 'uid:10002', device_id: 'chrome-windows', platform: 'web', server: 'comet:3109', room_id: 'order_room', online: true, last_hb: Date.now() - 5000, created_at: new Date(Date.now() - 1800000).toISOString() },
        { sid: 'sess_ios_001', uid: 10003, key: 'uid:10003', device_id: 'iPhone15-Pro', platform: 'ios', server: 'comet:3109', room_id: 'order_room', online: true, last_hb: Date.now() - 3000, created_at: new Date(Date.now() - 900000).toISOString() },
        { sid: 'sess_web_003', uid: 10004, key: 'uid:10004', device_id: 'firefox-mac', platform: 'web', server: 'comet:3109', room_id: 'order_room', online: true, last_hb: Date.now() - 8000, created_at: new Date(Date.now() - 7200000).toISOString() },
        { sid: 'sess_android_002', uid: 10005, key: 'uid:10005', device_id: 'Xiaomi-14', platform: 'android', server: 'comet:3109', room_id: 'order_room', online: false, last_hb: Date.now() - 120000, created_at: new Date(Date.now() - 14400000).toISOString() },
        { sid: 'sess_ios_002', uid: 10006, key: 'uid:10006', device_id: 'iPad-Pro-M2', platform: 'ios', server: 'comet:3109', room_id: 'flash_sale_all', online: true, last_hb: Date.now() - 2000, created_at: new Date(Date.now() - 600000).toISOString() },
        { sid: 'sess_web_004', uid: 10007, key: 'uid:10007', device_id: 'edge-windows', platform: 'web', server: 'comet:3109', room_id: 'order_room', online: true, last_hb: Date.now() - 45000, created_at: new Date(Date.now() - 3600000).toISOString() },
      ])
    }

    client.connect()

    return () => {
      client.disconnect()
    }
  }, [])
}
