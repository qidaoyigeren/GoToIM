import { create } from 'zustand'

export type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'reconnecting'

type ConnectionStoreState = {
  state: ConnectionState
  latencyMs: number
  reconnectCount: number
  lastHeartbeat: number
  setState: (s: ConnectionState) => void
  setLatency: (ms: number) => void
  addReconnect: () => void
  heartbeat: () => void
}

export const useConnectionStore = create<ConnectionStoreState>((set) => ({
  state: 'disconnected',
  latencyMs: 0,
  reconnectCount: 0,
  lastHeartbeat: 0,
  setState: (s) => set({ state: s }),
  setLatency: (ms) => set({ latencyMs: ms }),
  addReconnect: () => set((s) => ({ reconnectCount: s.reconnectCount + 1 })),
  heartbeat: () => set({ lastHeartbeat: Date.now() }),
}))
