import { create } from 'zustand'
import type { OnlineStats, Session } from '@/types/online'

type OnlineStoreState = {
  stats: OnlineStats | null
  sessions: Session[]
  setStats: (s: Partial<OnlineStats>) => void
  setSessions: (list: Session[]) => void
  updateSession: (sid: string, updates: Partial<Session>) => void
}

function normalizeStats(stats: Partial<OnlineStats>, sessions: Session[]): OnlineStats {
  const activeSessions = sessions.filter((session) => session.online)
  const activeUsers = new Set(activeSessions.map((session) => session.uid)).size

  return {
    ip_count: stats.ip_count ?? 0,
    conn_count: Math.max(stats.conn_count ?? 0, activeSessions.length),
    user_count: Math.max(stats.user_count ?? 0, activeUsers),
    offline_pending: stats.offline_pending ?? 0,
    direct_pushed: stats.direct_pushed ?? 0,
    kafka_fallback: stats.kafka_fallback ?? 0,
  }
}

export const useOnlineStore = create<OnlineStoreState>((set) => ({
  stats: null,
  sessions: [],
  setStats: (stats) =>
    set((state) => ({
      stats: normalizeStats(stats, state.sessions),
    })),
  setSessions: (list) =>
    set((state) => ({
      sessions: list,
      stats: state.stats ? normalizeStats(state.stats, list) : state.stats,
    })),
  updateSession: (sid, updates) =>
    set((state) => {
      const sessions = state.sessions.map((ses) => (ses.sid === sid ? { ...ses, ...updates } : ses))

      return {
        sessions,
        stats: state.stats ? normalizeStats(state.stats, sessions) : state.stats,
      }
    }),
}))
