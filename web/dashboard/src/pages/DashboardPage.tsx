import { useEffect } from 'react'
import StatsCards from '@/components/dashboard/StatsCards'
import OrderStatusFunnel from '@/components/dashboard/OrderStatusFunnel'
import PushThroughputChart from '@/components/dashboard/PushThroughputChart'
import EventStream from '@/components/dashboard/EventStream'
import AlertCards from '@/components/dashboard/AlertCards'
import { useOrders } from '@/hooks/useOrders'
import { useNotifications } from '@/hooks/useNotifications'
import { useWebSocket } from '@/websocket/useWebSocket'

export default function DashboardPage() {
  useOrders()
  useNotifications()
  useWebSocket()

  return (
    <div className="space-y-6 animate-fade-in">
      <div>
        <h1 className="text-xl font-bold text-gray-900">实时订单状态推送仪表盘</h1>
        <p className="text-sm text-gray-500 mt-1">GoToIM — Real-time Order Status & Notification Pipeline</p>
      </div>

      <StatsCards />

      <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
        <div className="xl:col-span-2 space-y-6">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            <OrderStatusFunnel />
            <PushThroughputChart />
          </div>
        </div>
        <div className="space-y-6">
          <AlertCards />
        </div>
      </div>

      <EventStream />
    </div>
  )
}
