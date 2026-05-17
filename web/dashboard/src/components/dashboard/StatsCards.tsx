import { useRealtimeStore } from '@/stores/realtimeStore'
import { useOnlineStore } from '@/stores/onlineStore'
import StatCard from '@/components/ui/StatCard'
import { Zap, CheckCircle2, Users, Activity, Clock, Package } from 'lucide-react'

export default function StatsCards() {
  const stats = useRealtimeStore((s) => s.stats)
  const onlineStats = useOnlineStore((s) => s.stats)

  return (
    <div className="grid grid-cols-2 lg:grid-cols-3 xl:grid-cols-6 gap-4">
      <StatCard
        title="推送速率"
        value={`${stats?.push_rate_per_sec ?? 0}/s`}
        subtitle={`累计 ${(stats?.total_pushed ?? 0).toLocaleString()} 条`}
        icon={Zap}
        format="text"
      />
      <StatCard
        title="ACK 确认率"
        value={stats?.ack_rate ?? 0}
        icon={CheckCircle2}
        format="percent"
        trend={{ value: `P99 ${stats?.latency_p99_ms ?? 0}ms`, positive: (stats?.latency_p99_ms ?? 0) < 50 }}
      />
      <StatCard
        title="在线用户"
        value={onlineStats?.user_count ?? 0}
        subtitle={`${(onlineStats?.conn_count ?? 0).toLocaleString()} 个连接`}
        icon={Users}
      />
      <StatCard
        title="gRPC 直连"
        value={stats?.delivery_path?.grpc_direct ?? 0}
        icon={Activity}
        format="percent"
        subtitle={`Kafka ${((stats?.delivery_path?.kafka_fallback ?? 0) * 100).toFixed(0)}%`}
      />
      <StatCard
        title="离线待补推"
        value={onlineStats?.offline_pending ?? 0}
        icon={Clock}
        subtitle="等待用户上线"
      />
      <StatCard
        title="活跃连接"
        value={onlineStats?.conn_count ?? 0}
        icon={Package}
        subtitle={`${(onlineStats?.ip_count ?? 0)} 个 IP`}
      />
    </div>
  )
}
