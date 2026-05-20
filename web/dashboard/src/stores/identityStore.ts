import { create } from 'zustand'
import type { ChatRole } from '@/types/chat'

const STORAGE_KEY = 'gotoim_chat_identity'

type IdentityState = {
  role: ChatRole
  userId: number
  peerId: number
  setRole: (role: ChatRole) => void
}

function readRole(): ChatRole {
  try {
    const v = sessionStorage.getItem(STORAGE_KEY)
    return v === 'merchant' ? 'merchant' : 'customer'
  } catch {
    return 'customer'
  }
}

function idsForRole(role: ChatRole) {
  return role === 'merchant'
    ? { userId: 90001, peerId: 10001 }
    : { userId: 10001, peerId: 90001 }
}

const initialRole = readRole()

export const useIdentityStore = create<IdentityState>((set) => ({
  role: initialRole,
  ...idsForRole(initialRole),
  setRole: (role) => {
    try {
      sessionStorage.setItem(STORAGE_KEY, role)
    } catch {
      // sessionStorage may be disabled; the in-memory store still works.
    }
    set({ role, ...idsForRole(role) })
  },
}))
