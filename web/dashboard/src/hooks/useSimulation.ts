import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { startSimulation, stopSimulation, getSimulationStatus } from '@/api/notify'

export function useSimulationStatus() {
  return useQuery({
    queryKey: ['simulationStatus'],
    queryFn: getSimulationStatus,
    refetchInterval: 5000,
  })
}

export function useStartSimulation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: { mode: string; qps: number; users: number }) =>
      startSimulation(params.mode, params.qps, params.users),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['simulationStatus'] })
    },
  })
}

export function useStopSimulation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: stopSimulation,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['simulationStatus'] })
    },
  })
}
