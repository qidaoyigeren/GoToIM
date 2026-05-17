import { NavLink } from 'react-router-dom'
import {
  LayoutDashboard,
  Package,
  Bell,
  Radio,
  Activity,
  Zap,
} from 'lucide-react'

const navItems = [
  { to: '/', icon: LayoutDashboard, label: '总览' },
  { to: '/orders', icon: Package, label: '订单管理' },
  { to: '/notifications', icon: Bell, label: '通知中心' },
  { to: '/sessions', icon: Radio, label: '在线会话' },
  { to: '/realtime', icon: Activity, label: '实时演示' },
]

export default function Sidebar() {
  return (
    <aside className="fixed left-0 top-0 z-40 h-screen w-56 bg-white border-r border-gray-200 flex flex-col">
      <div className="flex items-center gap-2.5 h-14 px-5 border-b border-gray-100">
        <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-primary-500 to-primary-700 flex items-center justify-center">
          <Zap size={18} className="text-white" />
        </div>
        <span className="font-semibold text-base text-gray-900 tracking-tight">GoToIM</span>
      </div>

      <nav className="flex-1 px-3 py-4 space-y-0.5">
        {navItems.map(({ to, icon: Icon, label }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              `flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all duration-150 ${
                isActive
                  ? 'bg-primary-50 text-primary-700 shadow-sm'
                  : 'text-gray-600 hover:bg-gray-50 hover:text-gray-900'
              }`
            }
          >
            <Icon size={18} />
            {label}
          </NavLink>
        ))}
      </nav>

      <div className="px-4 py-3 border-t border-gray-100">
        <div className="text-xs text-gray-400">
          Real-time Order Status
          <br />
          &amp; Notification Pipeline
        </div>
      </div>
    </aside>
  )
}
