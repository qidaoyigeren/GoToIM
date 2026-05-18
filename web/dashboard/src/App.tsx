import { Routes, Route } from 'react-router-dom'
import AppLayout from '@/components/layout/AppLayout'
import DashboardPage from '@/pages/DashboardPage'
import OrdersPage from '@/pages/OrdersPage'
import OrderDetailPage from '@/pages/OrderDetailPage'
import NotificationsPage from '@/pages/NotificationsPage'
import SessionsPage from '@/pages/SessionsPage'
import RealtimeWorkbenchPage from '@/pages/RealtimeWorkbenchPage'

export default function App() {
  return (
    <Routes>
      <Route element={<AppLayout />}>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/orders" element={<OrdersPage />} />
        <Route path="/orders/:id" element={<OrderDetailPage />} />
        <Route path="/notifications" element={<NotificationsPage />} />
        <Route path="/sessions" element={<SessionsPage />} />
        <Route path="/realtime" element={<RealtimeWorkbenchPage />} />
      </Route>
    </Routes>
  )
}
