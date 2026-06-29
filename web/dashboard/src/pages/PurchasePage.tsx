import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  Building2,
  CheckCircle2,
  MessageSquareText,
  Minus,
  PackageCheck,
  Plus,
  ReceiptText,
  Send,
  ShieldCheck,
  ShoppingBag,
  Store,
  UsersRound,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { createPurchaseOrder, listMerchantGroups, listMerchants, listProducts } from '@/api/notify'
import { useNotificationStore } from '@/stores/notificationStore'
import { useOrderStore } from '@/stores/orderStore'
import { useIdentityStore } from '@/stores/identityStore'
import type { Merchant, OrderImportance, OrderType, Product } from '@/types/order'
import { IMPORTANCE_LABELS, ORDER_TYPE_LABELS } from '@/types/order'
import type { PurchaseOrderResult } from '@/api/notify'

const orderTypes: OrderType[] = ['normal', 'presale', 'urgent', 'enterprise', 'after_sale', 'virtual']
const importances: OrderImportance[] = ['normal', 'high', 'urgent', 'critical']

const importancePolicy: Record<OrderImportance, { priority: string; ttl: string; ack: string; detail: string }> = {
  normal: { priority: 'normal', ttl: '3600s', ack: 'best_effort', detail: '常规消息，尽力送达即可。' },
  high: { priority: 'high', ttl: '1800s', ack: 'any_device', detail: '重要订单，任一设备 ACK 即满足。' },
  urgent: { priority: 'critical', ttl: '600s', ack: 'any_device', detail: '加急链路，缩短 TTL 并提高优先级。' },
  critical: { priority: 'critical', ttl: '300s', ack: 'primary_device', detail: '关键订单，要求主设备 ACK。' },
}

export default function PurchasePage() {
  const userId = useIdentityStore((s) => s.userId)
  const addOrder = useOrderStore((s) => s.addOrder)
  const addNotification = useNotificationStore((s) => s.addNotification)
  const merchantsQuery = useQuery({ queryKey: ['market', 'merchants'], queryFn: listMerchants })
  const merchants = merchantsQuery.data ?? []

  const [merchantId, setMerchantId] = useState('')
  const [productId, setProductId] = useState('')
  const [quantity, setQuantity] = useState(1)
  const [orderType, setOrderType] = useState<OrderType>('normal')
  const [importance, setImportance] = useState<OrderImportance>('high')
  const [buyerNote, setBuyerNote] = useState('请优先确认库存，并在聊天中同步处理进度。')
  const [submitting, setSubmitting] = useState(false)
  const [lastResult, setLastResult] = useState<PurchaseOrderResult | null>(null)

  useEffect(() => {
    if (!merchantId && merchants[0]) setMerchantId(merchants[0].merchant_id)
  }, [merchantId, merchants])

  const productsQuery = useQuery({
    queryKey: ['market', 'products', merchantId],
    queryFn: () => listProducts(merchantId),
    enabled: !!merchantId,
  })
  const groupsQuery = useQuery({
    queryKey: ['market', 'groups', merchantId],
    queryFn: () => listMerchantGroups(merchantId),
    enabled: !!merchantId,
  })
  const products = productsQuery.data ?? []

  useEffect(() => {
    if (products[0] && !products.some((item) => item.product_id === productId)) {
      setProductId(products[0].product_id)
    }
  }, [productId, products])

  const merchant = merchants.find((item) => item.merchant_id === merchantId)
  const product = products.find((item) => item.product_id === productId)
  const total = useMemo(() => (product ? product.price * quantity : 0), [product, quantity])
  const policy = importancePolicy[importance]

  const submit = async () => {
    if (!merchant || !product) return
    setSubmitting(true)
    try {
      const result = await createPurchaseOrder({
        user_id: String(userId),
        merchant_id: merchant.merchant_id,
        merchant_uid: merchant.merchant_uid,
        order_type: orderType,
        importance,
        buyer_note: buyerNote,
        fulfillment_mode: product.fulfillment_mode,
        items: [{ product_id: product.product_id, sku_id: product.sku_id, quantity }],
      })
      addOrder(result.order)
      result.notifications.forEach(addNotification)
      setLastResult(result)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="space-y-5 animate-fade-in">
      <header className="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-blue-600">Purchase</p>
          <h1 className="mt-1 text-2xl font-bold text-gray-950">购买页面</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-gray-600">
            选择商家和商品后直接创建购买订单，并同步生成买家通知、商家通知、订单私聊入口和商家群聊入口。
          </p>
        </div>
        <div className="grid grid-cols-3 gap-3">
          <Metric icon={ShoppingBag} label="订单类型" value={ORDER_TYPE_LABELS[orderType]} />
          <Metric icon={ShieldCheck} label="重要度" value={IMPORTANCE_LABELS[importance]} />
          <Metric icon={Send} label="投递策略" value={policy.priority} />
        </div>
      </header>

      <section className="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1fr)_390px]">
        <div className="space-y-5">
          <Panel title="商家列表" icon={Store}>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
              {merchants.map((item) => (
                <MerchantCard
                  key={item.merchant_id}
                  merchant={item}
                  selected={item.merchant_id === merchantId}
                  onSelect={() => setMerchantId(item.merchant_id)}
                />
              ))}
            </div>
          </Panel>

          <Panel title="商品卡片" icon={PackageCheck}>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2 2xl:grid-cols-3">
              {products.map((item, index) => (
                <ProductCard
                  key={item.product_id}
                  product={item}
                  index={index}
                  selected={item.product_id === productId}
                  onSelect={() => setProductId(item.product_id)}
                />
              ))}
              {!productsQuery.isLoading && products.length === 0 && (
                <div className="rounded-lg border border-dashed border-gray-200 px-4 py-8 text-center text-sm text-gray-400">
                  当前商家暂无可演示商品
                </div>
              )}
            </div>
          </Panel>

          {lastResult && <TriggeredMessages result={lastResult} />}
        </div>

        <aside className="space-y-4 xl:sticky xl:top-6 xl:self-start">
          <section className="rounded-lg border border-gray-100 bg-white p-5 shadow-sm">
            <div className="mb-4 flex items-center gap-2">
              <ReceiptText size={18} className="text-blue-600" />
              <h2 className="text-base font-semibold text-gray-950">订单确认区域</h2>
            </div>

            <div className="space-y-4">
              <div className="rounded-lg border border-gray-100 bg-gray-50 p-3">
                <div className="text-sm font-semibold text-gray-950">{product?.name || '请选择商品'}</div>
                <div className="mt-1 text-xs text-gray-500">{merchant?.name || '请选择商家'}</div>
                <div className="mt-3 flex items-center justify-between">
                  <span className="text-lg font-bold text-gray-950">¥{(product?.price ?? 0).toLocaleString()}</span>
                  <div className="flex items-center overflow-hidden rounded-lg border border-gray-200 bg-white">
                    <button type="button" onClick={() => setQuantity((value) => Math.max(1, value - 1))} className="p-2 text-gray-500 hover:bg-gray-50" title="减少数量">
                      <Minus size={14} />
                    </button>
                    <span className="w-11 text-center text-sm font-semibold text-gray-800">{quantity}</span>
                    <button type="button" onClick={() => setQuantity((value) => value + 1)} className="p-2 text-gray-500 hover:bg-gray-50" title="增加数量">
                      <Plus size={14} />
                    </button>
                  </div>
                </div>
              </div>

              <Field label="订单类型选择">
                <div className="grid grid-cols-2 gap-2">
                  {orderTypes.map((item) => (
                    <ChoiceButton key={item} active={orderType === item} onClick={() => setOrderType(item)}>
                      {ORDER_TYPE_LABELS[item]}
                    </ChoiceButton>
                  ))}
                </div>
              </Field>

              <Field label="重要度选择">
                <div className="grid grid-cols-4 gap-2">
                  {importances.map((item) => (
                    <ChoiceButton key={item} active={importance === item} onClick={() => setImportance(item)}>
                      {IMPORTANCE_LABELS[item]}
                    </ChoiceButton>
                  ))}
                </div>
              </Field>

              <Field label="买家备注">
                <textarea
                  value={buyerNote}
                  onChange={(event) => setBuyerNote(event.target.value)}
                  className="min-h-[86px] w-full resize-none rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
                />
              </Field>

              <div className="rounded-lg border border-blue-100 bg-blue-50 p-3 text-sm text-blue-950">
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <span>买家 UID：{userId}</span>
                  <span>商家 UID：{merchant?.merchant_uid ?? '-'}</span>
                  <span>priority：{policy.priority}</span>
                  <span>TTL：{policy.ttl}</span>
                  <span>ACK：{policy.ack}</span>
                  <span>合计：¥{total.toLocaleString()}</span>
                </div>
                <p className="mt-2 text-xs leading-5 text-blue-700">{policy.detail}</p>
              </div>

              <button
                type="button"
                onClick={() => void submit()}
                disabled={submitting || !merchant || !product}
                className="inline-flex w-full items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-gray-300"
              >
                <Send size={16} />
                {submitting ? '正在提交订单并推送消息' : '提交订单并推送消息'}
              </button>

              {groupsQuery.data?.[0] && (
                <Link
                  to={`/chat?room_id=${encodeURIComponent(groupsQuery.data[0].room_id)}`}
                  className="inline-flex w-full items-center justify-center gap-2 rounded-lg border border-gray-200 bg-white px-4 py-2.5 text-sm font-semibold text-gray-700 hover:bg-gray-50"
                >
                  <UsersRound size={16} />
                  打开商家群聊入口
                </Link>
              )}
            </div>
          </section>
        </aside>
      </section>
    </div>
  )
}

function Panel({ title, icon: Icon, children }: { title: string; icon: LucideIcon; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-gray-100 bg-white p-5 shadow-sm">
      <div className="mb-4 flex items-center gap-2">
        <Icon size={18} className="text-gray-700" />
        <h2 className="text-base font-semibold text-gray-950">{title}</h2>
      </div>
      {children}
    </section>
  )
}

function MerchantCard({ merchant, selected, onSelect }: { merchant: Merchant; selected: boolean; onSelect: () => void }) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`rounded-lg border p-4 text-left transition-colors ${
        selected ? 'border-blue-300 bg-blue-50' : 'border-gray-100 bg-white hover:border-gray-200 hover:bg-gray-50'
      }`}
    >
      <div className="flex items-center gap-3">
        <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-gray-900 text-white">
          <Building2 size={18} />
        </div>
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold text-gray-950">{merchant.name}</div>
          <div className="text-xs text-gray-400">UID {merchant.merchant_uid}</div>
        </div>
      </div>
      <p className="mt-3 line-clamp-2 text-xs leading-5 text-gray-500">{merchant.description}</p>
      <p className="mt-2 truncate text-xs text-blue-600">{merchant.group_name}</p>
    </button>
  )
}

function ProductCard({ product, selected, index, onSelect }: { product: Product; selected: boolean; index: number; onSelect: () => void }) {
  const visuals = [
    'from-sky-50 to-blue-100 text-blue-600',
    'from-emerald-50 to-teal-100 text-emerald-600',
    'from-amber-50 to-orange-100 text-amber-700',
  ][index % 3]
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`overflow-hidden rounded-lg border text-left transition-all ${
        selected ? 'border-blue-300 bg-blue-50 shadow-sm' : 'border-gray-100 bg-white hover:border-gray-200 hover:shadow-sm'
      }`}
    >
      <div className={`flex aspect-[5/2] items-center justify-center bg-gradient-to-br ${visuals}`}>
        <ShoppingBag size={38} />
      </div>
      <div className="p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h3 className="truncate text-sm font-semibold text-gray-950">{product.name}</h3>
            <p className="mt-1 line-clamp-2 text-xs leading-5 text-gray-500">{product.description}</p>
          </div>
          <span className="shrink-0 rounded-full bg-gray-100 px-2 py-0.5 text-[10px] text-gray-500">
            {product.fulfillment_mode || 'physical'}
          </span>
        </div>
        <div className="mt-4 flex items-center justify-between">
          <span className="text-lg font-bold text-gray-950">¥{product.price.toLocaleString()}</span>
          <span className="text-xs text-blue-600">{selected ? '已选择' : '选择商品'}</span>
        </div>
      </div>
    </button>
  )
}

function TriggeredMessages({ result }: { result: PurchaseOrderResult }) {
  const order = result.order
  const roomID = result.support_room?.room_id || order.support_room_id
  return (
    <section className="rounded-lg border border-emerald-100 bg-emerald-50 p-5 shadow-sm">
      <div className="mb-4 flex items-center gap-2">
        <CheckCircle2 size={18} className="text-emerald-700" />
        <h2 className="text-base font-semibold text-emerald-950">本次下单触发的消息</h2>
      </div>
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        {result.notifications.map((item) => (
          <div key={item.notify_id} className="rounded-lg border border-emerald-100 bg-white p-4">
            <div className="text-sm font-semibold text-gray-950">{item.event_type === 'created_merchant' ? '商家新订单通知' : '买家下单通知'}</div>
            <div className="mt-2 space-y-1 text-xs text-gray-500">
              <p>消息 ID：{item.notify_id}</p>
              <p>接收方：UID {item.user_id}</p>
              <p>方式：direct push / priority {item.priority}</p>
              <p>ACK：{item.ack_policy} / TTL {item.ttl_seconds}s</p>
            </div>
          </div>
        ))}
        <Link to={`/chat?order_id=${encodeURIComponent(order.order_id)}`} className="rounded-lg border border-emerald-100 bg-white p-4 hover:bg-gray-50">
          <div className="flex items-center gap-2 text-sm font-semibold text-gray-950">
            <MessageSquareText size={16} className="text-blue-600" />
            订单私聊入口
          </div>
          <p className="mt-2 text-xs leading-5 text-gray-500">
            conversation：{order.private_conversation_id || result.private_conversation?.conversation_id}
            <br />
            direct push：买家和商家一对一沟通订单。
          </p>
        </Link>
        {roomID && (
          <Link to={`/chat?room_id=${encodeURIComponent(roomID)}`} className="rounded-lg border border-emerald-100 bg-white p-4 hover:bg-gray-50">
            <div className="flex items-center gap-2 text-sm font-semibold text-gray-950">
              <UsersRound size={16} className="text-emerald-600" />
              商家群聊入口
            </div>
            <p className="mt-2 text-xs leading-5 text-gray-500">
              room：{roomID}
              <br />
              room push：加入商家群后接收群聊消息。
            </p>
          </Link>
        )}
      </div>
      <div className="mt-4 flex flex-wrap gap-2">
        <Link to={`/delivery?order_id=${encodeURIComponent(order.order_id)}`} className="rounded-lg bg-gray-900 px-3 py-2 text-xs font-semibold text-white hover:bg-gray-800">
          查看该订单投递状态
        </Link>
        <span className="rounded-lg bg-white px-3 py-2 text-xs text-emerald-700">订单 {order.order_id} 已创建</span>
      </div>
    </section>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-xs font-medium text-gray-500">{label}</span>
      {children}
    </label>
  )
}

function ChoiceButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-lg border px-3 py-2 text-xs font-medium transition-colors ${
        active ? 'border-blue-300 bg-blue-50 text-blue-700' : 'border-gray-200 bg-white text-gray-600 hover:bg-gray-50'
      }`}
    >
      {children}
    </button>
  )
}

function Metric({ icon: Icon, label, value }: { icon: LucideIcon; label: string; value: string }) {
  return (
    <div className="rounded-lg border border-gray-100 bg-white px-3 py-2 shadow-sm">
      <div className="flex items-center gap-1.5 text-[11px] text-gray-400">
        <Icon size={13} />
        {label}
      </div>
      <div className="mt-1 truncate text-xs font-semibold text-gray-800">{value}</div>
    </div>
  )
}
