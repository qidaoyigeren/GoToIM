import { useQuery } from '@tanstack/react-query'
import { getPlatformStats } from '@/api/notify'
import { useRealtimeStore } from '@/stores/realtimeStore'
import config from '@/config'

export function usePlatformStats() {
  const setStats = useRealtimeStore((s) => s.setStats)
  const addPushRatePoint = useRealtimeStore((s) => s.addPushRatePoint)

  return useQuery({
    queryKey: ['platformStats'],
    queryFn: async () => {
      const stats = await getPlatformStats()
      setStats(stats)
      const now = new Date()
      const time = `${now.getHours().toString().padStart(2, '0')}:${now.getMinutes().toString().padStart(2, '0')}:${now.getSeconds().toString().padStart(2, '0')}`
      addPushRatePoint({ time, rate: stats.push_rate_per_sec })
      return stats
    },
    refetchInterval: config.statsPollInterval,
  })
}
