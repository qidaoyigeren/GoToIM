import { useQuery } from '@tanstack/react-query'
import { getOnlineTotal } from '@/api/logic'
import { useOnlineStore } from '@/stores/onlineStore'
import config from '@/config'

export function useOnlineStatus() {
  const setStats = useOnlineStore((s) => s.setStats)

  return useQuery({
    queryKey: ['onlineStatus'],
    queryFn: async () => {
      const stats = await getOnlineTotal()
      setStats(stats)
      return stats
    },
    refetchInterval: config.onlinePollInterval,
  })
}
