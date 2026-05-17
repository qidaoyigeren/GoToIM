import config, { isDemoMode } from '@/config'

type RequestOptions = {
  method?: string
  body?: unknown
  params?: Record<string, string | number | string[] | number[]>
}

class ApiClient {
  private baseUrl: string

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl
  }

  async request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const { method = 'GET', body, params } = options

    let url = `${this.baseUrl}${path}`
    if (params) {
      const searchParams = new URLSearchParams()
      for (const [key, value] of Object.entries(params)) {
        if (Array.isArray(value)) {
          value.forEach((v) => searchParams.append(key, String(v)))
        } else {
          searchParams.set(key, String(value))
        }
      }
      const qs = searchParams.toString()
      if (qs) url += `?${qs}`
    }

    const fetchOptions: RequestInit = {
      method,
      headers: { 'Content-Type': 'application/json' },
    }
    if (body && method !== 'GET') {
      fetchOptions.body = JSON.stringify(body)
    }

    const response = await fetch(url, fetchOptions)
    if (!response.ok) {
      const errorText = await response.text().catch(() => '')
      throw new Error(`HTTP ${response.status}: ${errorText}`)
    }
    return response.json()
  }
}

export const notifyClient = new ApiClient(config.notifyBaseUrl)
export const logicClient = new ApiClient(config.logicBaseUrl)

export async function fetchPlatformStats() {
  if (isDemoMode()) {
    const { mockPlatformStats } = await import('./mock')
    return mockPlatformStats()
  }
  return notifyClient.request<{ code: number; data: import('@/types/message').PlatformStats }>('/platform/stats')
}

export async function fetchOnlineTotal() {
  if (isDemoMode()) {
    const { mockOnlineStats } = await import('./mock')
    return mockOnlineStats()
  }
  return logicClient.request<{ code: number; data: import('@/types/online').OnlineStats }>('/online/total')
}
