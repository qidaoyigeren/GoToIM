const config = {
  // Mock mode — opt-in: set VITE_USE_MOCK=true in .env to enable demo
  useMock: import.meta.env.VITE_USE_MOCK === 'true',

  // Notify Server (business API)
  notifyBaseUrl: '/api',

  // Goim Logic (IM infrastructure API)
  logicBaseUrl: '/goim',

  // Comet WebSocket
  wsUrl: 'ws://localhost:3102/sub',

  // Default user for demo
  defaultUserId: '10001',
  defaultUserName: 'Demo User',

  // Polling intervals (ms)
  statsPollInterval: 5000,
  onlinePollInterval: 10000,

  // WebSocket settings
  wsHeartbeatInterval: 30000,
  wsReconnectBaseDelay: 1000,
  wsReconnectMaxDelay: 30000,
  wsReconnectBackoffFactor: 1.5,
} as const

const STORAGE_KEY = 'gotoim_demo_mode'

function getStoredDemoMode(): boolean | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v === null) return null
    return v === 'true'
  } catch {
    return null
  }
}

function setStoredDemoMode(on: boolean): void {
  try {
    localStorage.setItem(STORAGE_KEY, String(on))
  } catch { /* ignore */ }
}

// Effective demo mode: user choice (localStorage) > env var
export function isDemoMode(): boolean {
  const stored = getStoredDemoMode()
  if (stored !== null) return stored
  return config.useMock
}

// Toggle demo mode — persists to localStorage and reloads
export function toggleDemoMode(): void {
  setStoredDemoMode(!isDemoMode())
}

export default config
