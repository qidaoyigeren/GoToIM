import { create } from 'zustand'
import type { RealtimeEvent, PlatformStats } from '@/types/message'

type RealtimeStoreState = {
  events: RealtimeEvent[]
  stats: PlatformStats | null
  pushRateHistory: { time: string; rate: number }[]
  addEvent: (e: RealtimeEvent) => void
  clearEvents: () => void
  setStats: (s: PlatformStats) => void
  addPushRatePoint: (point: { time: string; rate: number }) => void
}

const MAX_EVENTS = 200
const MAX_RATE_POINTS = 60

export const useRealtimeStore = create<RealtimeStoreState>((set) => ({
  events: [],
  stats: null,
  pushRateHistory: [],
  addEvent: (e) =>
    set((s) => ({
      events: [e, ...s.events].slice(0, MAX_EVENTS),
    })),
  clearEvents: () => set({ events: [] }),
  setStats: (stats) => set({ stats }),
  addPushRatePoint: (point) =>
    set((s) => ({
      pushRateHistory: [...s.pushRateHistory, point].slice(-MAX_RATE_POINTS),
    })),
}))
