import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar'
import TopBar from './TopBar'
import { useWebSocket } from '@/websocket/useWebSocket'

export default function AppLayout() {
  useWebSocket()

  return (
    <div className="min-h-screen bg-gray-50">
      <Sidebar />
      <div className="ml-56 flex flex-col min-h-screen">
        <TopBar />
        <main className="flex-1 p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
