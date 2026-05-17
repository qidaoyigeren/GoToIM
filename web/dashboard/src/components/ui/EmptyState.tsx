import { type LucideIcon, PackageOpen } from 'lucide-react'

type Props = {
  icon?: LucideIcon
  title: string
  description?: string
}

export default function EmptyState({ icon: Icon = PackageOpen, title, description }: Props) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="w-16 h-16 rounded-2xl bg-gray-100 flex items-center justify-center mb-4">
        <Icon size={28} className="text-gray-400" />
      </div>
      <h3 className="text-base font-medium text-gray-600">{title}</h3>
      {description && <p className="mt-1 text-sm text-gray-400 max-w-xs">{description}</p>}
    </div>
  )
}
