import { useMemo, useState } from 'react'
import { createFlashSale } from '@/api/notify'
import { useChangeOrderStatus, useCreateOrder } from '@/hooks/useOrders'
import { useStartSimulation, useStopSimulation, useSimulationStatus } from '@/hooks/useSimulation'
import { useRealtimeStore } from '@/stores/realtimeStore'
import type { OrderStatus } from '@/types/order'
import {
  Activity,
  BadgeDollarSign,
  CheckCircle2,
  Megaphone,
  PackageCheck,
  Play,
  ShieldCheck,
  Square,
  Truck,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

type ScenarioId = 'lifecycle' | 'peak' | 'campaign'

const scenarios: Array<{
  id: ScenarioId
  title: string
  icon: LucideIcon
  objective: string
  metric: string
  qps: number
  users: number
}> = [
  {
    id: 'lifecycle',
    title: '订单履约闭环',
    icon: PackageCheck,
    objective: '创建订单后自动走完支付、确认、发货、签收，每一步都触发实时通知与 ACK。',
    metric: '端到端状态一致性',
    qps: 20,
    users: 1,
  },
  {
    id: 'peak',
    title: '大促峰值守护',
    icon: ShieldCheck,
    objective: '用后端模拟引擎制造高并发订单状态事件，观察 gRPC 直连、Kafka 回退与离线补推。',
    metric: '高峰吞吐与降级能力',
    qps: 500,
    users: 10000,
  },
  {
    id: 'campaign',
    title: '高价值客户触达',
    icon: Megaphone,
    objective: '向指定用户发送闪购权益，验证营销通知是否进入同一可靠通知管道。',
    metric: '定向触达与转化机会',
    qps: 50,
    users: 1,
  },
]

const lifecycleSteps: OrderStatus[] = ['paid', 'confirmed', 'shipped', 'delivered']
const product = { product_name: 'GoToIM Priority Fulfillment Pack', quantity: 1, price: 1299 }

const stepLabel: Record<OrderStatus, string> = {
  created: '已创建',
  paid: '已支付',
  confirmed: '商家确认',
  shipped: '已发货',
  delivered: '已签收',
  cancelled: '已取消',
  delivery_failed: '配送异常',
}

export default function OrderScenarioConsole() {
  const [selected, setSelected] = useState<ScenarioId>('lifecycle')
  const [userId, setUserId] = useState('10001')
  const [progress, setProgress] = useState('等待执行')
  const [isRunning, setIsRunning] = useState(false)
  const createOrder = useCreateOrder()
  const changeStatus = useChangeOrderStatus()
  const startSimulation = useStartSimulation()
  const stopSimulation = useStopSimulation()
  const { data: simStatus, refetch } = useSimulationStatus()
  const addEvent = useRealtimeStore((s) => s.addEvent)

  const activeScenario = useMemo(
    () => scenarios.find((item) => item.id === selected) ?? scenarios[0],
    [selected]
  )
  const ActiveIcon = activeScenario.icon

  const execute = async () => {
    setIsRunning(true)
    try {
      if (selected === 'lifecycle') {
        setProgress('创建订单并写入通知管道')
        const created = await createOrder.mutateAsync({
          items: [product],
          total: product.price,
          userId,
        })
        addEvent({
          id: `scenario_create_${Date.now()}`,
          type: 'push_sent',
          msg_id: created.notification.notify_id,
          order_id: created.order.order_id,
          title: '业务订单进入通知管道',
          detail: `订单 ${created.order.order_id} 已创建，开始验证履约闭环`,
          timestamp: Date.now(),
          delivery_path: 'grpc_direct',
        })

        for (const status of lifecycleSteps) {
          setProgress(`推进至：${stepLabel[status]}`)
          const result = await changeStatus.mutateAsync({
            orderId: created.order.order_id,
            newStatus: status,
            extra: status === 'shipped' ? { location: '华东履约中心' } : undefined,
          })
          addEvent({
            id: `scenario_status_${status}_${Date.now()}`,
            type: 'order_status_change',
            msg_id: result.notification.notify_id,
            order_id: created.order.order_id,
            title: `订单${stepLabel[status]}`,
            detail: `状态事件已进入 GoToIM 推送链路，目标用户 ${userId}`,
            timestamp: Date.now(),
            delivery_path: 'grpc_direct',
          })
          await new Promise((resolve) => setTimeout(resolve, 450))
        }
        setProgress('履约链路完成，订单状态与通知已闭环')
      }

      if (selected === 'peak') {
        setProgress(`启动峰值压测：${activeScenario.qps}/s，${activeScenario.users.toLocaleString()} 用户`)
        await startSimulation.mutateAsync({
          mode: 'peak',
          qps: activeScenario.qps,
          users: activeScenario.users,
        })
        await refetch()
        setProgress('峰值流量已启动，观察吞吐、ACK 与回退比例')
      }

      if (selected === 'campaign') {
        setProgress(`发送定向权益通知至 UID ${userId}`)
        await createFlashSale('优先履约权益已开放', '高价值客户可获得限时优先发货与专属客服提醒', [userId])
        addEvent({
          id: `scenario_campaign_${Date.now()}`,
          type: 'push_sent',
          msg_id: `campaign_${Date.now()}`,
          title: '定向营销通知已发送',
          detail: `UID ${userId} 收到闪购权益，验证营销消息复用可靠推送链路`,
          timestamp: Date.now(),
          delivery_path: 'grpc_direct',
        })
        setProgress('权益通知已提交，等待客户端送达与 ACK')
      }
    } catch (error) {
      setProgress((error as Error)?.message || '执行失败，请检查后端服务')
    } finally {
      setIsRunning(false)
    }
  }

  const stopPeak = async () => {
    setIsRunning(true)
    try {
      await stopSimulation.mutateAsync()
      await refetch()
      setProgress('峰值压测已停止')
    } finally {
      setIsRunning(false)
    }
  }

  return (
    <section className="bg-white border border-gray-200 rounded-lg shadow-sm overflow-hidden">
      <div className="px-5 py-4 border-b border-gray-100 flex items-start justify-between gap-4">
        <div>
          <h2 className="text-base font-semibold text-gray-900">业务场景控制台</h2>
          <p className="text-xs text-gray-500 mt-1">把订单履约、峰值流量和营销触达放进同一条可靠通知管道里验证。</p>
        </div>
        <div className="hidden sm:flex items-center gap-2 text-xs text-gray-500">
          <Activity size={14} className="text-emerald-600" />
          {simStatus?.active ? `模拟运行中 ${simStatus.qps}/s` : '链路待命'}
        </div>
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-[260px_minmax(0,1fr)]">
        <div className="border-b xl:border-b-0 xl:border-r border-gray-100 p-3 space-y-2">
          {scenarios.map(({ id, icon: Icon, title, metric }) => {
            const active = id === selected
            return (
              <button
                key={id}
                type="button"
                onClick={() => setSelected(id)}
                className={`w-full min-h-[68px] rounded-lg border px-3 py-2 text-left transition-colors ${
                  active
                    ? 'border-primary-300 bg-primary-50 text-primary-800'
                    : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-50'
                }`}
              >
                <span className="flex items-center gap-2 text-sm font-semibold">
                  <Icon size={16} />
                  {title}
                </span>
                <span className="mt-1 block text-xs text-gray-500">{metric}</span>
              </button>
            )
          })}
        </div>

        <div className="p-5">
          <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_220px] gap-5">
            <div>
              <div className="flex items-center gap-2">
                <ActiveIcon size={18} className="text-primary-600" />
                <h3 className="text-lg font-semibold text-gray-900">{activeScenario.title}</h3>
              </div>
              <p className="mt-2 text-sm leading-6 text-gray-600">{activeScenario.objective}</p>

              <div className="mt-4 grid grid-cols-3 gap-3">
                <ValuePill icon={BadgeDollarSign} label="业务收益" value={activeScenario.metric} />
                <ValuePill icon={Truck} label="目标用户" value={selected === 'peak' ? activeScenario.users.toLocaleString() : `UID ${userId}`} />
                <ValuePill icon={CheckCircle2} label="验证结果" value={progress} />
              </div>
            </div>

            <div className="space-y-3">
              <label className="block text-xs font-medium text-gray-500">
                目标用户 UID
                <input
                  value={userId}
                  onChange={(event) => setUserId(event.target.value)}
                  disabled={selected === 'peak' || isRunning}
                  className="mt-1 w-full rounded-xl border border-gray-100 bg-white px-3 py-2 text-sm text-gray-900 disabled:bg-gray-50 disabled:text-gray-400"
                />
              </label>
              <button
                type="button"
                onClick={execute}
                disabled={isRunning || createOrder.isPending || changeStatus.isPending || startSimulation.isPending}
                className="w-full inline-flex items-center justify-center gap-2 rounded-lg bg-primary-600 px-4 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-primary-700 disabled:opacity-50"
              >
                <Play size={15} />
                执行业务场景
              </button>
              {simStatus?.active && (
                <button
                  type="button"
                  onClick={stopPeak}
                  disabled={isRunning || stopSimulation.isPending}
                  className="w-full inline-flex items-center justify-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-2.5 text-sm font-semibold text-red-700 hover:bg-red-100 disabled:opacity-50"
                >
                  <Square size={15} />
                  停止峰值流量
                </button>
              )}
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}

function ValuePill({
  icon: Icon,
  label,
  value,
}: {
  icon: LucideIcon
  label: string
  value: string
}) {
  return (
    <div className="min-h-[74px] rounded-lg border border-gray-100 bg-gray-50 px-3 py-3">
      <div className="flex items-center gap-1.5 text-xs font-medium text-gray-500">
        <Icon size={13} />
        {label}
      </div>
      <div className="mt-2 text-sm font-semibold leading-5 text-gray-900">{value}</div>
    </div>
  )
}
