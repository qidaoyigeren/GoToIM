# goim v3 Architecture Upgrade Prompt

## Context

当前项目是基于 [Terry-Mao/goim](https://github.com/Terry-Mao/goim) v2.0 的改进版本，已经补全了消息ACK、离线同步、多设备Session、双通道推送、失败重试等企业级特性。但其架构仍是原版的"管道式"设计：

```
Client → Comet(Gateway) → Logic(业务+路由混合) → Kafka(传输管道) → Job(消费投递) → Comet
```

本次升级目标：**将 MQ 从"传输工具"升级为"投递调度引擎"，引入独立的 Message Router 和 Delivery Worker，实现可调度、可观测、可恢复的投递体系。**

---

## Target Architecture

```
Producer (业务服务)
   │
   ▼
Message Router ─────────────── 核心新增层 ──────────────
   │   ├─ 路由决策（在线/离线/广播/房间）
   │   ├─ 反压控制（Token Bucket）
   │   ├─ 消息去重 & ID 生成
   │   └─ 协议转换（外部 Proto → 内部 Wire Format）
   │
   ▼
MQ Layer ───────────────── 投递调度引擎 ─────────────────
   │   ├─ 每用户独立队列（有序投递）
   │   ├─ 优先级队列（控制 > 业务）
   │   ├─ 延迟投递（内置重试，去掉 RetryWorker）
   │   ├─ 死信队列（DLQ，超限消息自动转入）
   │   ├─ 房间/广播队列（批量聚合投递）
   │   └─ 消息持久化 & 回放（上线时拉取未投递消息）
   │
   ▼
Delivery Worker ─────────── 投递执行层 ─────────────────
   │   ├─ 拉取 MQ 消息（per-user consumer group）
   │   ├─ gRPC Push → Gateway
   │   ├─ 投递状态回写（ACK → Router → MQ 确认消费）
   │   └─ 水平扩展（按 partition / consumer group）
   │
   ▼
Gateway ─────────────────── 薄接入层 ───────────────────
       ├─ 建连 / 断连 / 心跳维持
       ├─ 收发字节（TCP / WebSocket）
       ├─ 连接级限流（Token Bucket）
       └─ 零业务逻辑（所有路由决策上移到 Router）
```

---

## Key Changes from Current goim

### 1. Logic 拆分为 Message Router + Thin Backend

当前 `internal/logic/` 职责混杂：鉴权、Session管理、推送路由、负载均衡、节点发现、HTTP API。升级方案：

```
当前 Logic                          升级后
──────────────────────────        ──────────────────────
logic.go (大杂烩)          →       Message Router (路由决策)
conn.go (连接管理)          →       Gateway Auth Module
push.go (推送路由)          →       Router Dispatch Layer
balancer.go (负载均衡)      →       Router Load Strategy
nodes.go (节点发现)          →       保留，归入 Router
http/ (HTTP API)            →       Thin Backend API
service/session.go          →       保留，归入 Backend
service/ack.go              →       Router ACK Handler
service/sync.go             →       MQ 回放替代
service/retry.go            →       MQ Delayed Message 替代（删除）
service/push.go             →       Router Dispatch + Delivery Worker
```

### 2. MQ 角色升维

| 维度 | 当前 (v2) | 升级 (v3) |
|------|-----------|-----------|
| 定位 | 传输管道 | 投递调度引擎 |
| 队列模型 | 按消息类型分 topic | 按用户 + 优先级分队列 |
| 有序性 | 无保证 | 每用户队列天然有序 |
| 重试 | RetryWorker 定时扫 Redis ZSET | MQ Delayed Message |
| 死信 | 无 | DLQ 自动转入 |
| 回放 | Redis ZSET 离线队列 | MQ Consumer Group offset 回放 |
| 反压 | 无（Kafka堆积） | Router 入口感知投递状态 |

### 3. 新增 Message Router 内部结构

```
MessageRouter
├── DispatchEngine        // 路由决策核心
│   ├── RouteByUser()     // 在线 → Worker / 离线 → MQ Queue
│   ├── RouteByRoom()     // 房间 → MQ Room Queue（批量聚合）
│   └── RouteBroadcast()  // 广播 → MQ Broadcast Queue
├── RateLimiter           // 入口反压
│   ├── UserLevelLimit    // 每用户 QPS
│   └── GlobalLimit       // 全局 QPS
├── IDGenerator           // Snowflake 消息 ID
├── IdempotencyChecker    // 去重（Bloom Filter + Redis）
├── ProtocolConverter     // 外部 Proto ↔ 内部 Wire Format
└── ACKHandler            // 投递确认 → 更新 MQ offset
```

### 4. 新增 Delivery Worker 设计

```
DeliveryWorker（可水平扩展）
├── ConsumerGroup          // 对接 MQ Consumer Group
├── PushClient             // gRPC 连接池 → Gateway
├── ConcurrencyController  // 每个 Gateway 的并发控制
├── RetryPolicy            // 投递失败策略
│   ├── 瞬时失败 → 本地重试 3 次
│   ├── 持续失败 → 写入 MQ Delayed Queue
│   └── 超限 → 写入 DLQ
└── MetricsCollector       // 投递延迟、成功率、积压量
```

### 5. Gateway 精简

当前 `internal/comet/` 包含 `operation.go` 中的业务逻辑（ChangeRoom、Sub/Unsub、PushMsgAck 处理等），全部上移到 Router：

```
当前 Comet                          升级后 Gateway
──────────────────────────        ──────────────────────
server_tcp.go / server_ws.go  →   保留，协议接入
operation.go (业务逻辑)       →   删除，移至 Router
bucket.go / room.go           →   保留，连接管理
channel.go                    →   保留，每连接状态
ring.go                       →   保留，消息缓冲
priority_queue.go             →   保留，优先级发送
grpc/server.go                →   精简，仅保留 PushMsg/Kick
```

Gateway 仅处理 4 类 gRPC 请求：`PushMsg`、`KickConnection`、`HealthCheck`、`GetConnStats`

---

## Implementation Phases

### Phase 1: MQ 抽象层 + Consumer Group 改造

**不改架构，先改 MQ 使用方式**：
- 抽象 MQ 接口 `type MessageQueue interface { Enqueue() / Dequeue() / Ack() / DLQ() }`
- Job 的 Kafka Consumer 改为 Consumer Group 模式（已有基础）
- 用当前 Kafka 验证每用户 Partition 路由的可行性
- **目标**：当前功能不变，MQ 接口可插拔

### Phase 2: Message Router 独立

**从 Logic 中拆出 Router**：
- 新建 `internal/router/` 包
- DispatchEngine 接管 `logic/push.go` 的推送路由逻辑
- ACKHandler 接管 `logic/conn.go` 的 Receive/ACK 处理
- Logic 降级为 Thin Backend（仅 HTTP API + Session CRUD + 节点管理）
- **目标**：Router 独立部署，Logic 不参与消息路由

### Phase 3: Delivery Worker 独立

**从 Job 中拆出 Worker**：
- 新建 `internal/worker/` 包
- 替代当前 `internal/job/` 的 pushKeys/broadcast/broadcastRoom
- 增加投递状态回写和本地重试
- **目标**：Worker 可独立扩缩容，与 MQ 解耦

### Phase 4: MQ 能力深挖 + 简化

**MQ 承担更多职责后，删除冗余服务**：
- `service/retry.go` → 删除（MQ Delayed Message 替代）
- `service/sync.go` → 简化（MQ offset 回放替代 Redis ZSET）
- `service/ack.go` → 移至 Router（MQ Ack 机制替代手动状态追踪）
- **目标**：代码量减少 30%，投递链路更简洁

---

## MQ 选型建议

| 场景 | 推荐 | 原因 |
|------|------|------|
| 过渡期 | **保持 Kafka** | Consumer Group 已有，改动最小 |
| 中规模 | **NATS JetStream** | Consumer Group + Pull Mode + 每用户 Stream |
| 大规模 | **Redis Stream** | 每连接独立 Stream + Consumer Group + 低延迟 |
| 超大规模 | Kafka(分发) + Redis Stream(投递) | 两层 MQ，Kafka 做 Router→Worker，Redis 做 Worker→Gateway |

初期建议保持 Kafka，在 Phase 1 的 MQ 接口抽象下验证，后续按需替换。

---

## Success Metrics

| 指标 | 当前 | 目标 |
|------|------|------|
| 投递延迟 P99 | 依赖 Kafka 延迟 | < 100ms (直连路径) |
| 消息有序性 | 无保证 | 每用户队列有序 |
| 重试可见性 | RetryWorker 定时轮询 | MQ Delayed Message，实时可见 |
| 死信处理 | 无 | DLQ + 告警 |
| Gateway CPU | 包含业务逻辑 + 连接管理 | 仅连接管理，下降 40% |
| 代码复杂度 | Logic 单文件 254 行混杂 | Router + Backend 职责清晰 |

---

## Constraints

- **向后兼容**：Phase 1-2 期间，新旧路径双跑，通过 feature flag 切换
- **协议不变**：Client ↔ Gateway 的 TCP/WebSocket 协议保持不变
- **部署平滑**：新组件作为 sidecar 或独立 service 部署，不强制迁移
- **零数据丢失**：双写期间，新旧两条路径都成功才算投递成功

---

## Key Reference Files

```
当前项目:
  internal/logic/logic.go          - Logic 主入口（需拆分）
  internal/logic/push.go           - 推送路由逻辑（→ Router）
  internal/logic/conn.go           - 连接处理（→ Router ACK Handler）
  internal/logic/service/push.go   - 双通道推送（→ Router + Worker）
  internal/logic/service/retry.go  - 重试逻辑（→ MQ Delayed Message）
  internal/logic/service/ack.go    - ACK 追踪（→ MQ Ack）
  internal/logic/service/sync.go   - 离线同步（→ MQ 回放）
  internal/job/job.go              - Kafka 消费（→ Delivery Worker）
  internal/job/push.go             - pushKeys/broadcast（→ Worker）
  internal/comet/operation.go      - 业务逻辑（→ Router）

新增目录:
  internal/router/                 - Message Router（新增）
  internal/worker/                 - Delivery Worker（新增）
  internal/mq/                     - MQ 抽象层（新增）
  internal/gateway/                - 精简后的 Gateway（从 comet 演进）
```
