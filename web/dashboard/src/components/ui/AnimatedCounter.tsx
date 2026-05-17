import { useEffect, useRef, useState } from 'react'

type Props = {
  value: number
  format?: 'number' | 'currency' | 'percent'
  duration?: number
}

export default function AnimatedCounter({ value, format = 'number', duration = 600 }: Props) {
  const [display, setDisplay] = useState(value)
  const prevValue = useRef(value)
  const frameRef = useRef<number>(0)

  useEffect(() => {
    const start = prevValue.current
    const diff = value - start
    if (diff === 0) return

    const startTime = performance.now()

    const animate = (now: number) => {
      const elapsed = now - startTime
      const progress = Math.min(elapsed / duration, 1)
      // ease-out cubic
      const eased = 1 - Math.pow(1 - progress, 3)
      setDisplay(start + diff * eased)

      if (progress < 1) {
        frameRef.current = requestAnimationFrame(animate)
      }
    }

    frameRef.current = requestAnimationFrame(animate)
    prevValue.current = value

    return () => cancelAnimationFrame(frameRef.current)
  }, [value, duration])

  const fmt = new Intl.NumberFormat('zh-CN', {
    style: format === 'currency' ? 'currency' : 'decimal',
    currency: 'CNY',
    maximumFractionDigits: format === 'percent' ? 1 : 0,
    minimumFractionDigits: 0,
  })

  if (format === 'percent') return <>{fmt.format(display * 100)}%</>
  return <>{fmt.format(display)}</>
}
