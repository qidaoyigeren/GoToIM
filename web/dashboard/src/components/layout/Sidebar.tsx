import { NavLink } from 'react-router-dom'
import {
  MessageCircle,
  MessagesSquare,
  ShoppingBag,
  Zap,
} from 'lucide-react'

const navItems = [
  { to: '/purchase', icon: ShoppingBag, label: '购买' },
  { to: '/chat', icon: MessagesSquare, label: '聊天' },
  { to: '/delivery', icon: MessageCircle, label: '投递状态' },
]

export default function Sidebar() {
  return (
    <aside className="fixed left-0 top-0 z-40 flex h-screen w-56 flex-col border-r border-gray-200 bg-white">
      <div className="flex h-14 items-center gap-2.5 border-b border-gray-100 px-5">
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-primary-500 to-primary-700">
          <Zap size={18} className="text-white" />
        </div>
        <span className="text-base font-semibold tracking-tight text-gray-900">GoIM Demo</span>
      </div>

      <nav className="flex-1 space-y-0.5 px-3 py-4">
        {navItems.map(({ to, icon: Icon, label }) => (
          <NavLink
            key={to}
            to={to}
            end
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
        <div className="text-xs leading-5 text-gray-400">
          订单消息传递演示
          <br />
          direct push / room push / ACK
        </div>
      </div>
    </aside>
  )
}
