import { useState, useRef, useCallback } from 'react'
import { createOrder, changeOrderStatus, sendAck } from '@/api/notify'
import { useOrderStore } from '@/stores/orderStore'
import { useRealtimeStore } from '@/stores/realtimeStore'
import type { OrderStatus } from '@/types/order'
import { Send, Loader2, CheckCircle2 } from 'lucide-react'

const PRODUCTS = [
  { product_name: 'iPhone 16 Pro', price: 8999 },
  { product_name: 'AirPods Pro 2', price: 1899 },
  { product_name: 'MacBook Air M3', price: 8999 },
  { product_name: 'iPad Pro M4', price: 6799 },
  { product_name: 'Apple Watch Ultra 2', price: 6499 },
]

const STATUS_CHAIN: OrderStatus[] = ['paid', 'confirmed', 'shipped', 'delivered']

export default function BulkOrderCreator() {
  const [count, setCount] = useState(20)
  const [concurrency, setConcurrency] = useState(20)
  const [running, setRunning] = useState(false)
  const [progress, setProgress] = useState({ done: 0, total: 0, status: '' })
  const abortRef = useRef(false)

  const addOrder = useOrderStore((s) => s.addOrder)
  const updateOrderStatus = useOrderStore((s) => s.updateOrderStatus)
  const addEvent = useRealtimeStore((s) => s.addEvent)
  const userId = '10001'

  const run = useCallback(async () => {
    setRunning(true)
    abortRef.current = false
    setProgress({ done: 0, total: count, status: '创建订单中...' })

    const createdIds: string[] = []

    // Helper: run tasks with concurrency limit
    async function runWithConcurrency<T>(tasks: (() => Promise<T>)[], limit: number): Promise<T[]> {
      const results: T[] = []
      let idx = 0

      async function worker() {
        while (idx < tasks.length && !abortRef.current) {
          const i = idx++
          results[i] = await tasks[i]()
        }
      }

      await Promise.all(Array.from({ length: Math.min(limit, tasks.length) }, () => worker()))
      return results
    }

    // Phase 1: Create orders
    const createTasks = Array.from({ length: count }, (_, i) => async () => {
      const product = PRODUCTS[i % PRODUCTS.length]
      const qty = (i % 3) + 1
      const items = [{ product_name: product.product_name, quantity: qty, price: product.price }]
      const total = items.reduce((s, it) => s + it.price * it.quantity, 0)

      const result = await createOrder(userId, items, total)
      createdIds.push(result.order.order_id)
      addOrder(result.order)
      sendAck(result.notification.notify_id).catch(() => {})
      addEvent({
        id: `bulk_${Date.now()}_${i}`,
        type: 'push_sent',
        msg_id: result.notification.notify_id,
        order_id: result.order.order_id,
        title: `批量创建 #${i + 1}`,
        detail: `订单 ${result.order.order_id} 已创建并推送通知`,
        timestamp: Date.now(),
        delivery_path: 'grpc_direct',
      })
      setProgress((p) => ({ ...p, done: p.done + 1 }))
      return result
    })

    await runWithConcurrency(createTasks, concurrency)
    if (abortRef.current) { setRunning(false); return }

    // Phase 2: Advance all orders through paid→confirmed→shipped→delivered
    for (const status of STATUS_CHAIN) {
      if (abortRef.current) break
      setProgress({ done: 0, total: createdIds.length, status: `推进状态: ${status}...` })

      const statusTasks = createdIds.map((orderId) => async () => {
        const result = await changeOrderStatus(orderId, status)
        updateOrderStatus(orderId, status, new Date().toISOString())
        sendAck(result.notification.notify_id).catch(() => {})
        addEvent({
          id: `bulk_status_${Date.now()}_${orderId}`,
          type: 'order_status_change',
          msg_id: result.notification.notify_id,
          order_id: orderId,
          title: `批量状态变更`,
          detail: `订单 ${orderId} → ${status}`,
          timestamp: Date.now(),
          delivery_path: 'grpc_direct',
        })
        setProgress((p) => ({ ...p, done: p.done + 1 }))
      })

      await runWithConcurrency(statusTasks, concurrency)
    }

    setProgress((p) => ({ ...p, status: '完成！', done: p.total }))
    setRunning(false)
  }, [count, concurrency, addOrder, addEvent, updateOrderStatus])

  const stop = () => {
    abortRef.current = true
    setRunning(false)
  }

  return (
    <div className="bg-white rounded-xl border border-gray-100 p-5 shadow-sm">
      <h3 className="text-sm font-semibold text-gray-700 mb-3">批量订单压测</h3>
      <p className="text-xs text-gray-400 mb-4">
        并发调用后端 API 创建 N 个订单并走完完整状态机，产生真实推送流量
      </p>

      <div className="space-y-3">
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="text-xs font-medium text-gray-500">订单数量</label>
            <input
              type="number" min={1} max={500} value={count}
              onChange={(e) => setCount(Number(e.target.value))}
              disabled={running}
              className="mt-1 w-full text-xs border border-gray-200 rounded-lg px-3 py-2 bg-white disabled:opacity-50"
            />
          </div>
          <div>
            <label className="text-xs font-medium text-gray-500">并发数</label>
            <input
              type="number" min={1} max={50} value={concurrency}
              onChange={(e) => setConcurrency(Number(e.target.value))}
              disabled={running}
              className="mt-1 w-full text-xs border border-gray-200 rounded-lg px-3 py-2 bg-white disabled:opacity-50"
            />
          </div>
        </div>

        {running && (
          <div className="space-y-1.5">
            <div className="flex justify-between text-xs text-gray-500">
              <span>{progress.status}</span>
              <span>{progress.done}/{progress.total}</span>
            </div>
            <div className="h-1.5 bg-gray-100 rounded-full overflow-hidden">
              <div
                className="h-full bg-primary-500 rounded-full transition-all duration-300"
                style={{ width: `${progress.total > 0 ? (progress.done / progress.total) * 100 : 0}%` }}
              />
            </div>
          </div>
        )}

        {!running && progress.status === '完成！' && (
          <div className="flex items-center gap-2 text-xs text-green-600 font-medium">
            <CheckCircle2 size={14} />
            {count} 个订单 × 5 次状态变更 = {count * 5} 条推送消息已发送
          </div>
        )}

        <button
          onClick={running ? stop : run}
          className={`w-full inline-flex items-center justify-center gap-2 px-4 py-2.5 text-sm font-medium rounded-lg transition-colors ${
            running
              ? 'bg-red-50 text-red-600 border border-red-200 hover:bg-red-100'
              : 'bg-primary-600 text-white hover:bg-primary-700 shadow-sm'
          }`}
        >
          {running ? (
            <><Loader2 size={14} className="animate-spin" /> 停止</>
          ) : (
            <><Send size={14} /> 发起 {count} 个订单压测</>
          )}
        </button>
      </div>
    </div>
  )
}
