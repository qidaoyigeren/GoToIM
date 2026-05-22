import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import StatsCards from '@/components/dashboard/StatsCards'
import SLACards from '@/components/dashboard/SLACards'
import DLQRecoveryPanel from '@/components/dashboard/DLQRecoveryPanel'
import OrderStatusFunnel from '@/components/dashboard/OrderStatusFunnel'
import PushThroughputChart from '@/components/dashboard/PushThroughputChart'
import EventStream from '@/components/dashboard/EventStream'
import AlertCards from '@/components/dashboard/AlertCards'
import Skeleton, { SkeletonCard } from '@/components/ui/Skeleton'
import { CARD_LG } from '@/components/ui/cardStyles'
import { useOrders } from '@/hooks/useOrders'
import { useNotifications } from '@/hooks/useNotifications'
import { useOrderStore } from '@/stores/orderStore'
import { useOnlineStore } from '@/stores/onlineStore'
import { useRealtimeStore } from '@/stores/realtimeStore'
import { ArrowUpRight, CircleDollarSign, Clock3, ShieldCheck } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import type { Order } from '@/types/order'

const EMPTY_ORDERS: Order[] = []

export default function DashboardPage() {
  const ordersQuery = useOrders()
  const notificationsQuery = useNotifications()
  const orders = useOrderStore((s) => s.orders) ?? EMPTY_ORDERS
  const stats = useRealtimeStore((s) => s.stats)
  const onlineStats = useOnlineStore((s) => s.stats)
  const isInitialLoading = ordersQuery.isLoading || notificationsQuery.isLoading

  const impact = useMemo(() => {
    const totalGmv = orders.reduce((sum, order) => sum + (order.total || 0), 0)
    const activeOrders = orders.filter((order) => !['delivered', 'cancelled'].includes(order.status)).length
    const riskyOrders = orders.filter((order) => ['delivery_failed', 'cancelled'].includes(order.status)).length
    return {
      totalGmv,
      activeOrders,
      riskyOrders,
      reachableUsers: onlineStats?.user_count ?? stats?.online_users ?? 0,
    }
  }, [onlineStats?.user_count, orders, stats?.online_users])

  return (
    <div className="space-y-6 animate-fade-in">
      <div className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-primary-600">GoToIM Business Console</p>
          <h1 className="mt-1 text-2xl font-bold text-gray-950">实时订单状态与通知管道</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-gray-600">
            把“用户是否及时知道订单变化”变成可观测指标：订单价值、在线触达、ACK、回退和离线补偿都在同一张运营视图里。
          </p>
        </div>
        <Link
          to="/realtime"
          className="inline-flex items-center gap-2 rounded-lg bg-gray-900 px-4 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-gray-800"
        >
          打开业务工作台
          <ArrowUpRight size={15} />
        </Link>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <ImpactCard icon={CircleDollarSign} title="被管道覆盖的订单价值" value={`¥${impact.totalGmv.toLocaleString()}`} detail={`${orders.length} 个近期订单`} />
        <ImpactCard icon={Clock3} title="仍在履约中的订单" value={impact.activeOrders.toLocaleString()} detail="需要持续状态通知" />
        <ImpactCard icon={ShieldCheck} title="可实时触达用户" value={impact.reachableUsers.toLocaleString()} detail={`${impact.riskyOrders} 个异常订单需关注`} />
      </div>

      {isInitialLoading ? (
        <DashboardSkeleton />
      ) : (
        <>
          <StatsCards />
          <SLACards />

          <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
            <div className="xl:col-span-2 space-y-6">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                <OrderStatusFunnel />
                <PushThroughputChart />
              </div>
            </div>
            <div className="space-y-6">
              <AlertCards />
              <DLQRecoveryPanel />
            </div>
          </div>

          <EventStream />
        </>
      )}
    </div>
  )
}

function ImpactCard({
  icon: Icon,
  title,
  value,
  detail,
}: {
  icon: LucideIcon
  title: string
  value: string
  detail: string
}) {
  return (
    <section className={CARD_LG}>
      <div className="flex items-center justify-between gap-3">
        <div>
          <p className="text-xs font-medium text-gray-500">{title}</p>
          <p className="mt-2 text-2xl font-bold text-gray-950">{value}</p>
        </div>
        <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary-50 text-primary-600">
          <Icon size={19} />
        </div>
      </div>
      <p className="mt-3 text-xs text-gray-500">{detail}</p>
    </section>
  )
}

function DashboardSkeleton() {
  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-3 xl:grid-cols-6">
        {Array.from({ length: 6 }).map((_, index) => (
          <SkeletonCard key={index} />
        ))}
      </div>
      <div className="grid grid-cols-1 gap-6 xl:grid-cols-3">
        <div className="xl:col-span-2">
          <Skeleton className="h-[320px] rounded-xl border border-gray-100 bg-white shadow-sm" />
        </div>
        <div className="rounded-xl border border-gray-100 bg-white p-5 shadow-sm">
          <Skeleton className="mb-4 h-4 w-28" />
          <Skeleton lines={5} />
        </div>
      </div>
    </div>
  )
}
