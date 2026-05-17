import { type LucideIcon } from 'lucide-react'
import AnimatedCounter from './AnimatedCounter'

type Props = {
  title: string
  value: number | string
  subtitle?: string
  icon: LucideIcon
  trend?: { value: string; positive: boolean }
  format?: 'number' | 'currency' | 'percent' | 'text'
  className?: string
}

export default function StatCard({ title, value, subtitle, icon: Icon, trend, format = 'number', className = '' }: Props) {
  const formattedValue = () => {
    if (typeof value === 'string') return value
    switch (format) {
      case 'currency': return `¥${value.toLocaleString()}`
      case 'percent': return `${(value * 100).toFixed(1)}%`
      case 'number': return value.toLocaleString()
      default: return String(value)
    }
  }

  return (
    <div className={`bg-white rounded-xl border border-gray-100 p-5 shadow-sm hover:shadow-md transition-shadow duration-200 ${className}`}>
      <div className="flex items-start justify-between">
        <div>
          <p className="text-xs font-medium text-gray-500 uppercase tracking-wider">{title}</p>
          <div className="mt-1.5 text-2xl font-bold text-gray-900 tracking-tight">
            {typeof value === 'number' ? <AnimatedCounter value={value} format={format} /> : formattedValue()}
          </div>
          {subtitle && <p className="mt-0.5 text-xs text-gray-400">{subtitle}</p>}
          {trend && (
            <p className={`mt-1.5 text-xs font-medium ${trend.positive ? 'text-emerald-600' : 'text-red-500'}`}>
              {trend.positive ? '↑' : '↓'} {trend.value}
            </p>
          )}
        </div>
        <div className="w-10 h-10 rounded-lg bg-primary-50 flex items-center justify-center">
          <Icon size={20} className="text-primary-600" />
        </div>
      </div>
    </div>
  )
}
