import { NavLink } from 'react-router-dom'
import {
  Activity,
  Bell,
  LayoutDashboard,
  MessagesSquare,
  Package,
  Radio,
  Zap,
} from 'lucide-react'

const navItems = [
  { to: '/', icon: LayoutDashboard, label: '总览' },
  { to: '/orders', icon: Package, label: '订单' },
  { to: '/notifications', icon: Bell, label: '通知' },
  { to: '/chat', icon: MessagesSquare, label: '订单在线客服' },
  { to: '/sessions', icon: Radio, label: '在线会话' },
  { to: '/realtime', icon: Activity, label: '实时工作台' },
]

export default function Sidebar() {
  return (
    <aside className="fixed left-0 top-0 z-40 flex h-screen w-56 flex-col border-r border-gray-200 bg-white">
      <div className="flex h-14 items-center gap-2.5 border-b border-gray-100 px-5">
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-primary-500 to-primary-700">
          <Zap size={18} className="text-white" />
        </div>
        <span className="text-base font-semibold tracking-tight text-gray-900">GoToIM</span>
      </div>

      <nav className="flex-1 space-y-0.5 px-3 py-4">
        {navItems.map(({ to, icon: Icon, label }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              `flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium transition-all duration-150 ${
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

      <div className="border-t border-gray-100 px-4 py-3">
        <div className="text-xs text-gray-400">
          直连 IM + 通知投递
          <br />
          业务场景演示
        </div>
      </div>
    </aside>
  )
}
