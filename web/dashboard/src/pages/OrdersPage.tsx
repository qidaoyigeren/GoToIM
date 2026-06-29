import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  CheckCircle2,
  Minus,
  PackagePlus,
  Plus,
  RadioTower,
  ReceiptText,
  Send,
  ShieldCheck,
  ShoppingBag,
  Store,
  Truck,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { listMerchants, listProducts } from '@/api/notify'
import OrderTable from '@/components/orders/OrderTable'
import { CARD_LG, CARD_SM } from '@/components/ui/cardStyles'
import { useCreateOrder, useOrders } from '@/hooks/useOrders'
import { useOrderStore } from '@/stores/orderStore'
import config from '@/config'
import type { OrderImportance, OrderType, Product } from '@/types/order'
import { IMPORTANCE_LABELS, ORDER_TYPE_LABELS } from '@/types/order'

const ORDER_TYPES: OrderType[] = ['normal', 'presale', 'urgent', 'enterprise', 'after_sale', 'virtual']
const IMPORTANCES: OrderImportance[] = ['normal', 'high', 'urgent', 'critical']

const POLICY_PREVIEW: Record<OrderImportance, { priority: string; ttl: string; ack: string; text: string }> = {
  normal: { priority: 'normal', ttl: '3600s', ack: 'best_effort', text: '低打扰投递，适合普通商品提醒。' },
  high: { priority: 'high', ttl: '1800s', ack: 'any_device', text: '要求任一设备 ACK，适合重要订单。' },
  urgent: { priority: 'critical', ttl: '600s', ack: 'any_device', text: '更高优先级和更短 TTL，适合加急订单。' },
  critical: { priority: 'critical', ttl: '300s', ack: 'primary_device', text: '要求主设备 ACK，适合关键订单。' },
}

const EXAMPLES: Array<{
  label: string
  merchant_id: string
  product_id: string
  order_type: OrderType
  importance: OrderImportance
  buyer_note: string
  quantity: number
}> = [
  {
    label: '普通商品',
    merchant_id: 'm_apple_store',
    product_id: 'p_iphone_case',
    order_type: 'normal',
    importance: 'normal',
    buyer_note: '正常配送即可',
    quantity: 1,
  },
  {
    label: '加急订单',
    merchant_id: 'm_service_care',
    product_id: 'p_onsite_repair',
    order_type: 'urgent',
    importance: 'urgent',
    buyer_note: '请优先安排，今天需要处理',
    quantity: 1,
  },
  {
    label: '企业采购',
    merchant_id: 'm_office_supply',
    product_id: 'p_laptop_bulk',
    order_type: 'enterprise',
    importance: 'critical',
    buyer_note: '采购部需要确认送达时间和发票信息',
    quantity: 2,
  },
]

export default function OrdersPage() {
  const navigate = useNavigate()
  const { isLoading } = useOrders()
  const orders = useOrderStore((s) => s.orders) ?? []
  const createOrder = useCreateOrder()
  const merchantsQuery = useQuery({ queryKey: ['market', 'merchants'], queryFn: listMerchants })

  const [userId, setUserId] = useState<string>(config.defaultUserId)
  const [merchantId, setMerchantId] = useState('')
  const [productId, setProductId] = useState('')
  const [quantity, setQuantity] = useState(1)
  const [orderType, setOrderType] = useState<OrderType>('normal')
  const [importance, setImportance] = useState<OrderImportance>('high')
  const [buyerNote, setBuyerNote] = useState('希望今天发货')

  const merchants = merchantsQuery.data ?? []
  useEffect(() => {
    if (!merchantId && merchants[0]) setMerchantId(merchants[0].merchant_id)
  }, [merchantId, merchants])

  const productsQuery = useQuery({
    queryKey: ['market', 'products', merchantId],
    queryFn: () => listProducts(merchantId),
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
  const policy = POLICY_PREVIEW[importance]

  const submit = () => {
    if (!merchant || !product) return
    createOrder.mutate(
      {
        user_id: userId,
        merchant_id: merchant.merchant_id,
        merchant_uid: merchant.merchant_uid,
        order_type: orderType,
        importance,
        buyer_note: buyerNote,
        fulfillment_mode: product.fulfillment_mode,
        items: [{ product_id: product.product_id, sku_id: product.sku_id, quantity }],
      },
      {
        onSuccess: (data) => navigate(`/orders/${data.order.order_id}`),
      }
    )
  }

  const applyExample = (example: (typeof EXAMPLES)[number]) => {
    setMerchantId(example.merchant_id)
    setProductId(example.product_id)
    setOrderType(example.order_type)
    setImportance(example.importance)
    setBuyerNote(example.buyer_note)
    setQuantity(example.quantity)
  }

  return (
    <div className="space-y-6 animate-fade-in">
      <div className="flex flex-col gap-3 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <h1 className="text-2xl font-bold text-gray-950">购买订单消息传递系统</h1>
          <p className="mt-1 text-sm text-gray-500">
            通用电商下单体验：选商品、确认订单、生成买家与商家 IM 通知。
          </p>
        </div>
        <div className="grid grid-cols-3 gap-3">
          <Signal icon={ShoppingBag} label="订单数" value={isLoading ? '加载中' : String(orders.length)} />
          <Signal icon={RadioTower} label="链路" value="direct / room" />
          <Signal icon={ShieldCheck} label="策略" value={`${policy.priority} / ${policy.ack}`} />
        </div>
      </div>

      <section className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_380px]">
        <div className="space-y-6">
          <div className={CARD_LG}>
            <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
              <div className="flex items-center gap-2">
                <Store size={18} className="text-blue-600" />
                <h2 className="text-base font-semibold text-gray-900">选择商家</h2>
              </div>
              <div className="flex flex-wrap gap-2">
                {EXAMPLES.map((example) => (
                  <button
                    key={example.label}
                    type="button"
                    onClick={() => applyExample(example)}
                    className="rounded-full border border-gray-200 bg-gray-50 px-3 py-1.5 text-xs text-gray-600 transition-colors hover:border-blue-200 hover:bg-blue-50 hover:text-blue-700"
                  >
                    示例：{example.label}
                  </button>
                ))}
              </div>
            </div>

            <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
              {merchants.map((item) => (
                <button
                  key={item.merchant_id}
                  type="button"
                  onClick={() => setMerchantId(item.merchant_id)}
                  className={`rounded-lg border p-4 text-left transition-colors ${
                    item.merchant_id === merchantId
                      ? 'border-blue-300 bg-blue-50'
                      : 'border-gray-100 bg-white hover:border-gray-200 hover:bg-gray-50'
                  }`}
                >
                  <div className="flex items-center gap-2">
                    <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-gray-900 text-white">
                      <Store size={16} />
                    </div>
                    <div className="min-w-0">
                      <div className="truncate text-sm font-semibold text-gray-900">{item.name}</div>
                      <div className="text-xs text-gray-400">UID {item.merchant_uid}</div>
                    </div>
                  </div>
                  <p className="mt-3 line-clamp-2 text-xs leading-5 text-gray-500">{item.description}</p>
                  <p className="mt-2 truncate text-xs text-blue-600">{item.group_name}</p>
                </button>
              ))}
            </div>
          </div>

          <div className={CARD_LG}>
            <div className="mb-5 flex items-center gap-2">
              <PackagePlus size={18} className="text-emerald-600" />
              <h2 className="text-base font-semibold text-gray-900">商品卡片</h2>
            </div>
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
              {products.map((item, index) => (
                <ProductCard
                  key={item.product_id}
                  product={item}
                  selected={item.product_id === productId}
                  index={index}
                  onSelect={() => setProductId(item.product_id)}
                />
              ))}
              {products.length === 0 && (
                <div className="rounded-lg border border-dashed border-gray-200 px-4 py-8 text-center text-sm text-gray-400">
                  该商家暂无示例商品
                </div>
              )}
            </div>
          </div>

          <div>
            <div className="mb-3 flex items-center justify-between">
              <div>
                <h2 className="text-base font-semibold text-gray-900">订单中心</h2>
                <p className="mt-1 text-xs text-gray-500">卡片式订单列表，点击查看消息 trace 和订单私聊。</p>
              </div>
            </div>
            <OrderTable />
          </div>
        </div>

        <aside className="space-y-4 xl:sticky xl:top-6 xl:self-start">
          <div className={CARD_LG}>
            <div className="mb-4 flex items-center gap-2">
              <ReceiptText size={18} className="text-blue-600" />
              <h2 className="text-base font-semibold text-gray-900">确认订单</h2>
            </div>

            <div className="space-y-4">
              <Field label="用户 UID">
                <input
                  value={userId}
                  onChange={(event) => setUserId(event.target.value)}
                  className="w-full rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
                />
              </Field>

              <div className="rounded-lg border border-gray-100 bg-gray-50 p-3">
                <div className="text-sm font-semibold text-gray-900">{product?.name || '未选择商品'}</div>
                <div className="mt-1 text-xs text-gray-500">{merchant?.name || '未选择商家'}</div>
                <div className="mt-3 flex items-center justify-between">
                  <span className="text-sm font-semibold text-gray-900">¥{(product?.price ?? 0).toLocaleString()}</span>
                  <div className="flex items-center overflow-hidden rounded-lg border border-gray-200 bg-white">
                    <button type="button" onClick={() => setQuantity((value) => Math.max(1, value - 1))} className="p-2 text-gray-500 hover:bg-gray-50">
                      <Minus size={14} />
                    </button>
                    <span className="w-10 text-center text-sm font-semibold text-gray-800">{quantity}</span>
                    <button type="button" onClick={() => setQuantity((value) => value + 1)} className="p-2 text-gray-500 hover:bg-gray-50">
                      <Plus size={14} />
                    </button>
                  </div>
                </div>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <Field label="订单形式">
                  <select
                    value={orderType}
                    onChange={(event) => setOrderType(event.target.value as OrderType)}
                    className="w-full rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
                  >
                    {ORDER_TYPES.map((item) => (
                      <option key={item} value={item}>{ORDER_TYPE_LABELS[item]}</option>
                    ))}
                  </select>
                </Field>
                <Field label="重要度">
                  <select
                    value={importance}
                    onChange={(event) => setImportance(event.target.value as OrderImportance)}
                    className="w-full rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
                  >
                    {IMPORTANCES.map((item) => (
                      <option key={item} value={item}>{IMPORTANCE_LABELS[item]}</option>
                    ))}
                  </select>
                </Field>
              </div>

              <Field label="买家备注">
                <textarea
                  value={buyerNote}
                  onChange={(event) => setBuyerNote(event.target.value)}
                  className="min-h-[76px] w-full resize-none rounded-lg border border-gray-200 px-3 py-2 text-sm outline-none focus:border-blue-400"
                />
              </Field>

              <div className="rounded-lg border border-blue-100 bg-blue-50 px-3 py-3 text-sm text-blue-900">
                <div className="flex items-center gap-2 font-semibold">
                  <RadioTower size={15} />
                  订单消息策略
                </div>
                <div className="mt-3 grid grid-cols-2 gap-2 text-xs">
                  <span>priority：{policy.priority}</span>
                  <span>TTL：{policy.ttl}</span>
                  <span>ACK：{policy.ack}</span>
                  <span>总价：¥{total.toLocaleString()}</span>
                </div>
                <p className="mt-2 text-xs leading-5 text-blue-700">{policy.text}</p>
              </div>

              <button
                type="button"
                onClick={submit}
                disabled={createOrder.isPending || !merchant || !product}
                className="inline-flex w-full items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-gray-300"
              >
                <Send size={16} />
                {createOrder.isPending ? '正在创建订单消息' : '提交订单并推送消息'}
              </button>
            </div>
          </div>

          <div className={CARD_SM}>
            <div className="flex items-center gap-2 text-sm font-semibold text-gray-800">
              <CheckCircle2 size={16} className="text-emerald-600" />
              下单后自动创建买家通知、商家通知、订单私聊和商家群聊入口。
            </div>
          </div>
        </aside>
      </section>
    </div>
  )
}

function ProductCard({
  product,
  selected,
  index,
  onSelect,
}: {
  product: Product
  selected: boolean
  index: number
  onSelect: () => void
}) {
  const visuals = productVisual(index)
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`overflow-hidden rounded-lg border text-left transition-all ${
        selected ? 'border-blue-300 bg-blue-50 shadow-sm' : 'border-gray-100 bg-white hover:border-gray-200 hover:shadow-sm'
      }`}
    >
      <div className={`flex aspect-[4/3] items-center justify-center ${visuals.bg}`}>
        <visuals.Icon size={44} className={visuals.fg} />
      </div>
      <div className="p-4">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h3 className="text-sm font-semibold text-gray-900">{product.name}</h3>
            <p className="mt-1 line-clamp-2 text-xs leading-5 text-gray-500">{product.description}</p>
          </div>
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-[10px] text-gray-500">{product.fulfillment_mode || 'physical'}</span>
        </div>
        <div className="mt-4 flex items-center justify-between">
          <span className="text-lg font-bold text-gray-950">¥{product.price.toLocaleString()}</span>
          <span className="text-xs text-blue-600">{selected ? '已选择' : '选择商品'}</span>
        </div>
      </div>
    </button>
  )
}

function productVisual(index: number): { bg: string; fg: string; Icon: LucideIcon } {
  const options = [
    { bg: 'bg-gradient-to-br from-sky-50 to-blue-100', fg: 'text-blue-600', Icon: ShoppingBag },
    { bg: 'bg-gradient-to-br from-emerald-50 to-teal-100', fg: 'text-emerald-600', Icon: Truck },
    { bg: 'bg-gradient-to-br from-amber-50 to-orange-100', fg: 'text-amber-700', Icon: PackagePlus },
    { bg: 'bg-gradient-to-br from-rose-50 to-red-100', fg: 'text-red-600', Icon: ShieldCheck },
  ]
  return options[index % options.length]
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-xs font-medium text-gray-500">{label}</span>
      {children}
    </label>
  )
}

function Signal({ icon: Icon, label, value }: { icon: LucideIcon; label: string; value: string }) {
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
