import { useMemo, useState } from 'react'
import { createFlashSale } from '@/api/notify'
import { useChangeOrderStatus, useCreateOrder } from '@/hooks/useOrders'
import { useStartSimulation, useStopSimulation, useSimulationStatus } from '@/hooks/useSimulation'
import { useRealtimeStore } from '@/stores/realtimeStore'
import type { OrderStatus } from '@/types/order'
import {
  Activity,
  CheckCircle2,
  Megaphone,
  PackageCheck,
  Play,
  RadioTower,
  ShieldCheck,
  Square,
  UserRound,
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
    title: '购买订单链路',
    icon: PackageCheck,
    objective: '创建购买订单后依次触发商家确认、履约发出、送达签收，每一步都写入通知 outbox 并展示 GoIM 投递与 ACK。',
    metric: '端到端状态一致性',
    qps: 20,
    users: 1,
  },
  {
    id: 'peak',
    title: '峰值消息演练',
    icon: ShieldCheck,
    objective: '模拟大量订单消息并发进入 Logic，观察 direct push、Kafka fallback、离线补偿与 ACK 回执的变化。',
    metric: '高峰吞吐与可靠投递',
    qps: 500,
    users: 10000,
  },
  {
    id: 'campaign',
    title: '重点客户触达',
    icon: Megaphone,
    objective: '向指定用户发送活动类通知，用同一条可靠消息链路验证定向触达和客户端 ACK。',
    metric: '定向触达与回执',
    qps: 50,
    users: 1,
  },
]

const lifecycleSteps: OrderStatus[] = ['confirmed', 'shipped', 'delivered']

const stepLabel: Record<OrderStatus, string> = {
  created: '已下单',
  paid: '历史兼容状态',
  confirmed: '商家已确认',
  shipped: '履约已发出',
  delivered: '订单已送达',
  cancelled: '订单已取消',
  delivery_failed: '履约异常',
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
        setProgress('创建购买订单并写入通知 outbox')
        const created = await createOrder.mutateAsync({
          user_id: userId,
          merchant_id: 'm_apple_store',
          merchant_uid: 90001,
          order_type: 'urgent',
          importance: 'high',
          buyer_note: '实时链路演示订单，希望尽快履约',
          items: [{ product_id: 'p_iphone_case', quantity: 1 }],
        })

        addEvent({
          id: `scenario_create_${Date.now()}`,
          type: 'push_sent',
          msg_id: created.notification.notify_id,
          order_id: created.order.order_id,
          title: '购买订单已创建',
          detail: `订单 ${created.order.order_id} 已生成，买家通知与商家通知已进入投递链路`,
          timestamp: Date.now(),
          delivery_path: 'grpc_direct',
        })

        for (const status of lifecycleSteps) {
          setProgress(`推进状态：${stepLabel[status]}`)
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
            title: stepLabel[status],
            detail: `状态事件已通过 GoIM 推送给目标用户 ${userId}，等待客户端 ACK`,
            timestamp: Date.now(),
            delivery_path: 'grpc_direct',
          })
          await new Promise((resolve) => setTimeout(resolve, 450))
        }
        setProgress('订单消息链路完成，状态、通知、ACK 可在 trace 中核对')
      }

      if (selected === 'peak') {
        setProgress(`启动峰值演练：${activeScenario.qps}/s，${activeScenario.users.toLocaleString()} 用户池`)
        await startSimulation.mutateAsync({
          mode: 'peak',
          qps: activeScenario.qps,
          users: activeScenario.users,
        })
        await refetch()
        setProgress('峰值流量已启动，观察 direct push、fallback、ACK 和 DLQ 指标')
      }

      if (selected === 'campaign') {
        setProgress(`发送定向通知至 UID ${userId}`)
        await createFlashSale('会员服务提醒', '重点用户可查看新的履约权益与服务消息', [userId])
        addEvent({
          id: `scenario_campaign_${Date.now()}`,
          type: 'push_sent',
          msg_id: `campaign_${Date.now()}`,
          title: '定向通知已发送',
          detail: `UID ${userId} 的活动通知已进入 GoIM 投递链路`,
          timestamp: Date.now(),
          delivery_path: 'grpc_direct',
        })
        setProgress('定向通知已提交，等待客户端送达与 ACK')
      }
    } catch (error) {
      setProgress((error as Error)?.message || '执行失败，请检查后端服务状态')
    } finally {
      setIsRunning(false)
    }
  }

  const stopPeak = async () => {
    setIsRunning(true)
    try {
      await stopSimulation.mutateAsync()
      await refetch()
      setProgress('峰值演练已停止')
    } finally {
      setIsRunning(false)
    }
  }

  return (
    <section className="bg-white border border-gray-200 rounded-lg shadow-sm overflow-hidden">
      <div className="px-5 py-4 border-b border-gray-100 flex items-start justify-between gap-4">
        <div>
          <h2 className="text-base font-semibold text-gray-900">实时场景控制台</h2>
          <p className="text-xs text-gray-500 mt-1">用购买订单、峰值消息和定向通知验证 Logic、Router、Comet、离线、ACK、DLQ 的完整链路。</p>
        </div>
        <div className="hidden sm:flex items-center gap-2 text-xs text-gray-500">
          <Activity size={14} className="text-emerald-600" />
          {simStatus?.active ? `模拟运行中 ${simStatus.qps}/s` : '演示待命'}
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

              <div className="mt-4 grid grid-cols-1 sm:grid-cols-3 gap-3">
                <ValuePill icon={RadioTower} label="链路目标" value={activeScenario.metric} />
                <ValuePill icon={UserRound} label="目标用户" value={selected === 'peak' ? activeScenario.users.toLocaleString() : `UID ${userId}`} />
                <ValuePill icon={CheckCircle2} label="执行状态" value={progress} />
              </div>
            </div>

            <div className="space-y-3">
              <label className="block text-xs font-medium text-gray-500">
                目标用户 UID
                <input
                  value={userId}
                  onChange={(event) => setUserId(event.target.value)}
                  disabled={selected === 'peak' || isRunning}
                  className="mt-1 w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 disabled:bg-gray-50 disabled:text-gray-400"
                />
              </label>
              <button
                type="button"
                onClick={execute}
                disabled={isRunning || createOrder.isPending || changeStatus.isPending || startSimulation.isPending}
                className="w-full inline-flex items-center justify-center gap-2 rounded-lg bg-primary-600 px-4 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-primary-700 disabled:opacity-50"
              >
                <Play size={15} />
                执行场景
              </button>
              {simStatus?.active && (
                <button
                  type="button"
                  onClick={stopPeak}
                  disabled={isRunning || stopSimulation.isPending}
                  className="w-full inline-flex items-center justify-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-2.5 text-sm font-semibold text-red-700 hover:bg-red-100 disabled:opacity-50"
                >
                  <Square size={15} />
                  停止峰值演练
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
