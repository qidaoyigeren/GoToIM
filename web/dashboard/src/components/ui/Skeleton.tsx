import { CARD_LG } from './cardStyles'

type Props = {
  className?: string
  lines?: number
}

export default function Skeleton({ className = '', lines = 1 }: Props) {
  if (lines > 1) {
    return (
      <div className={`space-y-3 ${className}`}>
        {Array.from({ length: lines }).map((_, i) => (
          <div
            key={i}
            className="skeleton h-4 rounded"
            style={{ width: `${100 - (i % 3) * 15}%` }}
          />
        ))}
      </div>
    )
  }

  return <div className={`skeleton ${className}`} />
}

export function SkeletonCard() {
  return (
    <div className={`${CARD_LG} space-y-3`}>
      <Skeleton className="h-3 w-20" />
      <Skeleton className="h-8 w-28" />
      <Skeleton className="h-3 w-16" />
    </div>
  )
}

export function SkeletonTable({ rows = 5, cols = 5 }: { rows?: number; cols?: number }) {
  return (
    <div className="space-y-2">
      {Array.from({ length: rows }).map((_, r) => (
        <div key={r} className="flex gap-4">
          {Array.from({ length: cols }).map((_, c) => (
            <Skeleton key={c} className="h-10 flex-1" />
          ))}
        </div>
      ))}
    </div>
  )
}
