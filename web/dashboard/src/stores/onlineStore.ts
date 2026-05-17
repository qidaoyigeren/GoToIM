import { create } from 'zustand'
import type { OnlineStats, Session } from '@/types/online'

type OnlineStoreState = {
  stats: OnlineStats | null
  sessions: Session[]
  setStats: (s: OnlineStats) => void
  setSessions: (list: Session[]) => void
  updateSession: (sid: string, updates: Partial<Session>) => void
}

export const useOnlineStore = create<OnlineStoreState>((set) => ({
  stats: null,
  sessions: [],
  setStats: (stats) => set({ stats }),
  setSessions: (list) => set({ sessions: list }),
  updateSession: (sid, updates) =>
    set((s) => ({
      sessions: s.sessions.map((ses) => (ses.sid === sid ? { ...ses, ...updates } : ses)),
    })),
}))
