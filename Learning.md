# GoToIM 项目深度学习笔记

---

# 第一阶段：项目全局认知

---

## 1. 三句话项目介绍

**面试场景：请用三句话介绍你的项目。**

> GoToIM 是一个基于 goim 二次开发的高性能分布式 IM + 实时通知中台。核心解决长连接断开导致直推消息丢失的问题，通过**双通道自适应投递**（gRPC 实时直推 → 失败自动降级 Redis ZSET 离线队列 + Kafka 可靠投递）保证消息最终送达。技术上实现了 Comet/Logic/Router/Worker 四层架构，支持百万级并发连接，P99 推送延迟 < 10ms，消息到达率 100%。

---

## 2. 一分钟面试版

**面试场景：给你一分钟介绍你的项目。**

先说下项目定位。GoToIM 不是一个通用 IM 工具，它是一个**实时通知中台**——专门解决电商场景下订单状态变更、秒杀通知、物流轨迹这类"必须送达"的消息投递问题。

架构上分成四层：

- **Comet 接入层**：管理 TCP/WebSocket 长连接，用 CityHash 把连接打散到 32 个 Bucket 减少锁竞争，每连接独立读写协程，Ring Buffer 做无锁消息中转
- **Logic 业务层**：负责认证、Session 管理、节点发现、负载均衡，对外暴露 HTTP/gRPC 双协议
- **Router 路由层**：这是我自己写的核心模块，实现了双通道投递——先尝试 gRPC 直推，失败了自动降级到 Redis 离线队列加 Kafka 可靠投递，保证消息不丢
- **Worker 消费层**：从 Kafka 消费消息，通过 gRPC 连接池推到 Comet，房间消息做了批处理加 WAL 防崩溃丢失

核心技术栈是 Go + gRPC + Kafka + Redis + TCP/WebSocket。项目最大的亮点是**双通道消息自适应投递**——不是简单的"直推失败丢给 MQ"，而是围绕幂等、ACK、离线队列和补偿机制形成了一条可靠投递闭环。

---

## 2.1 面试口径修正：复杂度收敛版

如果面试官追问“四维限流、完整状态机、三次 ACK 会不会过重”，推荐这样回答：

> 这个问题我复盘过。我的设计原则不是把所有复杂能力都塞进主链路，而是主链路保持克制，扩展能力按场景开启。默认主链路只保留 `accepted -> pushed -> acked / timeout / failed` 这几个关键状态，能说明消息是否被系统接管、是否推到接入层、是否被客户端最终确认。更细的 `routed/direct_failed/fallback_queued/offline_stored` 这些状态主要用于 trace 排障，不会让每条消息都走强状态机约束。
>
> 限流也是一样，默认只启用用户维度和优先级维度。`business_type`、`event_type` 更适合作为观测标签和灰度开关，只有当监控证明某类业务真的冲击系统时，再打开对应维度的强限流。这样既保留了扩展性，也避免主流程被复杂策略拖重。

一句话总结：

> **主链路做可靠闭环，复杂维度做可观测和灰度扩展。**

---

## 3. 五分钟深度版（面试官追问"展开说说"）

GoToIM 本质上是一个**消息投递中台**。它的核心命题是：在长连接不稳定的网络环境下，如何保证业务通知 100% 到达？

围绕这个命题，我做了四层架构设计：

**第一层：Comet 接入层（连接管理）**

这是直接面对客户端的一层。核心挑战是**百万级并发连接的管理**。

- TCP/WebSocket 双协议支持，同一套业务逻辑
- CityHash 将连接 key 哈希后打散到 32 个 Bucket，每个 Bucket 内部是一把读写锁——锁粒度从"全局锁"降到"32 分之一"
- 每连接独立读写协程（Reader + Writer goroutine），Reader 读 TCP 帧放入 Ring Buffer，Dispatcher 从 Ring Buffer 取出写入 TCP——生产者-消费者模型，读写解耦
- Ring Buffer 是 SPSC（单生产者单消费者）无锁设计，原子操作保证可见性
- PriorityQueue 分两级：High（控制消息：心跳回复、ACK、踢人）和 Normal（业务推送），保证控制消息永远不被业务消息阻塞
- Round Buffer Pool：Reader/Writer/Timer 三类资源都按 round-robin 池化，预分配，避免 GC 压力

**第二层：Logic 业务层（业务编排）**

这是整个系统的"大脑"。

- 对外暴露 HTTP（Gin）+ gRPC 双协议，HTTP 给业务方调用，gRPC 给内部服务调用
- Session 管理用了五维 Redis 索引：sid（会话 ID）、uid（用户 ID）、device（设备 ID）、key（连接 key）、server（Comet 节点）。每个维度都能 O(1) 查询，满足不同场景的查询需求
- 负载均衡基于加权随机 + 区域亲和，优先同机房 Comet
- 节点发现基于 bilibili/discovery 注册中心

**第三层：Router 路由引擎（核心亮点）**

这是整个项目最有技术含量的模块。核心是**双通道自适应投递**：

- 快速通道：查询用户在线状态 → gRPC 直推 Comet → 客户端，延迟 < 50ms
- 可靠通道：gRPC 失败或用户离线 → Redis ZSET 离线队列 + Kafka 异步投递

关键设计决策：
1. 两条通道不是"二选一"，而是"先快后慢"——在线用户走快通道，失败自动降级
2. 部分成功场景：用户有 3 个设备（手机+平板+PC），其中 2 个在线 1 个离线——在线设备已收到，离线设备进 Kafka 等待补充投递
3. 消息去重用了两层：内存 64-shard FNV hash（5 分钟 TTL，O(1)，零 I/O）+ Redis HSETNX（7 天 TTL，持久化）
4. ACK 投递闭环：Pending → Delivered → Acked/Failed，核心状态持久化；更细状态用于 trace 排障
5. 离线队列用 Redis ZSET，score 用用户级递增序列号，支持范围查询和分页拉取

**第四层：Worker 消费层（异步投递引擎）**

从 Kafka 消费消息并投递到 Comet。

- CometClientPool 用了 fan-out 模式：每个 Comet 服务器 32 个 goroutine + 32 个 buffered channel，通过 atomic 计数器 round-robin 分发
- Room 消息做了批处理：积攒 20 条或 1 秒超时后批量发送，减少 gRPC 调用次数
- WAL（写前日志）：房间消息批次在 gRPC 发送前先写本地 WAL 文件，发送成功后截断。Crash 后重启会重放 WAL——保证不丢批次

---

## 4. 项目整体架构图

```
                    ┌──────────────────────┐
                    │   外部业务系统         │
                    │  (电商/物流/营销)      │
                    └──────────┬───────────┘
                               │ HTTP
                               ▼
                    ┌──────────────────────┐
                    │   Notify Server       │  ← 业务网关层（自定义）
                    │   订单/秒杀/物流      │
                    │   :3121 (Gin HTTP)    │
                    └──────────┬───────────┘
                               │ HTTP POST /goim/push/mids
                               ▼
     ┌─────────────────────────────────────────────┐
     │              Logic 业务层                     │
     │  ┌─────────────────────────────────────┐    │
     │  │ HTTP Server :3111  │ gRPC :3119     │    │
     │  ├─────────────────────────────────────┤    │
     │  │ Session 管理 │ 节点发现 │ 负载均衡   │    │
     │  │ Push API     │ Online  │ Sync       │    │
     │  ├─────────────────────────────────────┤    │
     │  │         Router 路由引擎 ★            │    │
     │  │  双通道投递 │ 幂等去重 │ ACK状态机  │    │
     │  └──────────┬──────────┬───────────────┘    │
     └─────────────┼──────────┼────────────────────┘
                   │          │
          ┌────────▼──┐  ┌───▼──────────┐
          │   Redis   │  │    Kafka      │
          │  Session  │  │  3个Topic     │
          │  Offline  │  │  Push/Room/   │
          │  Status   │  │  Broadcast    │
          └───────────┘  └───┬──────────┘
                              │ Consumer Group
                       ┌──────▼──────────┐
                       │  Worker 消费层   │
                       │  CometClientPool │
                       │  RoomAggregator  │
                       └──────┬──────────┘
                              │ gRPC Push
     ┌────────────────────────▼─────────────────────┐
     │              Comet 接入层                      │
     │  ┌─────────────────────────────────────────┐  │
     │  │ TCP :3101  │  WebSocket :3102           │  │
     │  │ gRPC Server :3109                       │  │
     │  ├─────────────────────────────────────────┤  │
     │  │ CityHash 32-way Bucket 分片              │  │
     │  │ Ring Buffer (SPSC Lock-free)            │  │
     │  │ PriorityQueue (High/Normal 两级)        │  │
     │  │ Round Buffer Pool (Reader/Writer/Timer) │  │
     │  └─────────────────────────────────────────┘  │
     └───────────────────────────────────────────────┘
                               │
                    ┌──────────▼───────────┐
                    │     客户端            │
                    │  iOS/Android/Web      │
                    │  TCP/WebSocket 长连接  │
                    └──────────────────────┘
```

**各层端口与服务发现：**

| 服务 | TCP | WebSocket | HTTP | gRPC | Discovery |
|------|-----|-----------|------|------|-----------|
| Comet | :3101 | :3102 | — | :3109 | 注册到 :7171 |
| Logic | — | — | :3111 | :3119 | 注册到 :7171 |
| Worker | — | — | — | — | 发现 Comet |
| Notify | — | — | :3121 | — | — |

---

## 5. 消息生命周期（从业务事件到客户端 ACK）

```
时间线 ──────────────────────────────────────────────────────────▶

[1] 业务事件产生
    用户下单 → 支付成功 → Notify Server 收到回调
    ↓
[2] 调用 Logic Push API
    POST /goim/push/mids  {"to_uid":12345, "text":"订单已支付"}
    ↓
[3] Logic.pushMids() → Router.RouteByUser()
    ├─ [3a] Snowflake 生成全局唯一 msgID (如 "1734567890123456789")
    ├─ [3b] HSETNX msg:{msgID} status pending  ← 原子抢占，去重
    │       └─ 已存在? → 直接返回 (其他节点已处理)
    ├─ [3c] IsOnline(uid) → Redis HGETALL user_sessions:{uid}
    │       ├─ 在线 → [3d] 走快速通道
    │       └─ 离线 → [3e] 走可靠通道
    ↓
[3d] 快速通道 (directPush)
     for each session:
       gRPC PushMsg(server, [key], op, body) → Comet
       500ms timeout per session
     ├─ 全部成功 → MarkDelivered(msgID), directTotal++
     ├─ 部分成功 → MarkDelivered + 失败 session 走可靠通道
     └─ 全部失败 → 走可靠通道
    ↓
[3e] 可靠通道 (reliableEnqueue)
     ├─ ZADD offline:{uid} {seq} {msgID}     ← Redis ZSet 离线队列
     └─ Kafka pushTopic (partition=uid)      ← Worker 异步消费
    ↓
[4] Comet 收到 gRPC PushMsg
     bucket = CityHash32(key) % 32           ← 分桶定位
     channel = bucket.chs[key]               ← 精确查找
     channel.Push(proto)                     → PriorityQueue (非阻塞)
     channel.Signal()                        → ProtoReady 唤醒写协程
    ↓
[5] 写协程 dispatchTCP
     p.WriteTCP(wr)                          ← 序列化二进制协议
     wr.Flush()                              ← 刷到内核 TCP buffer
    ↓
[6] 客户端接收
     TCP 读取 → 解析 Proto → 展示通知
     发送 ACK: OpPushMsgAck (seq, msgID)
    ↓
[7] Comet 转发 ACK
     Comet.Operate → Logic.Receive → ACKHandler.HandleACK
     ├─ HSET msg:{msgID} status acked
     ├─ ZREM offline:{uid} {msgID}           ← 清理离线队列
     ├─ Kafka ACK Topic                      ← 发布 ACK 事件
     └─ idCache.MarkSeen(msgID)              ← 写入内存幂等缓存
    ↓
[8] 生命周期结束
     msg:{msgID} 7天后 TTL 过期自动清理
```

**关键时间节点：**
- 业务事件 → Logic Router 决策：< 1ms
- gRPC 直推（在线）：< 50ms
- Kafka 异步投递（离线）：100ms ~ 2s
- 客户端 ACK → 离线队列清理：< 10ms

---

## 6. 为什么是"通知中台"而不是"通用 IM"

这是面试中最关键的项目定位问题。通用 IM 和通知中台的区别：

| 维度 | 通用 IM | 通知中台 |
|------|---------|---------|
| **消息可靠性** | 尽力送达，丢了可重发 | 必须送达，有 ACK 追踪 |
| **业务语义** | 聊天文本 | 订单状态/秒杀/物流事件 |
| **消息模型** | 单通道推送 | 双通道自适应（直推+可靠） |
| **幂等要求** | 不严格 | 严格幂等（订单不能推两次"已支付"） |
| **可观测性** | 基础的 | 完整的：直推/Kafka 比、ACK 率、P50/P99 |
| **业务闭环** | 无 | 订单状态机 7 种状态全链路追踪 |

面试时这样说：

> GoToIM 不是通用 IM，它是一个**消息投递中台**。区别在于：通用 IM 关注的是"聊天"这个功能，而中台关注的是"消息可靠到达"这个能力。它不关心消息内容是聊天还是订单，它关心的是：消息推了吗？推到了吗？客户端确认了吗？每种投递方式占比多少？延迟分布怎样？这对应了大厂中台的核心理念——**把通信能力抽象成基础服务，上层业务只关心"发什么"，不用关心"怎么发"**。

---

## 7. 为什么基于 goim

**面试追问："为什么选 goim 而不是从零写？"**

回答要点：
1. goim 是 B 站开源的成熟长连接框架，已经在生产环境验证过百万级并发
2. 复用它的 Comet 接入层（TCP/WebSocket 管理、Ring Buffer、Bucket 分片）——这些是长连接系统的"基建"，和业务无关，自己写容易踩坑
3. goim 缺的是"可靠性层"——它只管连接和推送，不管消息丢不丢。这正是我改造的核心方向
4. 基于开源项目改造 vs 从零写，在大厂也是非常常见的工作模式——体现的是"在已有系统上做增强"的能力

---

## 8. 相比原版 goim 改了什么

原版 goim 是一个通用的 IM 长连接框架。GoToIM 在它之上做了四个层面的改造：

**第一层：可靠性改造（从"尽力送达"到"100%到达"）**

- 新增 `internal/router/` 整个路由引擎——原版 goim 只做直推，丢了就丢了
- 新增离线队列 + Kafka 双保险
- 新增 ACK 状态追踪 + 幂等去重
- 新增 Snowflake 分布式 ID

**第二层：Session 增强（从"单连接"到"多端管理"）**

- 五维 Redis 索引替代原版简单 mid→key 映射
- 同设备互踢逻辑
- 离线消息同步（原版没有）

**第三层：消费者重写（从"简单转发"到"可靠投递引擎"）**

- `internal/worker/` 完全重写，替代原版 `internal/job/`
- CometClientPool fan-out 模式
- RoomAggregator 批处理 + WAL 防崩溃
- MQ 抽象层（可替换 Kafka 为其他 MQ）

**第四层：工程化增强**

- Snowflake ID 生成器
- Prometheus Metrics
- OpenTelemetry Tracing
- gRPC 拦截器（Recovery + Logging）
- Token Bucket Rate Limiter
- 完整的 e2e 测试 + 集成测试

---

## 9. 整体业务链路（以订单通知为例）

```
[用户操作]                     [系统行为]                      [消息通道]
─────────────────────────────────────────────────────────────────────

1. 创建订单
   用户点击"下单"      →  Notify Server 创建订单(status=created)
                          → 通知用户"订单已创建"
                          → Logic.RouteByUser → directPush → 客户端

2. 支付成功
   支付回调            →  Notify Server 更新订单(status=paid)
                          → 通知用户"支付成功"
                          → Router → 在线? gRPC直推 : Kafka+离线

3. 商家发货
   商家后台操作         →  Notify Server 更新订单(status=shipped)
                          → 通知用户"已发货" + 物流单号
                          → Router 双通道

4. 物流更新
   物流系统回调         →  Notify Server 推送物流轨迹
                          → 用户可能离线 → Kafka + 离线队列

5. 用户签收
   物流状态=已签收      →  Notify Server 更新(status=delivered)
                          → 推送"已签收"通知

6. 用户上线
   用户打开App          →  Comet.TCP connect → Logic.Connect
                          → OnUserOnline → 拉取离线队列
                          → ZRANGEBYSCORE offline:{uid} → 逐条推送

7. 用户ACK
   用户点击通知         →  Comet → Logic → ACKHandler
                          → ZREM 清理离线队列
                          → Metrics 记录 ACK 率

8. 运营看板
   管理员查看           →  Notify Server /platform/stats
                          → 展示: 直推比/Kafka比、P50/P99、ACK率
```

---

## 10. 项目最像大厂的地方

面试官问"你这个项目最像大厂的地方是什么"，可以这么回答：

**第一，可靠性思维，不是功能思维。**
普通项目关心"消息能不能发出去"，这个项目关心"消息能不能 100% 到达"。两条通道不是简单的 retry，而是有状态追踪、幂等去重、ACK 确认的完整闭环。这是 P0 级生产系统的思维。

**第二，可观测性不是事后加的，是设计的一部分。**
直推/Kafka 比例、P50/P99 延迟、ACK 率——这些监控不是"上线后看看"，而是从 Router 的 `directTotal`/`kafkaTotal` 原子计数器开始就设计好的。大厂最怕"系统是黑盒，出了事不知道哪里出的"。

**第三，接口抽象和分层清晰。**
MQ 抽象层（Producer/Consumer 接口）意味着你可以把 Kafka 换成 Pulsar 或 RocketMQ 而业务逻辑不改。Comet/Logic/Router/Worker 四层之间通过 gRPC 和接口通信，每一层可以独立扩缩容。这是微服务架构的核心思想。

**第四，WAL（写前日志）这种细节。**
RoomAggregator 在发 gRPC 之前先写本地 WAL，Crash 后重放。这不是什么高大上的技术，但这是真正在生产环境跑过的系统才会考虑的细节——"万一进程崩溃，正在积攒的这批房间消息怎么办？"

**第五，有完整的测试和压测体系。**
benchmarks/ 目录下有 8 种不同的压测场景（连接压测、Push 压测、房间压测、到达延迟压测），还有 HTML 报告生成器。integration_test/ 有完整的集成测试。Notfiy Server 有业务模拟器。这体现的是"对系统行为有信心，不只是代码能编译"。

---

> **一句话总结**：GoToIM 不是一个 Demo，它是一个经过可靠性设计、有可观测性、可独立扩缩容、有测试覆盖的生产级消息投递中台。

---

## 附录：面试常见追问速查

**Q: 为什么不是直接用 Kafka 做所有推送？**
A: Kafka 延迟在 100ms-2s，不能满足实时推送需求（如秒杀通知要求 < 100ms）。gRPC 直推延迟 < 50ms。所以用双通道——在线走快速通道，离线/失败走 Kafka。

**Q: 为什么 Redis + Kafka 组合而不是二选一？**
A: Redis ZSET 离线队列提供结构化存储和高效的范围查询（"用户上线后拉取 seq > N 的所有离线消息"）。Kafka 提供高吞吐的异步解耦和消费者组的负载均衡。两者职责不同，是互补关系。

**Q: 消息真的 100% 到达吗？**
A: "100%"是最终一致性的承诺，不是实时保证。前提是：客户端最终会在线、会 ACK、系统不丢数据。实现手段：双通道 + 重试 + 幂等 + 离线队列 TTL 7 天 + WAL + DLQ。

**Q: 怎么压测验证的？**
A: benchmarks/ 下有专门的 arrival_bench（到达延迟 benchmark），模拟 N 个在线用户 + M 个离线用户，统计从 push 到 ACK 的全链路延迟，计算到达率。

**Q: 为什么用 Snowflake 而不是 UUID？**
A: Snowflake 是趋势递增的 int64，比 UUID 字符串做 Redis key 更省内存；趋势递增特性对 B+Tree 索引友好；自带时间戳可排序。

---

# 第二阶段：项目目录与模块分析

---

## 1. 完整目录树

```
goim/
├── api/                              # Protobuf 定义 + 生成代码
│   ├── generate.go                   # go:generate 指令
│   ├── comet/comet.proto             # Comet gRPC 服务定义
│   ├── logic/logic.proto             # Logic gRPC 服务定义
│   ├── logic/ext.go                  # 自定义扩展方法 ★
│   ├── protocol/protocol.proto       # 核心 Proto 消息定义
│   ├── protocol/protocol.go          # 二进制协议编解码（TCP/WS 传输层）★
│   ├── protocol/operation.go         # 22 种操作码定义 ★
│   └── protocol/message.go           # MsgBody/AckBody/SyncBody 二进制编码 ★
│
├── cmd/                              # 四个服务的入口
│   ├── comet/main.go                 # Comet 网关入口
│   ├── logic/main.go                 # Logic 业务层入口
│   ├── job/main.go                   # Worker 消费层入口
│   └── notify-server/main.go         # 通知服务入口（电商 Demo）★
│
├── internal/                         # 核心应用逻辑
│   ├── comet/                        # 连接网关层（原版 + 增强）
│   │   ├── server.go                 # Server 结构体，Bucket 管理，NewServer
│   │   ├── server_tcp.go             # TCP accept → serveTCP → dispatchTCP 完整生命周期
│   │   ├── server_websocket.go       # WebSocket 同等实现
│   │   ├── bucket.go                 # Bucket 结构体，CityHash 分片管理
│   │   ├── channel.go                # Channel 结构体（Ring/PriorityQueue/Signal）
│   │   ├── room.go                   # Room 双向链表
│   │   ├── ring.go                   # SPSC 无锁环形缓冲区 ★
│   │   ├── round.go                  # Reader/Writer/Timer 资源池 ★
│   │   ├── priority_queue.go         # 两级优先级队列（High/Normal）★
│   │   ├── operation.go              # Server.Operate 操作分发
│   │   ├── grpc/server.go            # Comet gRPC Server（PushMsg/Broadcast）
│   │   ├── whitelist.go              # Debug 白名单日志
│   │   └── conf/conf.go              # Comet 配置
│   │
│   ├── logic/                        # 业务逻辑层（原版 + 大幅改造）
│   │   ├── logic.go                  # Logic 编排器，服务初始化 ★
│   │   ├── conn.go                   # Connect/Disconnect/Heartbeat/Receive RPC ★
│   │   ├── push.go                   # PushKeys/PushMids/PushRoom/PushAll ★
│   │   ├── comet.go                  # CometPusher：到所有 Comet 的 gRPC 客户端池 ★
│   │   ├── balancer.go               # 加权负载均衡 + 区域亲和
│   │   ├── nodes.go                  # 节点发现 + 区域路由
│   │   ├── online.go                 # 在线统计（Top/Room/Total/Summary）
│   │   ├── http/server.go            # Gin HTTP 路由注册
│   │   ├── http/push.go              # HTTP Push 处理器
│   │   ├── http/nodes.go             # HTTP 节点发现处理器
│   │   ├── http/online.go            # HTTP 在线统计处理器
│   │   ├── http/sync.go              # HTTP 离线同步处理器 ★
│   │   ├── http/middleware.go         # Logger + Panic Recovery 中间件 ★
│   │   ├── http/result.go            # 响应辅助函数
│   │   ├── grpc/server.go            # Logic gRPC Server
│   │   ├── dao/dao.go                # DAO 结构体（Redis pool + Kafka producer）★
│   │   ├── dao/dao_iface.go          # SessionDAO/MessageDAO/PushDAO 接口 ★
│   │   ├── dao/redis.go              # Redis 操作实现（Session/Message/Offline/Seq）★
│   │   ├── dao/kafka.go              # 旧版 Kafka Producer（保留兼容）
│   │   ├── model/metadata.go         # Discovery 元数据常量
│   │   ├── model/room.go             # Room Key 编解码
│   │   ├── model/online.go           # Online/Top 数据模型
│   │   ├── service/session.go        # SessionManager：五维 Redis 索引 + 设备互踢 ★
│   │   ├── service/sync.go           # SyncService：离线消息同步 ★
│   │   ├── service/ack.go            # 基础 ACK 服务
│   │   └── conf/conf.go              # Logic 配置（Redis/Kafka/MQ/Snowflake）
│   │
│   ├── router/                       # 消息路由引擎（★ 完全自定义）
│   │   ├── router.go                 # DispatchEngine 结构体 + 委托方法
│   │   ├── dispatch.go               # RouteByUser/RouteByRoom/RouteBroadcast ★★★
│   │   ├── ack.go                    # ACKHandler：ACK 状态机 ★★
│   │   ├── idempotency.go            # IdempotencyChecker：64-shard FNV Hash ★★
│   │   ├── protocol.go               # ProtocolConverter：JSON ↔ Binary
│   │   ├── ratelimit.go              # RateLimiter：Token Bucket
│   │   └── pushmsg.go               # pushMsgBytes 序列化辅助
│   │
│   ├── worker/                       # 消息投递引擎（★ 完全自定义）
│   │   ├── worker.go                 # DeliveryWorker：Kafka 消费调度 ★
│   │   ├── dispatch.go               # pushKeys/broadcast/broadcastRoom
│   │   ├── comet_pool.go             # CometClientPool：gRPC 连接池 + fan-out ★
│   │   ├── room_aggregator.go        # RoomAggregator：批处理 + WAL ★
│   │   ├── ack_reporter.go           # ACKReporter：投递结果上报 ★
│   │   └── conf/conf.go              # Worker 配置
│   │
│   ├── mq/                           # MQ 抽象层（★ 完全自定义）
│   │   ├── types.go                  # Message/MessageHandler/DeliveryStatus ★
│   │   ├── producer.go               # Producer 接口 ★
│   │   ├── consumer.go               # Consumer 接口 ★
│   │   └── kafka/
│   │       ├── producer.go           # Kafka Producer（SyncProducer + 主题路由）★
│   │       ├── consumer.go           # Kafka Consumer（ConsumerGroup + 延迟消息）★
│   │       └── dlq.go               # Dead Letter Queue ★
│   │
│   ├── notify/                       # 电商通知平台（★ 完全自定义）
│   │   ├── server.go                 # Gin HTTP Server + 路由注册
│   │   ├── conf/config.go            # Notify 配置
│   │   ├── handler/                  # HTTP 处理器
│   │   │   ├── order.go              # 订单 CRUD + 状态变更
│   │   │   ├── flash_sale.go         # 秒杀通知
│   │   │   ├── logistics.go          # 物流更新
│   │   │   ├── platform.go           # 平台统计 + 模拟器控制
│   │   │   └── user.go              # 用户通知历史
│   │   ├── model/                    # 数据模型
│   │   │   ├── order.go              # Order + OrderItem
│   │   │   ├── notification.go       # Notification
│   │   │   └── flash_sale.go         # FlashSale
│   │   ├── service/                  # 业务服务
│   │   │   ├── push_client.go        # HTTP Client → goim Logic API
│   │   │   ├── order_notify.go       # 订单生命周期通知服务
│   │   │   └── flash_sale.go         # 秒杀通知服务
│   │   └── simulator/                # 负载模拟器
│   │       ├── engine.go             # 模拟器编排器
│   │       ├── order_lifecycle.go    # 订单生命周期模拟
│   │       ├── flash_sale.go         # 秒杀流量模拟
│   │       └── traffic.go           # 流量模式生成器
│   │
│   └── grpcx/                        # gRPC 拦截器（★ 自定义）
│       └── interceptor.go            # Recovery + Logging 拦截器
│
├── pkg/                              # 共享工具包
│   ├── bufio/bufio.go               # TCP 缓冲 I/O（Reader/Writer + 池）
│   ├── bytes/buffer.go              # 字节缓冲池（sync.Pool）
│   ├── bytes/writer.go              # 写缓冲（Peek + Write）
│   ├── encoding/binary/endian.go    # 大端整数编解码
│   ├── time/timer.go                # 时间轮（O(1) add/del）
│   ├── time/duration.go             # TOML Duration 类型
│   ├── log/log.go                   # Zap 结构化日志
│   ├── websocket/                   # WebSocket 服务端实现
│   ├── snowflake/snowflake.go       # Snowflake 分布式 ID ★
│   ├── metrics/metrics.go           # Prometheus Metrics ★
│   ├── tracing/tracing.go           # OpenTelemetry Tracing ★
│   └── ratelimit/token_bucket.go    # Token Bucket 限流 ★
│
├── benchmarks/                       # 性能压测工具（8 种场景）
│   ├── arrival_bench/main.go        # 消息到达延迟 Benchmark
│   ├── conn_bench/main.go           # 连接压测
│   ├── push_bench/main.go           # Push 性能压测
│   ├── multi_push/main.go           # 多用户 Push 压测
│   ├── push_room/main.go            # 房间 Push 压测
│   ├── push_rooms/main.go           # 多房间 Push 压测
│   ├── worker_bench/main.go         # Worker 压测
│   ├── report_gen/main.go           # HTML 报告生成器
│   └── run.ps1 / run.sh             # 压测启动脚本
│
├── deploy/                           # Docker 部署
│   ├── docker-compose.yml            # 完整技术栈（Redis+Kafka+Discovery+4服务）
│   ├── Dockerfile                    # 多服务构建
│   └── configs/                      # Docker 环境配置
│
├── web/                              # 前端
│   ├── dashboard/                    # Vue/TS 管理面板
│   └── order-tracker/                # 订单追踪 SPA
│
├── examples/                         # 客户端示例
│   ├── javascript/main.go           # JS WebSocket Demo Server
│   ├── chat-demo/                    # Web 聊天 Demo（HTML/JS SPA）
│   └── e2e_test_client/main.go      # E2E 测试客户端
│
├── test_e2e/main.go                 # E2E 测试主程序
├── integration_test/main_test.go    # 集成测试
├── third_party/discovery/           # Fork 的 bilibili/discovery
└── scripts/                         # 运维脚本
```

---

## 2. 四层架构模块职责

| 层 | 模块 | 端口 | 职责 | 原版/改造 | 代码量 |
|---|------|------|------|----------|--------|
| 接入层 | `internal/comet` | TCP:3101, WS:3102, gRPC:3109 | TCP/WS 连接管理，CityHash 分桶，RingBuffer，PriorityQueue | 原版 + 增强 | ~2000行 |
| 业务层 | `internal/logic` | HTTP:3111, gRPC:3119 | 认证、Session、Push、负载均衡、节点发现 | 原版 + 大幅改造 | ~3000行 |
| 路由层 | `internal/router` | — | 双通道投递、幂等去重、ACK 状态机、协议转换 | **完全自定义** | ~800行 |
| 消费层 | `internal/worker` | — | Kafka 消费、gRPC 连接池、Room 聚合、WAL | **完全自定义** | ~600行 |
| 队列层 | `internal/mq` | — | MQ 抽象接口、Kafka 实现、DLQ | **完全自定义** | ~400行 |
| 业务层 | `internal/notify` | HTTP:3121 | 订单通知、秒杀、物流、模拟器 | **完全自定义** | ~800行 |
| 工具层 | `pkg/*` | — | Snowflake、Metrics、Tracing、RateLimit | **完全自定义** | ~600行 |

> 注：自定义代码总量约 4000+ 行，占项目核心逻辑的 60%+。

---

## 3. 模块依赖图

```
                        ┌──────────────┐
                        │  外部业务方    │
                        └──────┬───────┘
                               │ HTTP POST
                               ▼
                   ┌───────────────────────┐
                   │    Notify Server       │
                   │    (:3121 Gin HTTP)    │
                   │    订单/秒杀/物流      │
                   └───────────┬───────────┘
                               │ HTTP Client → Logic API
                               ▼
         ┌─────────────────────────────────────────┐
         │              Logic Layer                  │
         │                                          │
         │  ┌──────────────────────────────────┐   │
         │  │  http/server.go (Gin :3111)      │   │
         │  │  POST /push/{keys,mids,room,all} │   │
         │  │  GET  /online/{top,room,total}   │   │
         │  │  GET  /nodes/{weighted,instances}│   │
         │  └────────────┬─────────────────────┘   │
         │               │                          │
         │  ┌────────────▼─────────────────────┐   │
         │  │  logic.go (Logic 编排器)          │   │
         │  │  ├─ SessionManager (五维 Redis)  │   │
         │  │  ├─ CometPusher (gRPC 客户端池)   │   │
         │  │  ├─ LoadBalancer (加权+区域)      │   │
         │  │  ├─ SyncService (离线同步)        │   │
         │  │  └─ Router (DispatchEngine) ──────┤   │
         │  └────────────┬─────────────────────┘   │
         │               │                          │
         │  ┌────────────▼─────────────────────┐   │
         │  │  Router (DispatchEngine) ★核心   │   │
         │  │  ├─ RouteByUser (双通道核心)      │   │
         │  │  ├─ ACKHandler (状态机)           │   │
         │  │  └─ IdempotencyChecker (去重)     │   │
         │  └──┬─────────┬──────────┬──────────┘   │
         └─────┼─────────┼──────────┼──────────────┘
               │         │          │
      ┌────────▼──┐ ┌───▼────┐ ┌──▼──────────┐
      │   Redis   │ │ Kafka  │ │ Comet Client │
      │  Session  │ │ Push   │ │ (gRPC Pool)  │
      │  Offline  │ │ Room   │ └──────┬───────┘
      │  Status   │ │ Ack    │        │
      └─────┬─────┘ └───┬────┘        │
            │           │             │
            │    ┌──────▼──────────┐  │
            │    │  Worker 消费层   │  │
            │    │  ConsumerGroup  │  │
            │    │  CometClientPool│──┤ (gRPC → Comet)
            │    │  RoomAggregator │  │
            │    │  ACKReporter ───┼──┼─→ Kafka ACK Topic
            │    └──────┬──────────┘  │
            │           │             │
            │    ┌──────▼─────────────▼──┐
            │    │     Comet 接入层       │
            │    │  ┌──────────────────┐ │
            │    │  │ gRPC Server :3109│ │← Logic/Worker 推送入口
            │    │  │ TCP:3101 WS:3102 │ │→ 客户端连接入口
            │    │  └──────────────────┘ │
            └────│─── Buckets (32-way)   │
                 │   Ring Buffer          │
                 │   PriorityQueue        │
                 └───────────────────────┘
                          │ TCP/WebSocket
                          ▼
                    ┌──────────┐
                    │  客户端   │
                    └──────────┘
```

**关键依赖关系总结：**

| 调用方 | 被调用方 | 协议 | 用途 |
|--------|---------|------|------|
| Notify Server | Logic HTTP :3111 | HTTP | 推送订单/秒杀/物流通知 |
| Logic (Router) | Comet gRPC :3109 | gRPC | 直接推送消息到客户端 |
| Logic (Router) | Redis | TCP | Session/Offline/Status 读写 |
| Logic (Router) | Kafka | TCP | 可靠通道消息入队 |
| Worker | Kafka | TCP | 消费可靠通道消息 |
| Worker | Comet gRPC :3109 | gRPC | 异步投递到客户端 |
| Comet | Logic gRPC :3119 | gRPC | 认证/心跳/ACK 转发 |
| Comet | Discovery :7171 | HTTP | 注册/发现 |

---

## 4. 启动流程

```
[1] Discovery 服务先启动
    └─ 注册中心就绪，等待各服务注册

[2] Comet 启动 (cmd/comet/main.go)
    ├─ 读取 comet.toml 配置
    ├─ 初始化 Tracing (OTLP)
    ├─ 初始化 Discovery 客户端 → 注册自己
    ├─ 创建 gRPC 客户端连接 Logic
    ├─ NewServer() → 创建 32 个 Bucket + Round 资源池
    ├─ 启动 TCP Listener (:3101) → N 个 accept goroutine
    ├─ 启动 WebSocket Listener (:3102)
    ├─ 启动 gRPC Server (:3109)
    └─ 等待信号 (SIGINT/SIGTERM)

[3] Logic 启动 (cmd/logic/main.go)
    ├─ 读取 logic.toml 配置
    ├─ 初始化 Tracing
    ├─ 初始化 Discovery 客户端 → 注册自己
    ├─ 初始化 Redis 连接池
    ├─ 初始化 Kafka Producer (SyncProducer)
    ├─ 初始化 MQ Producer (新抽象层)
    ├─ 初始化 Snowflake ID 生成器
    ├─ NewLogic() → 创建 SessionManager + CometPusher + Router
    ├─ 启动 HTTP Server (:3111)
    ├─ 启动 gRPC Server (:3119)
    └─ 等待信号

[4] Worker 启动 (cmd/job/main.go)  Job/DeliveryWorker
    ├─ 读取 job.toml 配置
    ├─ 初始化 Discovery 客户端 → Watch Comet 节点
    ├─ New() → 创建 Kafka ConsumerGroup + CometClientPool
    ├─ 订阅 Kafka Topics (push/room/all)
    ├─ Start() → 阻塞式消费循环
    └─ 等待信号

[5] Notify Server 启动 (cmd/notify-server/main.go) [可选，Demo用]
    ├─ 读取 notify.toml 配置
    ├─ 创建 Logic HTTP Client
    ├─ 创建 OrderNotifyService + FlashSaleService
    ├─ 启动 Gin HTTP Server (:3121)
    ├─ [可选] 自动启动模拟器
    └─ 等待信号
```

**启动顺序依赖：**
```
Discovery → Comet (依赖 Discovery) → Logic (依赖 Discovery + Redis + Kafka) → Worker (依赖 Kafka + Comet) → Notify (依赖 Logic)
```

---

## 5. 配置分析

### Comet 配置关键参数

```toml
[tcp]
bind = [":3101"]           # TCP 监听地址
sndbuf = 4096              # SO_SNDBUF
rcvbuf = 4096              # SO_RCVBUF
keepalive = false          # TCP KeepAlive
reader = 32                # Reader 池分片数
readBuf = 1024             # 每分片 Buffer 数
readBufSize = 8192         # 每个 Buffer 大小 (8KB)
writer = 32                # Writer 池分片数
writeBuf = 1024            # 每分片 Buffer 数
writeBufSize = 8192        # 每个 Buffer 大小 (8KB)

[websocket]
bind = [":3102"]           # WS 监听地址

[protocol]
timer = 32                 # Timer 池分片数
timerSize = 1000           # 时间轮大小
handshakeTimeout = "8s"    # 握手超时
rateLimit = 100            # 每连接每秒最大消息数 (新增 ★)
rateBurst = 200            # 突发容量 (新增 ★)

[bucket]
size = 32                  # Bucket 数量 (CityHash 分片数)
channel = 1024             # chs map 初始容量
room = 1024                # rooms map 初始容量
routineAmount = 32         # 房间广播 goroutine 数
routineSize = 1024         # 房间广播 channel buffer
```

**面试要点**：
- `size=32` + CityHash → 全局锁拆分为 32 个 Bucket 锁，锁粒度降低 32 倍
- `reader/writer=32` + `readBuf/writeBuf=1024` → 总预分配内存 = 32×1024×8KB×2 ≈ 512MB（所有连接共享，非每连接分配）
- `rateLimit=100` → 防止单连接洪水攻击，每连接每秒最多 100 条消息

### Logic 配置关键参数

```toml
[rpcServer]
addr = ":3119"             # gRPC 监听（Comet 调用）
[httpServer]
addr = ":3111"             # HTTP 监听（业务方调用）

[kafka]
brokers = ["localhost:9092"]
topic = "goim_push"        # 旧版单 Topic（兼容）
pushTopic = "goim_push_user"     # 用户推送 Topic (新增 ★)
roomTopic = "goim_push_room"     # 房间推送 Topic (新增 ★)
allTopic  = "goim_push_all"      # 全服广播 Topic (新增 ★)
ackTopic  = "goim_ack"           # ACK 回调 Topic (新增 ★)

[redis]
network = "tcp"
addr = "localhost:6379"
poolSize = 100             # 连接池大小
idleTimeout = "300s"
expire = "30m"             # Session TTL

[snowflake]
machineID = 1              # 机器 ID (0-1023)

[node]
defaultDomain = "goim.com"
heartbeat = 30             # Comet 心跳/续期间隔 (秒)
regionWeight = 1.0         # 区域权重
```

**面试要点**：
- 4 个 Kafka Topic 按消息类型隔离 → 每个 Topic 可独立扩容 Partition
- Snowflake machineID → 多 Logic 实例部署时需要唯一配置
- Session TTL 30 分钟 → 心跳续期，30 分钟无心跳自动清理

---

## 6. 协议分析

### 三层协议体系

```
┌─────────────────────────────────────────┐
│ [业务层] JSON (外部 API)                  │
│ {"from": 123, "to": 456, "text": "..."}  │
│ 用途: HTTP API 请求体                     │
│ 实现: ProtocolConverter (router/protocol.go) │
└──────────────┬──────────────────────────┘
               │ 转换
┌──────────────▼──────────────────────────┐
│ [传输层] Protobuf (内部 RPC + Kafka)      │
│ pb.PushMsg { type, operation, server,    │
│   keys, room, msg, speed }              │
│ 用途: gRPC 调用 + Kafka 消息体            │
│ 文件: api/logic/logic.proto              │
└──────────────┬──────────────────────────┘
               │ 编码
┌──────────────▼──────────────────────────┐
│ [网络层] 二进制协议 (TCP/WebSocket)        │
│ 16 字节固定头 + 可变 Body                 │
│ [PackLen(4)][HeaderLen(2)][Ver(2)][Op(4)]│
│   [Seq(4)][Body(N)]                     │
│ 最大 Body: 4KB (RawBody 透传除外)         │
│ 文件: api/protocol/protocol.go            │
└─────────────────────────────────────────┘
```

### 22 种操作码 (Operation Codes)

| 分类 | OpCode | 常量 | 方向 | 用途 |
|------|--------|------|------|------|
| **连接管理** | 0 | OpHandshake | C→S | 握手请求 |
| | 1 | OpHandshakeReply | S→C | 握手响应 |
| | 2 | OpHeartbeat | C→S | 心跳请求 |
| | 3 | OpHeartbeatReply | S→C | 心跳响应 |
| | 4 | OpAuth | C→S | 认证请求 |
| | 5 | OpAuthReply | S→C | 认证响应 |
| **消息** | 6 | OpSendMsg | C→S | 发送消息 |
| | 7 | OpSendMsgReply | S→C | 发送响应 |
| **房间** | 10 | OpChangeRoom | C→S | 加入/切换房间 |
| | 12 | OpSub | C→S | 订阅操作类型 |
| | 13 | OpUnsub | C→S | 取消订阅 |
| **ACK/同步** | 18 | OpSendMsgAck | C→S | 发送消息 ACK (新增 ★) |
| | 19 | OpPushMsgAck | C→S | 推送消息 ACK (新增 ★) |
| | 20 | OpSyncReq | C→S | 同步请求 (新增 ★) |
| | 21 | OpSyncReply | S→C | 同步响应 (新增 ★) |
| **控制** | 22 | OpKickConnection | S→C | 被踢下线通知 (新增 ★) |
| **内部信号** | 10 | OpProtoReady | 内部 | Ring Buffer 有数据可读 |
| | 11 | OpProtoFinish | 内部 | 连接关闭信号 |
| **透传** | - | OpRaw | S→C | 原始帧透传（gRPC→TCP） |

**为什么用自定义二进制协议而不是直接用 Protobuf over TCP？**

1. **零依赖**：客户端不需要 Protobuf 库，纯二进制解析
2. **极低开销**：固定 16 字节头，比 Protobuf 的 varint 编码更简单
3. **流式友好**：PackLen 前缀可以让 TCP 流正确拆包，不需要 Protobuf 的 Length-delimited 包装
4. **OpRaw 透传优化**：gRPC Push 来的消息可以跳过解序列化→再序列化，直接透传 Body 到 TCP

### gRPC 服务定义

**Comet gRPC Service**（Logic/Worker 调用 Comet）：
```protobuf
service Comet {
    rpc PushMsg(PushMsgReq) returns (PushMsgReply);           // 点到点推送
    rpc Broadcast(BroadcastReq) returns (BroadcastReply);      // 全服广播
    rpc BroadcastRoom(BroadcastRoomReq) returns (BroadcastRoomReply); // 房间广播
    rpc Rooms(RoomsReq) returns (RoomsReply);                  // 查询活跃房间
}
```

**Logic gRPC Service**（Comet 调用 Logic）：
```protobuf
service Logic {
    rpc Connect(ConnectReq) returns (ConnectReply);       // 客户端认证 (新增 sid/sessions)
    rpc Disconnect(DisconnectReq) returns (DisconnectReply);
    rpc Heartbeat(HeartbeatReq) returns (HeartbeatReply);
    rpc RenewOnline(OnlineReq) returns (OnlineReply);
    rpc Receive(ReceiveReq) returns (ReceiveReply);      // 消息转发 (新增 ACK/Sync 处理)
    rpc Nodes(NodesReq) returns (NodesReply);             // 节点发现
    rpc AckMessage(AckReq) returns (AckReply);            // (新增 ★) ACK 处理
    rpc SyncOffline(SyncOfflineReq) returns (SyncOfflineReply); // (新增 ★) 离线同步
}
```

---

## 7. 原版 goim vs GoToIM 改造对照表

| 功能 | 原版 goim | GoToIM | 改造文件 |
|------|----------|--------|---------|
| **消息推送** | HTTP → Logic → gRPC → Comet → TCP | 双通道：gRPC 直推 + Kafka/Redis 离线 | `internal/router/dispatch.go` (新增) |
| **消息去重** | 无 | HSETNX Redis + 64-shard FNV Memory | `internal/router/idempotency.go` (新增) |
| **ACK 追踪** | 无 | Pending→Delivered→Acked 状态机 | `internal/router/ack.go` (新增) |
| **离线消息** | 无 | Redis ZSET + 上线拉取 | `internal/logic/service/sync.go` (新增) |
| **消息 ID** | 无 | Snowflake 全局唯一 | `pkg/snowflake/snowflake.go` (新增) |
| **Session 索引** | mid→key (单一映射) | 五维索引 (sid/uid/device/key/server) | `internal/logic/service/session.go` (新增) |
| **设备互踢** | 无 | 同设备新连接 → 原子 Kick 旧连接 | `internal/logic/service/session.go` (新增) |
| **消费者** | 旧版 `internal/job/` | DeliveryWorker + CometClientPool | `internal/worker/` (重写) |
| **房间批量** | 无 | RoomAggregator + WAL | `internal/worker/room_aggregator.go` (新增) |
| **Kafka 抽象** | 无 | Producer/Consumer 接口 | `internal/mq/` (新增) |
| **延迟消息** | 无 | Kafka Header `goim_delayed_until` | `internal/mq/kafka/consumer.go` (新增) |
| **DLQ** | 无 | Dead Letter Queue | `internal/mq/kafka/dlq.go` (新增) |
| **监控指标** | 无 | Prometheus Metrics (连接数/推送量/ACK率/延迟) | `pkg/metrics/metrics.go` (新增) |
| **链路追踪** | 无 | OpenTelemetry OTLP | `pkg/tracing/tracing.go` (新增) |
| **限流** | 无 | Token Bucket (全局+每用户+每连接) | `pkg/ratelimit/` (新增) |
| **连接限速** | 无 | 每连接 rateLimit/rateBurst | `internal/comet/server_tcp.go` (增强) |
| **优先级队列** | 单通道 Signal | 两级 PriorityQueue (High/Normal) | `internal/comet/priority_queue.go` (新增) |
| **gRPC 拦截器** | 无 | Recovery + Logging | `internal/grpcx/` (新增) |
| **业务 Demo** | 无 | Notify Server (订单/秒杀/物流) | `internal/notify/` (新增) |
| **压力测试** | 部分 | 8 种 Benchmark + 报告生成 | `benchmarks/` (增强) |
| **操作码** | 0-17 | 0-22 (新增 ACK/Sync/Kick) | `api/protocol/operation.go` (增强) |
| **Logic API** | push/online/nodes | + push/offline, sync, /metrics | `internal/logic/http/` (增强) |

---

## 8. 关键技术决策与 Trade-off

### 决策 1：Router 独立为层而非放在 Logic 里

**Why**: Logic 层已经承担了认证、Session、节点发现、在线统计等职责。Router 的双通道投递、幂等、ACK 是非常内聚的可靠性逻辑，放在 Logic 里会让 Logic 变成"上帝对象"。

**Trade-off**: 多了一层抽象，调用链变长了（Logic → Router → Comet/Kafka/Redis），但换来的是：Router 可以独立测试、独立替换（比如以后换投递策略）。

### 决策 2：MQ 抽象层

**Why**: 直接调 sarama 会耦合到 Kafka API。抽象 Producer/Consumer 接口后，理论上可以换 Pulsar/RocketMQ/Redis Stream。

**Trade-off**: 当前 Worker.New() 还是直接调了 `kafka.NewConsumer()`，不是完全解耦。这是一个"接口先行，实现渐进"的策略——先用接口约束行为，具体的工厂模式可以后面补。

### 决策 3：Protobuf 用于内部 RPC，二进制协议用于客户端

**Why**: 内部 RPC 用 Protobuf 是为了类型安全和代码生成。客户端用二进制协议是为了零依赖和最低开销。中间通过 `ProtocolConverter` 和 `OpRaw` 透传做桥接。

**Trade-off**: 两套协议意味着要维护两套编解码，但换来了各自的优化空间。客户端不需要引入 Protobuf 库，移动端包体积更小。

---

> **第二阶段总结（面试记忆版）**：项目分四层架构——Comet 管连接、Logic 管业务、Router 管路由、Worker 管消费。我在原版 goim 基础上新增了 4000+ 行代码，零改动的文件不到 30%。核心改造方向是"可靠性"——给原本"只管推不管到"的系统补上了双通道、幂等、ACK、离线队列、WAL 这一整套可靠性闭环。

---

# 第三阶段：核心链路深挖

---

## 链路 1：登录/建连链路

### 时序图

```
客户端                    Comet                      Logic                    Redis
  │                        │                          │                        │
  │──TCP Connect──────────▶│                          │                        │
  │                        │                          │                        │
  │──OpAuth(uid,token)────▶│                          │                        │
  │                        │ authTCP() 读取 8s 超时     │                        │
  │                        │──gRPC Connect────────────▶│                        │
  │                        │  (mid, token, key,        │                        │
  │                        │   server, ip)             │                        │
  │                        │                          │ conn.go: Connect()     │
  │                        │                          │ 1. 解析 token          │
  │                        │                          │ 2. 创建 Session        │
  │                        │                          │──GET device_session───▶│
  │                        │                          │  {uid}:{deviceID}      │
  │                        │                          │◀──oldSID──────────────│
  │                        │                          │                        │
  │                        │                          │ if oldSID != "":        │
  │                        │                          │──Kick 旧 Session───────▶│
  │                        │                          │  DelSession Pipeline    │
  │                        │◀──gRPC KickConnection────│  (4 keys 原子删除)      │
  │──OpKickConnection─────▶│ (通知旧连接关闭)          │                        │
  │  (旧设备收到踢下线)      │                          │                        │
  │                        │                          │──AddSession Pipeline──▶│
  │                        │                          │  HSET session:{sid}    │
  │                        │                          │  HSET user_sessions:   │
  │                        │                          │       {uid}            │
  │                        │                          │  SET device_session:   │
  │                        │                          │       {uid}:{device}   │
  │                        │                          │  SET key_sid:{key}     │
  │                        │                          │◀──OK──────────────────│
  │                        │                          │                        │
  │                        │◀──gRPC ConnectReply──────│ 3. GetSessions(uid)    │
  │                        │   (mid, key, sid,        │   返回所有在线设备       │
  │                        │    sessions, rid, hb)    │   4. OnUserOnline(uid) │
  │                        │                          │     拉取离线消息        │
  │                        │                          │──ZRANGEBYSCORE────────▶│
  │                        │                          │  offline:{uid}         │
  │                        │                          │◀──msgIDs──────────────│
  │                        │                          │                        │
  │                        │                          │──DirectPush(sessions)  │
  │◀──OpAuthReply─────────│                          │  (离线消息推送到所有设备) │
  │  (mid,key,sid,sessions)│                          │                        │
  │                        │                          │                        │
  │──OpSyncReply(离线消息)──▶(来自 SyncService)        │                        │
  │                        │                          │                        │
  │ 连接建立完成             │                          │                        │
  │ (进入消息循环)           │                          │                        │
```

### 核心函数调用链

```
Comet: acceptTCP → serveTCP → authTCP → s.Connect(gRPC)
  → Logic.Connect:
      1. parseToken(body) → mid, deviceID, platform
      2. generate sid (UUID)
      3. sessionMgr.Create(sess)
         ├─ dao.GetDeviceSession(uid, deviceID) → oldSID
         ├─ if oldSID → sessionMgr.Kick(oldSID)
         │    ├─ GetSession(sid) → server, key
         │    ├─ dao.DelSession(sid, uid, deviceID, key) [Pipeline]
         │    ├─ local.Delete(uid)
         │    └─ kicker.KickConnection(ctx, server, key) [gRPC → Comet]
         └─ dao.AddSession(...) [Pipeline 6 条 Redis 命令]
      4. sessionMgr.GetSessions(uid) → []*Session
      5. go syncSvc.OnUserOnline(uid, lastSeq)
         ├─ GetOfflineQueue(uid, lastSeq, 100) [ZRANGEBYSCORE]
         ├─ for each msgID: GetMessageStatus → parse → build SyncReplyBody
         └─ pusher.DirectPush(sessions, OpSyncReply, syncBytes)
      6. return ConnectReply{mid, key, sid, sessions, rid, heartbeat}
  ← Comet: 收到 reply，ch.Mid=mid, ch.Key=key
    → b.Put(rid, ch) [注册到 Bucket]
    → 写入 OpAuthReply 到 CliProto Ring
    → go dispatchTCP() [启动写协程]
    → 进入消息循环
```

### 关键结构体

```go
// ConnectReq (Logic gRPC)
type ConnectReq struct {
    Mid    int64   // 用户 ID
    Key    string  // 连接 key（Comet 生成）
    Server string  // Comet 节点 ID
    Body   []byte  // JSON: {"mid":123,"token":"xxx","device_id":"xxx","platform":"ios"}
}

// ConnectReply (Logic gRPC)
type ConnectReply struct {
    Mid      int64    // 用户 ID
    Key      string   // 连接 key
    Sid      string   // Session ID (UUID)
    Sessions []string // 所有在线 Session JSON 列表
    Rid      int32    // 默认 Room ID
    Hb       int64    // 心跳间隔 (秒)
}
```

---

## 链路 2：Push 链路（双通道核心 ★★★ 最重要）

### 时序图

```
业务方               Logic HTTP           Router                Redis         Comet gRPC       客户端
  │                    │                    │                     │              │               │
  │─POST /push/mids──▶│                    │                     │              │               │
  │ {to_uid, text}    │                    │                     │              │               │
  │                    │ pushMids()         │                     │              │               │
  │                    │─GenerateMsgID()───▶│                     │              │               │
  │                    │  Snowflake.Next()  │                     │              │               │
  │                    │◀──msgID───────────│                     │              │               │
  │                    │                    │                     │              │               │
  │                    │─RouteByUser()─────▶│                     │              │               │
  │                    │  (msgID,uid,op,    │                     │              │               │
  │                    │   body,seq)        │                     │              │               │
  │                    │                    │                     │              │               │
  │                    │                    │ [1] 幂等检查         │              │               │
  │                    │                    │─TrackMessage()─────▶│              │               │
  │                    │                    │  HSETNX msg:{msgID} │              │               │
  │                    │                    │  status pending     │              │               │
  │                    │                    │◀──OK (原子抢占成功)──│              │               │
  │                    │                    │                     │              │               │
  │                    │                    │ [2] 在线检查         │              │               │
  │                    │                    │─IsOnline(uid)──────▶│              │               │
  │                    │                    │  HGETALL user_       │              │               │
  │                    │                    │  sessions:{uid}      │              │               │
  │                    │                    │◀──sessions──────────│              │               │
  │                    │                    │  (3 个设备在线)       │              │               │
  │                    │                    │                     │              │               │
  │                    │                    │ [3a] 快速通道        │              │               │
  │                    │                    │ directPush(sessions) │              │               │
  │                    │                    │─────────────────────────────────▶│               │
  │                    │                    │  for each session:   │              │               │
  │                    │                    │   gRPC PushMsg(srv,  │              │               │
  │                    │                    │   [key], op, body)   │              │               │
  │                    │                    │   500ms timeout each │              │               │
  │                    │                    │                     │              │ Bucket(key)──▶│
  │                    │                    │                     │              │ Channel.Push()│
  │                    │                    │                     │              │ Signal()      │
  │                    │                    │                     │              │──WriteTCP────▶│
  │                    │                    │◀──success───────────│              │               │
  │                    │                    │                     │              │               │
  │                    │                    │ [3b] 结果处理        │              │               │
  │                    │                    │ 全部成功 (3/3):      │              │               │
  │                    │                    │  MarkDelivered()───▶│              │               │
  │                    │                    │   HSET status        │              │               │
  │                    │                    │   delivered          │              │               │
  │                    │                    │  directTotal++       │              │               │
  │                    │                    │                     │              │               │
  │                    │                    │ [3c] 部分失败的场景:  │              │               │
  │                    │                    │ (2/3 成功, 1/3 失败) │              │               │
  │                    │                    │                     │              │               │
  │                    │                    │ reliableEnqueue(     │              │               │
  │                    │                    │   failedSessions)    │              │               │
  │                    │                    │                     │              │               │
  │                    │                    │  ZADD offline:{uid}─▶│              │               │
  │                    │                    │  {seq} {msgID}       │              │               │
  │                    │                    │  TTL 7 days          │              │               │
  │                    │                    │                     │              │               │
  │                    │                    │  Kafka PushTopic─────│──▶ Worker    │               │
  │                    │                    │  partition=uid       │              │               │
  │                    │                    │  kafkaTotal++        │              │               │
  │                    │                    │                     │              │               │
  │◀──Response─────────│                    │                     │              │               │
  │  {msg_id, code}    │                    │                     │              │               │
```

### RouteByUser 完整决策树

```
RouteByUser(msgID, toUID, op, body, seq)
  │
  ├─ [Step 0] msgID 为空? → Snowflake 自动生成
  │
  ├─ [Step 1] HSETNX msg:{msgID} status pending
  │   └─ 返回 0 (已存在)? → return nil (去重，不处理)
  │
  ├─ [Step 2] IsOnline(toUID) → HGETALL user_sessions:{uid}
  │   ├─ 在线: sessions = [{手机}, {平板}, {PC}]
  │   └─ 离线: sessions = []
  │
  ├─ [Step 3] sessions 非空? → directPush(sessions)
  │   │
  │   ├─ 全部成功 (len(failed)==0)
  │   │   → MarkDelivered, directTotal++, return nil
  │   │
  │   ├─ 部分成功 (anyOK && len(failed)>0)
  │   │   → MarkDelivered (已送达的设备标记)
  │   │   → reliableEnqueue(failedSessions) (失败设备走可靠通道)
  │   │   → directTotal++, kafkaTotal++, return nil/error
  │   │
  │   └─ 全部失败 (!anyOK)
  │       → reliableEnqueue(allSessions) [走可靠通道]
  │       → kafkaTotal++, return error/nil
  │
  └─ [Step 4] 离线或全部失败 → reliableEnqueue
      ├─ ZADD offline:{uid} {seq} {msgID} (best-effort)
      └─ Kafka pushTopic (partition=uid)
```

### 核心源码分析

**directPush 的 `anyOK` 设计（关键）：**

```go
// 伪代码还原
func directPush(ctx context.Context, pusher CometPusher, sessions []*Session, op int32, body []byte) (failedSessions []*Session, err error) {
    var lastErr error
    var anyOK bool
    for _, sess := range sessions {
        pushCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
        err := pusher.PushMsg(pushCtx, sess.Server, []string{sess.Key}, op, body)
        cancel()
        if err != nil {
            failedSessions = append(failedSessions, sess)
            lastErr = err
        } else {
            anyOK = true
        }
    }
    // 关键：全失败 vs 部分失败
    if !anyOK && len(failedSessions) > 0 {
        return failedSessions, lastErr  // 触发 full fallback
    }
    return failedSessions, nil  // partial success, nil error
}
```

**`anyOK` 的意义**：区分"全失败"（走完整可靠通道）和"部分失败"（已送达的设备标记 Delivered，失败的进离线队列）。没有这个标记的话，部分失败也会被当作全失败处理，导致成功送达的设备重复收到消息。

---

## 链路 3：ACK 链路

### 时序图

```
客户端                 Comet                   Logic                    Redis              Kafka
  │                      │                       │                        │                  │
  │─OpPushMsgAck───────▶│                       │                        │                  │
  │  (seq, msgID)       │                       │                        │                  │
  │                      │ serveTCP 读取          │                        │                  │
  │                      │ if OpPushMsgAck:       │                        │                  │
  │                      │  s.Operate()           │                        │                  │
  │                      │  (不 echo-back)        │                        │                  │
  │                      │──gRPC Receive────────▶│                        │                  │
  │                      │  (OpPushMsgAck, body)  │                        │                  │
  │                      │                       │ conn.go: Receive()     │                        │
  │                      │                       │ 解析 AckBody           │                        │
  │                      │                       │ {uid, msgID}           │                        │
  │                      │                       │                        │                        │
  │                      │                       │─router.HandleACK()────▶│                        │
  │                      │                       │  ACKHandler             │                        │
  │                      │                       │                        │                        │
  │                      │                       │ [1] UpdateMessageStatus│                        │
  │                      │                       │──HSET msg:{msgID}─────▶│                        │
  │                      │                       │  status acked          │                        │
  │                      │                       │  updated_at {now_ms}   │                        │
  │                      │                       │◀──OK──────────────────│                        │
  │                      │                       │                        │                        │
  │                      │                       │ [2] RemoveFromOffline   │                        │
  │                      │                       │──ZREM offline:{uid}───▶│                        │
  │                      │                       │  {msgID}               │                        │
  │                      │                       │◀──OK (best-effort)────│                        │
  │                      │                       │                        │                        │
  │                      │                       │ [3] Publish ACK Event   │                        │
  │                      │                       │──────────────────────────────────────────────▶│
  │                      │                       │  Kafka ackTopic         │                  │
  │                      │                       │  {msgID,uid,acked}      │                  │
  │                      │                       │                        │                        │
  │                      │                       │ [4] idCache.MarkSeen()  │                        │
  │                      │                       │  写入 64-shard FNV 缓存  │                        │
  │                      │                       │  (内存层去重, 5min TTL)  │                        │
  │                      │                       │                        │                        │
  │                      │                       │ [5] metrics.AckTotal++  │                        │
  │                      │                       │                        │                        │
  │                      │◀──ReceiveReply────────│                        │                        │
```

### ACK 状态机

```
                TrackMessage (HSETNX)
                atomic claim
                     │
                     ▼
              ┌──────────┐
              │ PENDING  │ ← 消息已写入 Redis，等待推送
              └────┬─────┘
                   │
        ┌──────────┼──────────┐
        │ push                 │ retries
        │ success              │ exhausted
        ▼                      ▼
  ┌──────────┐          ┌──────────┐
  │DELIVERED │          │  FAILED  │  ← 终态
  └────┬─────┘          └──────────┘
       │ client ACK
       │ HandleACK()
       ▼
  ┌──────────┐
  │  ACKED   │  ← 终态
  └──────────┘
       │
       ├─ HSET msg:{msgID} status acked
       ├─ ZREM offline:{uid} {msgID}
       ├─ Kafka ackTopic (可观测性)
       └─ idCache.MarkSeen(msgID) (内存去重)
```

### 为什么 ACK 后的操作是 best-effort？

```go
// HandleACK 中：
// Step 1: 核心操作——必须成功
err := a.dao.UpdateMessageStatus(ctx, msgID, MsgStatusAcked)
if err != nil { return err }  // 只有这一步失败才返回错误

// Step 2: 清理离线队列——best-effort
err = a.dao.RemoveFromOfflineQueue(ctx, uid, msgID)
if err != nil { log.Warn(...) }  // 失败只记日志

// Step 3: Kafka ACK 事件——best-effort
if a.pushDAO != nil {
    err = a.pushDAO.PublishACK(ctx, msgID, uid, MsgStatusAcked)
    if err != nil { log.Warn(...) }  // 失败只记日志
}
```

**Why**: 只有"标记消息为 acked"是必须成功的——这决定了消息是否会被重复投递。离线队列清理失败有 TTL 兜底（7 天自动过期），ACK 事件丢失不影响消息可靠性——只是监控看板少一条记录。

---

## 链路 4：Kafka 消费链路

### 时序图

```
Kafka                Worker                    Redis          Comet gRPC         客户端
  │                     │                        │               │                 │
  │──Message──────────▶│                        │               │                 │
  │  pushTopic          │ ConsumeClaim()         │               │                 │
  │  partition={uid}    │                        │               │                 │
  │                     │ processMessage():      │               │                 │
  │                     │  Unmarshal pb.PushMsg  │               │                 │
  │                     │  Type=PUSH             │               │                 │
  │                     │                        │               │                 │
  │                     │ pushKeys():            │               │                 │
  │                     │  将 Proto 编码为 OpRaw  │               │                 │
  │                     │                        │               │                 │
  │                     │ CometClientPool.Push():│               │                 │
  │                     │  atomic round-robin    │               │                 │
  │                     │  选 goroutine channel  │               │                 │
  │                     │──────────────────────────────────────▶│                 │
  │                     │  gRPC PushMsg(keys,    │               │                 │
  │                     │    OpRaw, encodedProto) │               │                 │
  │                     │                        │               │ Bucket → Push  │
  │                     │                        │               │──WriteTCP─────▶│
  │                     │◀──gRPC success─────────│               │                 │
  │                     │                        │               │                 │
  │                     │ MarkMessage → commit   │               │                 │
  │                     │ offset (手动提交)       │               │                 │
  │                     │                        │               │                 │
  │                     │ ACKReporter.Report():  │               │                 │
  │                     │────────────────────────────────────────────────────▶  │
  │                     │  Kafka ackTopic        │               │   (可观测性)   │
  │                     │  {msgID,uid,delivered} │               │                 │
  │                     │                        │               │                 │
  │──Next Message─────▶│                        │               │                 │
  │                     │                        │               │                 │
  │  [错误场景]          │                        │               │                 │
  │                     │ processMessage()       │               │                 │
  │                     │  return error          │               │                 │
  │                     │                        │               │                 │
  │◀──Redeliver────────│                        │               │                 │
  │  (skip MarkMessage) │                        │               │                 │
  │  same offset again  │                        │               │                 │
  │                     │                        │               │                 │
  │                     │ [重复消费 → 幂等保护]    │               │                 │
  │                     │ Worker 调 PushMsg      │               │                 │
  │                     │ → Comet → Channel.Push │               │                 │
  │                     │ → PriorityQueue full?  │               │                 │
  │                     │ → drop (无声丢弃)       │               │                 │
  │                     │ 客户端收到重复消息      │               │                 │
  │                     │ → 发送 ACK              │               │                 │
  │                     │ → Router.HandleACK     │               │                 │
  │                     │ → HSETNX: idempotent   │               │                 │
  │                     │ → idCache.IsDuplicate  │               │                 │
  │                     │ → return nil (已处理)   │               │                 │
```

### offset commit 策略

```go
// kafka/consumer.go consumerGroupHandler.ConsumeClaim:
for msg := range claim.Messages() {
    // ... 构造 mq.Message ...
    
    err := h.handler(session.Context(), mqMsg)  // 调用 Worker.processMessage
    
    if err != nil {
        log.Error("handler error: %v", err)
        continue  // ← 不 MarkMessage，不提交 offset
    }
    
    session.MarkMessage(msg, "")  // ← 手动提交 offset
}
```

**面试要点**：这是 at-least-once 语义的标准实现——处理成功才提交，处理失败不提交等 Kafka 重发。代价是可能重复消费，所以上游（客户端/Worker）需要幂等。

---

## 链路 5：Session 管理链路

### Session 生命周期

```
 ┌─────────────────────────────────────────────────────────┐
 │                     CONNECT                              │
 │  TCP accept → authTCP → Logic.Connect                   │
 │    │                                                     │
 │    ▼                                                     │
 │  session.Create(sess):                                   │
 │    ├─ GetDeviceSession(uid, deviceID) → oldSID           │
 │    │   └─ oldSID 存在? → Kick(oldSID) [原子踢旧连接]      │
 │    │       ├─ DelSession(4 keys Pipeline)                │
 │    │       └─ KickConnection(gRPC → Comet)               │
 │    └─ AddSession(6 keys Pipeline):                       │
 │        ├─ HSET session:{sid} uid,key,device,platform...  │
 │        ├─ HSET user_sessions:{uid} {sid} → "device:plat" │
 │        ├─ SET  device_session:{uid}:{device} → {sid}     │
 │        └─ SET  key_sid:{key} → {sid}                     │
 │                                                          │
 │  OnUserOnline(uid, lastSeq):                             │
 │    └─ ZRANGEBYSCORE offline:{uid} {lastSeq} +inf         │
 │        └─ for each msgID: DirectPush(sessions, OpSync)   │
 │                                                          │
 │                     ACTIVE                               │
 │  客户端每 N 秒发送 OpHeartbeat                            │
 │  Comet.Heartbeat(gRPC) → Logic.Heartbeat:                │
 │    ├─ GetSessionByKey(key) → sid                         │
 │    └─ ExpireSession(sid, uid):                           │
 │        ├─ EXPIRE session:{sid} 30m                       │
 │        └─ EXPIRE user_sessions:{uid} 30m                 │
 │                                                          │
 │                     DISCONNECT                           │
 │  TCP 断开 → serveTCP cleanup:                            │
 │    ├─ b.Del(ch) [从 Bucket 移除]                          │
 │    ├─ ch.Close() [发送 ProtoFinish]                      │
 │    └─ s.Disconnect(gRPC) → Logic.Disconnect:             │
 │        ├─ GetSessionByKey(key) → sid                     │
 │        ├─ GetSession(sid) → deviceID                     │
 │        └─ Kick(sid, uid, deviceID):                      │
 │            ├─ DelSession(4 keys Pipeline)                │
 │            └─ local.Delete(uid) [清缓存]                  │
 │                                                          │
 │                     EXPIRED                              │
 │  30 分钟无心跳 → Redis TTL 自动过期                        │
 │  被动清理（非主动删除）                                    │
 └─────────────────────────────────────────────────────────┘
```

### 五维 Redis 索引设计

| 维度 | Key Pattern | 操作 | 使用场景 |
|------|------------|------|---------|
| **sid** | `session:{sid}` Hash | HSET/EXPIRE/DEL | 查询某 Session 的完整信息 |
| **uid** | `user_sessions:{uid}` Hash | HSET/HDEL | 查用户所有在线设备 → 多端推送 |
| **device** | `device_session:{uid}:{deviceID}` String | SET/GET/DEL | 同设备互踢：新登录 → 查旧 sid |
| **key** | `key_sid:{key}` String | SET/GET/DEL | 心跳：key → sid O(1) 查找 |
| **server** | `session:{sid}` 中 server 字段 | HGET | 踢人：知道 sid → 知道 Comet 节点 |
| (辅助) | `key_{key}` String | SET/GET | 旧版索引，保留兼容 |

**为什么是五维？为什么不用一个 Hash 搞定？**

因为查询模式不同：
- 推送时：uid → 所有 session（`user_sessions:{uid}` 的 HGETALL）
- 心跳时：key → sid（`key_sid:{key}` 的 GET，O(1)）
- 踢人时：uid+device → 旧 sid（`device_session:{uid}:{device}` 的 GET）
- 踢人后：sid → server+key（`session:{sid}` 的 HGETALL）→ 通知 Comet

如果只用一个 Hash，每次查询都要扫全表。这是典型的**用空间换查询效率**——多维索引各司其职，每个查询都是 O(1)。

---

## 链路 6：离线消息同步链路

### 时序图

```
客户端                 Comet                  Logic                   Redis
  │                      │                      │                       │
  │──TCP Connect───────▶│                      │                       │
  │──OpAuth────────────▶│                      │                       │
  │                      │──gRPC Connect──────▶│                       │
  │                      │                      │ 认证通过               │
  │                      │                      │                       │
  │                      │                      │ go syncSvc.OnUserOnline(uid, lastSeq)
  │                      │                      │  (异步 goroutine)      │
  │                      │                      │                       │
  │                      │                      │─GetOfflineQueue()────▶│
  │                      │                      │  ZRANGEBYSCORE         │
  │                      │                      │  offline:{uid}         │
  │                      │                      │  {lastSeq} +inf        │
  │                      │                      │  LIMIT 100             │
  │                      │                      │◀──[msgID1, msgID2,...]─│
  │                      │                      │                       │
  │                      │                      │ for each msgID:        │
  │                      │                      │─GetMessageStatus()────▶│
  │                      │                      │  HGETALL msg:{msgID}   │
  │                      │                      │◀──{status,from,to,     │
  │                      │                      │    op,body(base64)}────│
  │                      │                      │                       │
  │                      │                      │ 跳过 status=acked       │
  │                      │                      │ 构建 SyncReplyBody     │
  │                      │                      │ 序列化为二进制          │
  │                      │                      │                       │
  │                      │                      │─DirectPush()──────────│
  │                      │◀──gRPC PushMsg──────│  (走 Comet gRPC)       │
  │◀──OpSyncReply───────│                      │                       │
  │  (离线消息列表)       │                      │                       │
  │                      │                      │                       │
  │──OpPushMsgAck──────▶│                      │                       │
  │                      │──gRPC Receive──────▶│                       │
  │                      │                      │─HandleACK()──────────▶│
  │                      │                      │  ZREM offline:{uid}    │
  │                      │                      │  {msgID} ← 清理        │
```

### 设计要点

**为什么用 ZSET 而不是 List？**
- ZSET 用 seq（用户级递增序列号）做 score，支持"拉取 seq > N 的所有消息"
- List 只能按插入顺序拉取，不支持范围查询
- 用户断线重连后，可以用 `ZRANGEBYSCORE offline:{uid} {lastSeq} +inf` 精确拉取遗漏的消息

**为什么 LIMIT 100？**
- 防止离线太久（如 7 天未登录）的用户上线时拉取巨量消息
- 100 条一批，客户端 ACK 一批后再拉下一批（分页拉取）
- `GetOfflineMessages` 返回 `HasMore` 标志，客户端据此决定是否继续拉

---

## 链路 7：多端同步/同设备互踢链路

### 时序图

```
[场景：用户先在手机上登录，然后在平板上登录同一账号同一 deviceID]

手机(旧连接)        平板(新连接)         Comet                Logic               Redis
  │                    │                  │                    │                   │
  │ [已连接]            │                  │                    │                   │
  │ Mid=123            │                  │                    │                   │
  │ Key=conn_abc       │                  │                    │                   │
  │ DeviceID=DEV_A     │                  │                    │                   │
  │                    │                  │                    │                   │
  │                    │─TCP Connect────▶│                    │                   │
  │                    │─OpAuth─────────▶│                    │                   │
  │                    │  (uid=123,       │                    │                   │
  │                    │   device=DEV_A)  │                    │                   │
  │                    │                  │─gRPC Connect─────▶│                   │
  │                    │                  │                    │                   │
  │                    │                  │                    │ 1. GetDeviceSession│
  │                    │                  │                    │──GET device_session│
  │                    │                  │                    │  :123:DEV_A──────▶│
  │                    │                  │                    │◀──oldSID="sid_old"─│
  │                    │                  │                    │  (发现旧连接!)      │
  │                    │                  │                    │                   │
  │                    │                  │                    │ 2. Kick(sid_old)  │
  │                    │                  │                    │──GetSession(sid)─▶│
  │                    │                  │                    │◀──{server:"comet1",│
  │                    │                  │                    │    key:"conn_abc"} │
  │                    │                  │                    │                   │
  │                    │                  │◀──gRPC KickConnect │                   │
  │                    │                  │   (server=comet1,  │                   │
  │                    │                  │    key=conn_abc)    │                   │
  │                    │                  │                    │                   │
  │                    │                  │ Bucket("conn_abc") │                   │
  │                    │                  │ → Channel          │                   │
  │                    │                  │ → Channel.Close()  │                   │
  │                    │                  │ → ProtoFinish      │                   │
  │                    │                  │                    │                   │
  │◀──TCP Close────────│                  │                    │                   │
  │  (收到 Kick, 连接断开)│                 │                    │                   │
  │                    │                  │                    │                   │
  │                    │                  │                    │ 3. DelSession     │
  │                    │                  │                    │   Pipeline 删除    │
  │                    │                  │                    │   {session,user,   │
  │                    │                  │                    │    device,key_sid} │
  │                    │                  │                    │                   │
  │                    │                  │                    │ 4. AddSession     │
  │                    │                  │                    │   (新平板 Session) │
  │                    │                  │                    │──Pipeline 创建───▶│
  │                    │                  │                    │◀──OK──────────────│
  │                    │                  │                    │                   │
  │                    │◀──OpAuthReply───│◀──ConnectReply────│                   │
  │                    │  连接建立         │                    │                   │
```

### 原子性问题

Kick 操作分为三步，不是原子的：
1. `GetSession(sid)` — 读 session 信息
2. `DelSession()` — Pipeline 删 4 个 Redis key
3. `KickConnection()` — gRPC 通知 Comet

**竞态窗口**：在步骤 1 和步骤 2 之间，如果旧连接的 TTL 刚好过期，Redis 自动删除了 key，`DelSession` 会部分失败。但这在实践中是可接受的——因为 TTL 是 30 分钟，而代码执行间隔是微秒级。即使 `DelSession` 失败，`KickConnection` 仍然会执行，旧连接仍然会被关闭。

**为什么不用 Redis MULTI/EXEC 事务？**
因为步骤 1 的 GET 结果（server 和 key）需要在步骤 3 的 gRPC 调用中使用。Redis 事务无法包含网络 I/O。如果非要原子化，需要用 Lua 脚本——但当前实现已经够用。

---

## 链路 8：房间消息链路

### 时序图

```
Logic               Kafka            Worker                    Comet              客户端(房间内)
  │                    │                │                        │                    │
  │─RouteByRoom()────▶│                │                        │                    │
  │  pushTopic Room    │                │                        │                    │
  │                    │──Message─────▶│                        │                    │
  │                    │  partition:    │                        │                    │
  │                    │  roomKey       │ processMessage():      │                    │
  │                    │                │  Type=ROOM             │                    │
  │                    │                │                        │                    │
  │                    │                │ getRoom(roomID)        │                    │
  │                    │                │  → RoomAggregator      │                    │
  │                    │                │                        │                    │
  │                    │                │ r.Push(op, body)       │                    │
  │                    │                │  → proto channel       │                    │
  │                    │                │  (非阻塞,满则丢弃)      │                    │
  │                    │                │                        │                    │
  │                    │──Message─────▶│                        │                    │
  │                    │──Message─────▶│                        │                    │
  │                    │  ... 积攒 20 条 或 1 秒 ...             │                    │
  │                    │                │                        │                    │
  │                    │                │ pushproc(): flush      │                    │
  │                    │                │  1. writeWAL(data)     │                    │
  │                    │                │     └─ f.Write(len+data)│                   │
  │                    │                │     └─ f.Sync() (fsync)│                    │
  │                    │                │  2. broadcastRoomRaw() │                    │
  │                    │                │──────────────────────▶│                    │
  │                    │                │  gRPC BroadcastRoom    │                    │
  │                    │                │  (所有 Comet 节点)      │                    │
  │                    │                │                        │──BroadcastRoom───▶│
  │                    │                │                        │  for each bucket: │
  │                    │                │                        │   room.Push()     │
  │                    │                │                        │   channel.Push()  │
  │                    │                │                        │   Signal()        │
  │                    │                │                        │                    │──WriteTCP──▶│
  │                    │                │  3. cleanWAL()         │                    │   用户收到
  │                    │                │     └─ f.Truncate(0)   │                    │   房间消息
```

### WAL 设计

**为什么需要 WAL？**
RoomAggregator 积攒 20 条消息在内存中。如果 Worker 进程在这 20 条 flush 之前崩溃，这批次消息就会永久丢失——Kafka offset 已经提交了（之前的消息都成功处理了），但这 20 条还在内存 buffer 里。

**WAL 流程：**
```
pushproc 循环:
  buf 收集消息:
    每条消息 → buf.Write(proto) → n++

  触发 flush (n>=20 或 1秒超时):
    data = buf.Bytes()
    writeWAL(data)          ← [1] 先写日志 (fsync 确保落盘)
    broadcastRoomRawBytes() ← [2] 再发 gRPC
    cleanWAL()              ← [3] 成功后清理日志 (truncate)

  启动时恢复:
    if WAL 文件存在且非空:
        broadcastRoomRawBytes(walData)  ← 重放上次未发送的批次
    os.OpenFile(wal, O_TRUNC)           ← 清空 WAL 准备新写入
```

**为什么不用 Kafka 的 idempotent producer？**
Kafka idempotent producer 解决的是 producer 到 broker 的重复，不是 consumer 处理中的崩溃。WAL 解决的是 consumer 侧的问题——消息已经从 Kafka 消费了（offset 要提交了），但还没成功发给 Comet。

---

## 链路 9：消息重试链路

### 整体流程

```
Kafka Redeliver → Worker.processMessage → gRPC PushMsg → Comet → 客户端
                                                     │
                                                     ├─ 重试成功 → 客户端 ACK → 幂等去重通过
                                                     │              └─ 客户端已经收到过 → 去重返回 nil
                                                     │              └─ 客户端没收到过 → 正常下发给用户
                                                     │
                                                     └─ 重试失败 → continue (不提交 offset)
                                                                    → Kafka 再次 redeliver
                                                                    → 无限循环 (直到 DLQ 介入)
```

### 幂等去重两层设计（重试链路的核心保障）

```
Worker 重试 → Comet Push → 客户端收到重复消息 → 客户端发 ACK
                                                       │
                                                       ▼
                                              Router.HandleACK()
                                                       │
                                              ┌────────▼────────┐
                                              │ Layer 1: Memory │
                                              │ idCache.IsDuplicate(msgID)
                                              │ FNV-64a(msgID) % 64
                                              │ shard.seen[hash]
                                              │ ├─ 找到且未过期 → DUP!
                                              │ └─ 未找到 → 查 Layer 2
                                              └────────┬────────┘
                                                       │
                                              ┌────────▼────────┐
                                              │ Layer 2: Redis  │
                                              │ HGETALL msg:{msgID}
                                              │ status == "acked"
                                              │ ├─ 是 → DUP!
                                              │ └─ 否 → NOT DUP
                                              └─────────────────┘
```

**为什么重试不会导致无限重复推送？**
1. 客户端收到消息后发 ACK → status 变 acked
2. Worker 重试 → Comet → 客户端再收到 → 再发 ACK
3. HandleACK 中：`idCache.IsDuplicate(msgID)` → **内存层命中** → return nil
4. 消息不会再次展示给用户（客户端本地去重）

---

## 链路 10：降级链路

### 双通道决策全景

```
RouteByUser(msgID, uid, op, body, seq)
  │
  │ [门控 1] HSETNX 去重
  │   └─ 已处理 → 直接返回 (不降级)
  │
  │ [门控 2] IsOnline
  │   ├─ 在线 → 尝试 gRPC 直推
  │   │   ├─ 全成功 → DONE (不降级)
  │   │   ├─ 部分成功 → 失败设备降级到 Kafka+ZSET
  │   │   └─ 全失败 → 全部降级到 Kafka+ZSET
  │   │
  │   └─ 离线 → 全部降级到 Kafka+ZSET
  │
  ▼
[降级路径] reliableEnqueue
  ├─ ZADD offline:{uid} {seq} {msgID}   ← [持久化] 离线队列
  └─ Kafka pushTopic (partition=uid)    ← [异步] Worker 补充投递

降级后的补充投递:
  Worker.Consume → processMessage → pushKeys
    ├─ keys != ["uid:xxx"] → 正常 gRPC Push → Comet → 客户端
    └─ keys == ["uid:xxx"] → 占位符，用户在离线队列中
       → 客户端上线 → OnUserOnline → ZRANGEBYSCORE → 拉取
```

### 降级策略的关键决策

**Q: 为什么直推失败不重试而是直接降级？**

A: 直推用的是 gRPC，有 500ms per-session 的超时。如果 500ms 内发不出去，说明这个连接大概率有问题（网络抖动、Comet 节点故障、客户端断连）。此时重试直推意义不大——再等 500ms 大概率也失败。不如立即写 Kafka，让异步通道在连接恢复后补推。

**Q: 部分成功时为什么直接返回成功（nil error）？**

A: 部分成功 = 至少一个设备收到了。对业务方来说，"已发送"就是成功。失败设备的兜底是系统内部的事，不应该让上游感知。

---

> **第三阶段总结（面试记忆版）**：项目有 10 条核心链路。最核心的是 Push 链路——RouteByUser 函数是整个系统的"灵魂"，它实现了先 gRPC 直推、失败自动降级 Kafka 的双通道逻辑，还内建了 HSETNX 去重和 anyOK 的部分成功处理。ACK 链路保证了"推到≠结束"，客户端确认后才清理离线队列。Session 用了五维 Redis 索引，解决了不同场景下的 O(1) 查询。

---

# 第四阶段：源码级分析

---

## 模块 1：Router 路由引擎（★★★ 项目核心）

### 模块职责
消息投递策略的执行层。决定一条消息走快速通道（gRPC 直推）还是可靠通道（Kafka + Redis 离线队列），以及去重、ACK 状态追踪。

### 核心结构体

**DispatchEngine**（[router.go](internal/router/router.go)）：
```go
type DispatchEngine struct {
    producer    mq.Producer           // Kafka/Redis MQ 生产者（可靠通道）
    broadcaster DirectBroadcaster     // 直接 gRPC 广播（房间/全服降级）
    dao         dao.PushDAO           // 旧版 Kafka Push DAO（兼容）
    msgDAO      dao.MessageDAO        // 消息状态/离线队列 Redis 操作
    sessMgr     *service.SessionManager // 在线状态查询
    ackHandler  *ACKHandler           // ACK 处理器 + 状态追踪
    pusher      CometPusher           // 直接 gRPC 推送（快速通道）
    idGen       IDGenerator           // Snowflake ID 生成器
    directTotal atomic.Int64          // 直推计数（监控用）
    kafkaTotal  atomic.Int64          // Kafka 计数（监控用）
}
```

**ACKHandler**（[ack.go](internal/router/ack.go)）：
```go
type ACKHandler struct {
    dao     dao.MessageDAO       // Redis 消息状态持久化
    pushDAO dao.PushDAO          // Kafka ACK 事件发布
    idCache *IdempotencyChecker  // 共享的内存幂等缓存
}
```

**IdempotencyChecker**（[idempotency.go](internal/router/idempotency.go)）：
```go
type IdempotencyChecker struct {
    shards [64]idemShard        // 64 个独立分片（固定数组，不分配堆）
    msgDAO dao.MessageDAO        // Redis 第二层回查
}

type idemShard struct {
    mu   sync.Mutex              // 每分片独立锁
    seen map[uint64]int64        // FNV-64a hash → 过期时间（Unix 纳秒）
}
```

### 核心函数

| 函数 | 文件 | 职责 |
|------|------|------|
| `RouteByUser` | dispatch.go | 双通道决策核心：HSETNX 去重 → 在线检查 → directPush 或 reliableEnqueue |
| `directPush` | dispatch.go | gRPC 直推所有 session，500ms 超时，区分全失败/部分失败 |
| `reliableEnqueue` | dispatch.go | 可靠通道：ZADD 离线队列 + Kafka 入队 |
| `HandleACK` | ack.go | ACK 处理：UpdateMessageStatus → RemoveFromOfflineQueue → PublishACK → MarkSeen |
| `TrackMessage` | ack.go | 消息原子抢占：HSETNX Redis |
| `IsDuplicate` | idempotency.go | 两层去重：内存 FNV hash + Redis HGETALL |
| `MarkSeen` | idempotency.go | 写入内存去重缓存，触发 eviction 检查 |

### 并发设计

- `directTotal` / `kafkaTotal`：`atomic.Int64` 无锁计数器，用于 Prometheus 暴露直推/Kafka 投递比
- `IdempotencyChecker`：64 个独立 `sync.Mutex`，按 `FNV(msgID) % 64` 路由，锁粒度 1/64
- `TrackMessage`：依赖 Redis `HSETNX` 做分布式互斥，无需本地锁
- `directPush`：每 session 独立 `context.WithTimeout(500ms)`，一 session 超时不阻塞其他

### 性能优化

**内存去重（第一层）为什么快？**
- FNV-64a hash：XOR + 乘法，无加密开销
- 64 路分片：每分片独立锁，高并发下锁竞争概率 < 1/64
- 5 分钟 TTL：自动过期删除，无需后台清理协程
- 固定数组 `[64]idemShard`：栈分配，无 GC 扫描

**为什么两层去重？**
- 第一层（内存）：纳秒级，覆盖 5 分钟内的重复（占比 99%+）
- 第二层（Redis）：覆盖跨服务重启、长时间间隔的重复
- 两层互补，极低开销覆盖全场景

### 可靠性设计

**RouteByUser 的每一步都是容错的：**
1. HSETNX 失败 → 该消息已被其他节点处理，return nil（不算错误）
2. IsOnline 失败 → 保守处理：当作用户离线，走可靠通道（不错过消息）
3. gRPC 部分失败 → 已送达的标记 Delivered，失败的走可靠通道
4. offline ZADD 失败 → 只记日志，不阻塞 Kafka 入队
5. Kafka 入队失败 → 返回 error，上游可重试

### 为什么这样设计

**为什么 Router 是独立模块而不是 Logic 的方法？**
- Logic 已有认证、Session、节点发现、在线统计等职责——已经够大了
- Router 的双通道投递、幂等、ACK 是一套内聚的可靠性逻辑
- 独立后可以：单独测试、单独替换投递策略、单独压测

### Trade-off

**双层去重的代价：**
- 每个 msgID 用内存存储 hash + 过期时间 = 16 字节
- 64 shard × 2000 entry max = 128,000 条消息 = 约 2MB 内存
- 5 分钟 TTL → 每秒最多约 427 条新消息的幂等保护
- Redis 降级（保守策略）：查询失败时 `IsDuplicate` 返回 false → 可能重复投递但不会丢失
- 去重 vs 丢失：**宁可重复投递，不可漏投一条**（这是 IM 通知系统的设计原则）

---

## 模块 2：Comet 连接网关

### 模块职责
管理所有客户端的 TCP/WebSocket 长连接，提供高性能的消息写入通道。

### 核心结构体

**Server**（[server.go](internal/comet/server.go)）：
```go
type Server struct {
    c         *conf.Config        // 全局配置
    round     *Round              // Reader/Writer/Timer 资源池
    buckets   []*Bucket           // CityHash 连接分片（默认 32 个）
    bucketIdx uint32              // 分片总数（用于取模）
    serverID  string              // Comet 节点标识
    rpcClient logic.LogicClient   // 到 Logic 的 gRPC 客户端（连接复用）
}
```

**Bucket**（[bucket.go](internal/comet/bucket.go)）：
```go
type Bucket struct {
    cLock       sync.RWMutex                 // 保护 chs, rooms, ipCnts
    chs         map[string]*Channel          // key → Channel 连接映射
    rooms       map[string]*Room             // roomID → Room 房间映射
    routines    []chan *pb.BroadcastRoomReq  // 房间广播协程（round-robin）
    routinesNum uint64                       // 原子计数器
    ipCnts      map[string]int32             // IP 连接数限制
}
```

**Channel**（[channel.go](internal/comet/channel.go)）：
```go
type Channel struct {
    Room     *Room                  // 当前所在房间（双向链表节点）
    CliProto Ring                   // 客户端协议环形缓冲区（读→写中转）
    signal   *PriorityQueue         // 优先级信号队列（High + Normal）
    Writer   bufio.Writer           // 带缓冲 TCP Writer
    Reader   bufio.Reader           // 带缓冲 TCP Reader
    limiter  *ratelimit.TokenBucket // 每连接消息速率限制
    Mid      int64                  // 用户 ID（认证后设置）
    Key      string                 // 连接唯一标识
    IP       string                 // 客户端 IP
    watchOps map[int32]struct{}     // 订阅的操作类型集合（用于过滤推送）
}
```

### 核心函数

| 函数 | 文件 | 职责 |
|------|------|------|
| `NewServer` | server.go | 创建 N 个 Bucket + Round 资源池 + gRPC 客户端 |
| `AcceptTCP` / `serveTCP` | server_tcp.go | TCP 连接全生命周期：accept → auth → message loop → cleanup |
| `authTCP` | server_tcp.go | 握手认证：8s 超时，读 OpAuth → gRPC Connect → 写入 Bucket |
| `dispatchTCP` | server_tcp.go | 写协程：阻塞 Pop 优先级队列 → WriteTCP → Flush |
| `Bucket.Put` | bucket.go | 连接注册：CityHash 选 Bucket → 写 chs map → 关旧 Channel |
| `Bucket.Channel` | bucket.go | O(1) 连接查找（map 直接索引） |
| `Channel.Push` | channel.go | 非阻塞入队 PriorityQueue，满则丢弃 |
| `Channel.Signal` | channel.go | 发送 ProtoReady 唤醒 dispatch goroutine |
| `PushMsg` (gRPC) | grpc/server.go | gRPC 入口：Bucket(key) → Channel → Push → Signal |

### 并发设计

**CityHash 分片隔离**：
```
Server 有 32 个 Bucket
每个 Bucket 独立 RWMutex

PushMsg(key):
  bucket = buckets[CityHash32(key) % 32]
  bucket.cLock.RLock()
  ch = bucket.chs[key]
  ch.Push(proto)         ← 对 Channel 的操作是无锁的
  bucket.cLock.RUnlock()
```

锁粒度：从"全局一把锁"降到"32 分之一"。在大流量下，全局锁会有 ~3% 的 goroutine 在等锁，32 分片后降到 ~0.1%。

**为什么 CityHash 而不是 CRC32 或简单取模？**
- CityHash 是 Google 为短字符串优化的非加密哈希，分布均匀
- CRC32 对特定输入模式碰撞率高（如 "conn_1", "conn_2"...）
- 简单取模需要先 hash → CityHash 一步完成 hash + 分布
- FNV 也可以，但 CityHash 对短 key 更快

### Ring Buffer 原理（SPSC Lock-Free）

```go
type Ring struct {
    rp   uint64           // 读指针（consumer: dispatch goroutine）
    num  uint64           // 容量（必须为 2 的幂）
    mask uint64           // num - 1（快速取模）
    wp   uint64           // 写指针（producer: reader goroutine）
    data []protocol.Proto // 底层数组
}
```

**核心操作**：
- `Set()`：生产者检查 `wp - rp >= num`（满），分配 slot，返回指针
- `SetAdv()`：`atomic.StoreUint64(&wp, wp+1)` —— **原子写**保证 consumer 可见
- `Get()`：消费者检查 `rp == wp`（空），返回 slot 指针
- `GetAdv()`：`rp++` —— 普通自增（只有 consumer 写 rp）

**为什么是 SPSC（单生产者单消费者）？**

每连接的 Ring Buffer 只有两个 goroutine 操作：
- 生产者 = reader goroutine（只有一个 goroutine 从 TCP 读）
- 消费者 = dispatch goroutine（只有一个 goroutine 写 TCP）

所以不需要 CAS 循环，只需要一个原子 store 做 memory barrier——比 CAS 更高效。

**`wp` 和 `rp` 为什么用 64 位而不回绕？**
- 永不回绕，只比较差值 `wp - rp`
- 即使每秒 100 万条消息，64 位计数器可以用 50 万年
- 避免了回绕判断的复杂性和 ABA 问题

### PriorityQueue 两级设计

```go
type PriorityQueue struct {
    high   chan *protocol.Proto   // 容量 = svrProto（配置值）
    normal chan *protocol.Proto   // 容量 = svrProto × 8
}
```

**为什么需要两级？**
- 控制消息（心跳回复、ACK、Sync、踢人）走 high channel
- 业务消息走 normal channel
- 如果 normal channel 被巨量业务消息塞满，控制消息仍然可以走 high channel 立刻响应
- 否则：客户端发心跳 → 服务器回复被堵在队列里 → 客户端超时 → 断连 → 雪崩

**高优先级操作码：**
```go
func isHighPriority(op int32) bool {
    switch op {
    case OpHeartbeatReply, OpAuthReply, OpPushMsgAck,
         OpSendMsgAck, OpSyncReply, OpKickConnection,
         OpProtoReady, OpProtoFinish:
        return true
    }
    return false
}
```

### Round Buffer 池化原理

**为什么需要池化？**
- TCP 连接需要读写缓冲区（每个 8KB × 2 = 16KB）
- 百万连接 = 16GB 缓冲区
- 如果每次 accept 都分配，close 都释放 → GC 压力巨大
- 池化后：预分配固定数量，连接之间复用

**三层池化：**
```
Round
├─ readers[32]    ← 32 个 Reader 池分片
│   └─ Pool[1024] ← 每分片 1024 个 8KB Buffer
├─ writers[32]    ← 32 个 Writer 池分片
│   └─ Pool[1024] ← 每分片 1024 个 8KB Buffer
└─ timers[32]     ← 32 个 Timer 池分片
    └─ 每分片管理 timerData 复用
```

连接分配：`r = acceptCount++ % 32`，连接被固定绑定到一个分片，该连接的所有读写都使用该分片的池。连接关闭时归还 Buffer。

### goroutine-per-connection 模型

```
每连接 = 2 goroutines

[reader goroutine]                    [dispatch goroutine]
  阻塞读 TCP                 ProtoReady →  阻塞 Pop PriorityQueue
  处理帧                                  Pop 到 ProtoReady
  写入 CliProto Ring                      遍历 CliProto Ring
  SetAdv() (atomic store wp)              Get → WriteTCP → Flush
  Signal() → highChan                     GetAdv()
                                          Pop 到 *Proto (服务端推送)
                                          WriteTCP → Flush
```

### 为什么这样设计

**为什么 reader + dispatch 两个协程而不是一个？**
- 读写解耦：TCP 读可能阻塞，如果读写同协程，该连接的所有出站消息都要等 TCP 可读
- 出站延迟可控：dispatch 协程阻塞在 Pop 上（等待 Push），一旦有消息立即写入
- 实践中：20 万连接的 Comet 节点 = 40 万 goroutine。Go 的 goroutine 栈 2KB 起，40 万 ~= 800MB，在 64GB 机器上完全可行

### Trade-off

**Ring Buffer 满时的行为**：
- `Set()` 发现满 → 返回 error → reader goroutine 丢弃帧
- 意味着：如果 dispatch 协程写 TCP 太慢（网络拥塞），新收到的帧直接丢弃
- 这是有意的设计——宁可在服务端丢帧（客户端会重发），也不要无限排队导致 OOM

---

## 模块 3：Session 管理

### 模块职责
管理客户端的在线状态，提供五维索引，支持多端管理和同设备互踢。

### 核心结构体

```go
type SessionManager struct {
    dao    dao.SessionDAO    // Redis SessionDAO 接口
    local  sync.Map          // uid → []*Session 本地缓存（1 分钟 TTL）
    ttl    time.Duration     // Session Redis TTL
    kicker CometKicker       // Comet 连接踢出接口（可选注入）
}

type Session struct {
    SID       string      // Session UUID
    UID       int64       // 用户 ID
    Key       string      // 连接 key（Comet 定位 Channel）
    DeviceID  string      // 设备唯一标识
    Platform  string      // android/ios/web
    Server    string      // Comet 节点标识
    CreatedAt time.Time
    LastHBAt  time.Time
    cachedAt  time.Time   // 本地缓存时间戳（包外不可见）
}
```

### 五维索引 Redis 设计

```
在线用户 uid=123, 设备=iPhone14_5, platform=ios
在 comet-1 上 key=conn_abc, sid=uuid-xxx

Redis 写入（AddSession Pipeline）：
  1. HSET session:uuid-xxx uid 123 key conn_abc device_id iPhone14_5 platform ios server comet-1
  2. HSET user_sessions:123 uuid-xxx "iPhone14_5:ios"
  3. SET device_session:123:iPhone14_5 uuid-xxx
  4. SET key_sid:conn_abc uuid-xxx
  + EXPIRE on all keys = 30 minutes
```

**每个索引的使用场景与复杂度：**

| 场景 | Redis 操作 | 复杂度 | 调用方 |
|------|-----------|--------|--------|
| 推送消息：uid → 所有 session | `HGETALL user_sessions:123` | O(N) N=设备数 | RouteByUser |
| 心跳续期：key → sid → EXPIRE | `GET key_sid:conn_abc` → `EXPIRE session:sid` | O(1) | Heartbeat |
| 设备互踢：uid+device → 旧 sid | `GET device_session:123:iPhone14_5` | O(1) | Create |
| 踢人通知：sid → server+key | `HGETALL session:uuid-xxx` | O(1) | Kick |
| 用户下线：清理所有索引 | Pipeline: DEL + HDEL + DEL + DEL | O(1) | Disconnect |

### 原子 Kick 实现

```go
func (m *SessionManager) Kick(ctx context.Context, sid string, uid int64, deviceID string) error {
    // Step 1: 读取 Session 信息（需要 server 和 key 用于通知 Comet）
    sess, err := m.GetSession(ctx, sid)

    // Step 2: Pipeline 删除 4 个 Redis key
    m.dao.DelSession(ctx, sid, uid, deviceID, sess.Key)
    //   DEL session:{sid}
    //   HDEL user_sessions:{uid} {sid}
    //   DEL device_session:{uid}:{deviceID}
    //   DEL key_sid:{key}

    // Step 3: 清除本地缓存
    m.local.Delete(uid)

    // Step 4: 通知 Comet 关闭 TCP 连接
    if m.kicker != nil && sess.Server != "" && sess.Key != "" {
        m.kicker.KickConnection(ctx, sess.Server, sess.Key)
    }
    return nil
}
```

### sync.Map 本地缓存

```go
// Read-Aside 模式：
func (m *SessionManager) GetSessions(ctx context.Context, uid int64) ([]*Session, error) {
    // 1. 查本地缓存
    if val, ok := m.local.Load(uid); ok {
        sessions := val.([]*Session)
        if len(sessions) > 0 && time.Since(sessions[0].cachedAt) < time.Minute {
            return sessions, nil  // 缓存命中，不查 Redis
        }
    }
    // 2. 缓存未命中，查 Redis
    sessions, err := m.dao.GetUserSessions(ctx, uid)
    // 3. 回写本地缓存
    m.local.Store(uid, sessions)
    return sessions, nil
}
```

**为什么用 sync.Map 而不是 map + RWMutex？**
- 读多写少：`GetSessions`（每次 Push 都调用）远多于 `Create`/`Kick`（每次连接建立/断开才调用）
- `sync.Map` 在读密集型场景下接近无锁性能
- 1 分钟 TTL 容忍一定程度的脏读

### 为什么这样设计

**为什么 Session 存 Redis 而不是存数据库？**
- 读写频率极高（每次 Push 查在线状态，每次心跳续期）
- Session 是临时状态，丢了可以重建（用户重连）
- Redis 的 TTL 机制完美匹配 Session 过期需求

**为什么 Redis 宕机不会导致全部服务不可用？**
- 本地缓存 1 分钟 TTL：短时间内不依赖 Redis 也能工作
- 心跳间隔 30 分钟：Redis 宕机后，已创建的 Session 在本地缓存有效期内仍可推送
- 新连接无法认证（需要 Redis），但已连接用户不受影响

---

## 模块 4：Worker 投递引擎

### 模块职责
从 Kafka 消费消息，通过 gRPC 连接池投递到 Comet。

### 核心结构体

```go
type DeliveryWorker struct {
    consumer mq.Consumer              // Kafka ConsumerGroup
    comets   *CometClientPool         // 到所有 Comet 的 gRPC 连接池
    reporter *ACKReporter             // 投递结果上报
    rooms    map[string]*RoomAggregator // 房间批处理聚合器
    roomsMu  sync.RWMutex
}

type CometClientPool struct {
    servers     map[string]*cometClient  // hostname → client
    routineSize int                       // 每 Comet 32 个 goroutine
    routineChan int                       // channel buffer 1024
}

type cometClient struct {
    client        comet.CometClient
    pushChan      []chan *comet.PushMsgReq    // 32 个 push 通道
    roomChan      []chan *comet.BroadcastRoomReq // 32 个 room 通道
    broadcastChan chan *comet.BroadcastReq    // 1 个 broadcast 通道
    pushChanNum   uint64                      // atomic round-robin 计数器
    roomChanNum   uint64
}

type RoomAggregator struct {
    id    string              // 房间 ID
    proto chan *protocol.Proto // 消息缓冲 channel（batch × 2 容量）
}
```

### CometClientPool Fan-Out 设计

```
Worker 收到 Kafka 消息
  │
  ▼
CometClientPool.Push(serverID, pushReq)
  │
  ├─ idx = atomic.Add(&pushChanNum, 1) % 32   ← 无锁 round-robin
  ├─ ch = client.pushChan[idx]
  ├─ select { case ch <- pushReq: return nil; default: return ErrFull }
  │
  ▼
process goroutine (32 个 per Comet):
  for {
    select {
    case req := <-pushChan:    client.PushMsg(ctx, req)    ← gRPC 调用
    case req := <-roomChan:    client.BroadcastRoom(ctx, req)
    case req := <-broadcastChan: client.Broadcast(ctx, req)  ← 所有 goroutine 共享
    }
  }
```

**为什么每 Comet 32 个 goroutine？**
- gRPC 调用是同步的（等待 response），1 个 goroutine 发完等 response 时不能发下一条
- 32 个 goroutine = 32 路并发 gRPC 调用
- 太多了浪费 goroutine 内存，太少了吞吐不够

### RoomAggregator WAL 设计

```
pushproc: 积攒消息 → 批量发送 → WAL 保护

WAL 文件: <WALDir>/<roomID>.wal

写入格式: [4 bytes: len][N bytes: protobuf data]
  → write(fd, len_prefix + data)
  → fsync(fd)          ← 确保落盘

清理: ftruncate(fd, 0)  ← 发送成功后截断

恢复(启动时):
  if wal not empty:
    broadcastRoomRawBytes(walData)  ← 重放未发送的批次
  open(wal, O_TRUNC)                 ← 清空
```

**为什么 WAL 不写 Kafka offset 而是写消息本身？**
- 如果写 offset：恢复时需要从 Kafka 重新消费 → 需要 seek offset → 复杂
- 写消息本身：恢复时直接发送 → 简单直接
- WAL 大小有限（最多 20 条消息的 protobuf，约几 KB）

---

## 模块 5：MQ 抽象层

### 接口设计

```go
type Producer interface {
    EnqueueToUser(ctx, uid, msg) error         // 单用户推送
    EnqueueToUsers(ctx, uids, msg) error       // 批量用户推送
    EnqueueToRoom(ctx, roomID, msg) error      // 房间广播
    EnqueueBroadcast(ctx, msg, speed) error    // 全服广播
    EnqueueACK(ctx, msgID, uid, status) error  // ACK 事件
    EnqueueDelayed(ctx, uid, msg, delayMs) error // 延迟消息
    Close() error
}

type Consumer interface {
    Consume(ctx, handler MessageHandler) error  // 阻塞式消费
    Close() error
}
```

### Kafka 实现要点

**分区策略**：
- `EnqueueToUser`：key = `strconv.FormatInt(uid, 10)` → 同一用户的所有消息进入同一分区（保序）
- `EnqueueToRoom`：key = `roomID` → 同一房间的消息进入同一分区
- `EnqueueBroadcast`：key = `speed` → 按速度参数分区

**延迟消息**：
```go
// Producer 端：写入 Kafka Header
msg.Headers["goim_delayed_until"] = strconv.FormatInt(nowMs + delayMs, 10)
saramaMsg.Headers = []sarama.RecordHeader{{Key: "goim_delayed_until", Value: ...}}

// Consumer 端：消费时检查
if deliverAt, ok := msg.Headers["goim_delayed_until"]; ok {
    if now < deliverAt {
        time.Sleep(100 * time.Millisecond)
        continue  // 不提交 offset，Kafka 会重发
    }
}
```

**为什么不直接用 Kafka 的延迟消息？**
- Kafka 本身不支持延迟消息（不像 RocketMQ 有 18 个延迟级别）
- 通过 Header + 轮询实现：收到消息后判断时间，未到时间则 sleep 100ms 让 Kafka 重发
- 简单但不够优雅——理想方案可以用时间轮（如滴滴的 delayqueue）做本地延迟队列

**DLQ（死信队列）**：
```go
type DLQ struct {
    pub   sarama.SyncProducer
    topic string
}

func (d *DLQ) Send(ctx context.Context, msg *mq.Message, reason string) error {
    // 写入 dlq-reason Header
    // 发送到 DLQ topic
}
```

---

## 模块 6：Notify 业务层

### 模块职责
业务网关——将电商业务事件（订单、秒杀、物流）转换为消息推送。

### 核心结构体

```go
type OrderNotifyService struct {
    logicClient  *PushClient         // goim Logic HTTP 客户端
    orders       map[string]*Order   // 内存订单存储（Demo 简化）
    statsCollector *StatsCollector   // ACK 率/延迟统计
}

type PushClient struct {
    logicAddr string                 // Logic HTTP 地址
    httpClient *http.Client
}

type Simulator struct {
    engine     *Engine               // 模拟引擎
    modes      []string              // lifecycle/normal/peak/flash_sale
}
```

### 业务闭环

```
订单创建 → 状态变更 → PushClient.POST /goim/push/mids
  → Logic Router 双通道投递
  → 客户端收到 → ACK
  → Notify Server 统计
     ├─ ACK 率统计：acked / sent × 100%
     ├─ P50/P99 延迟统计：push_time → ack_time
     ├─ 直推/Kafka 投递比例：directTotal / (directTotal + kafkaTotal)
     └─ GET /platform/stats  → JSON dashboard
```

---

## 模块 7：基础设施

### Snowflake ID 生成器

```go
type Snowflake struct {
    mu        sync.Mutex
    epoch     int64    // 起始时间戳（2024-01-01 00:00:00 UTC）
    machineID int64    // 机器 ID（0-1023）
    sequence  int64    // 毫秒内序列号（0-4095）
    lastMs    int64    // 上次生成的时间戳
}

// 64 位结构: [1 bit sign] [41 bits timestamp] [10 bits machine] [12 bits sequence]
func (s *Snowflake) Generate() int64 { ... }
func (s *Snowflake) GenerateString() (string, error) { ... }
```

**为什么是 Snowflake 而不是 UUID？**
- UUID 是 128 位字符串（如 "550e8400-e29b-41d4-a716-446655440000"），做 Redis key 浪费内存
- Snowflake 是 int64，趋势递增，对 Redis/DB 索引友好
- 自带时间戳，可排序，方便按时间范围查询

### Prometheus Metrics

```go
var (
    ConnectionsActive = prometheus.NewGauge(...)     // 活跃连接数
    PushTotal = prometheus.NewCounterVec(...)        // 推送总数 {channel,status}
    PushLatency = prometheus.NewHistogramVec(...)    // 推送延迟 {channel}
    MsgAckTotal = prometheus.NewCounter(...)         // ACK 总数
    RateLimitedTotal = prometheus.NewCounter(...)    // 限流次数
    OfflineQueueSize = prometheus.NewGaugeVec(...)   // 离线队列大小 {uid}
)
```

### OpenTelemetry Tracing

```go
// pkg/tracing/tracing.go
func InitTracerProvider(endpoint, serviceName string) (*sdktrace.TracerProvider, error) {
    // OTLP gRPC Exporter → Jaeger/Tempo/Datadog
    // AlwaysSample sampler
    // 注入 otelgin / otelgrpc middleware
}
```

### Token Bucket Rate Limiter

```go
type RateLimiter struct {
    mu         sync.Mutex
    userLimits map[int64]*tokenBucket    // 每用户桶
    global     *tokenBucket              // 全局桶
}
```

三层限流体系：
1. **全局限流**：`global.Allow()` — 保护整个 Push API
2. **每用户限流**：`userLimits[uid].Allow()` — 防止单用户洪水
3. **每连接限流**：`Channel.limiter.Allow()` — 保护单连接（Comet 层）

---

> **第四阶段总结（面试记忆版）**：源码分析覆盖了 7 个核心模块。Router 的双通道决策树 + 两层去重是项目最大的技术亮点。Comet 的 CityHash 分桶 + RingBuffer + PriorityQueue + Round Pool 四件套让百万连接成为可能。Session 的五维索引解决了多端管理的所有查询场景。Worker 的 Fan-Out Pool + WAL 是 Kafka 消费侧的工程化实践。

---

# 第五阶段：大厂面试训练

---

## 面试官设定

你现在面对的是**阿里/字节/腾讯的 Go 高级/资深面试官**。他们：
- 读过你的简历，知道项目名称和技术栈
- 会从浅到深追问，测试你理解的深度
- 特别关注"Why"——为什么这样设计
- 会挑战你的 Trade-off——有没有更好的方案
- 会深挖故障场景——如果 X 挂了怎么办

---

## 维度 1：项目介绍

### Q1（浅）：简单介绍一下你的项目。

**参考答案（30 秒版）：**

> GoToIM 是一个基于 goim 二次开发的高性能消息投递中台。核心解决长连接断开导致的消息丢失问题，通过双通道自适应投递——gRPC 实时直推加 Kafka/Redis 离线兜底——保证消息 100% 到达。架构上分成四层：Comet 管连接、Logic 管业务、Router 管路由、Worker 管异步投递。我在原版 goim 基础上新增了 4000+ 行代码，核心改造方向是"可靠性"。

### Q2（深）：你说是"消息投递中台"而不是"IM"，具体区别是什么？

**关键回答点：**
1. 可靠性要求不同：IM 丢了消息用户可以重发（"你刚才说什么？"），中台丢了订单通知意味着用户不知道支付成功了
2. 消息语义不同：IM 是聊天文本，中台是结构化事件（订单状态码、物流单号）
3. ACK 追踪：IM 有"已读"就够了，中台需要 Pending → Delivered → Acked 的完整状态追踪
4. 可观测性不同：中台需要直推/Kafka 比例、P99 延迟、ACK 率这些运营指标

### Q3（源码）：相比原版 goim，你具体改了什么代码？

**回答结构：**
- **新增模块**（4 个）：`internal/router/`（路由引擎）、`internal/worker/`（消费者重写）、`internal/mq/`（MQ 抽象）、`internal/notify/`（业务 Demo）
- **新增工具**（4 个）：Snowflake、Metrics、Tracing、RateLimit
- **增强模块**：Logic 的 Session 从 1 维变 5 维、Comet 增加 PriorityQueue 和连接限速、Logic API 增加 /push/offline 和 /sync 和 /metrics
- **新增操作码**（5 个）：OpSendMsgAck(18)、OpPushMsgAck(19)、OpSyncReq(20)、OpSyncReply(21)、OpKickConnection(22)

**追问："你说 Worker 是重写的，原版 job 做了什么？你改了什么？"**

原版 job 只是从 Kafka 消费一条消息 → 调 gRPC → 结束。我改成了：
- CometClientPool fan-out 并发投递（原版单 goroutine 串行）
- RoomAggregator 批处理 + WAL（原版一条一条发）
- ACKReporter 投递结果上报（原版没有反馈）
- MQ 抽象接口（原版硬编码 sarama）

---

## 维度 2：架构设计

### Q1（浅）：说说你的四层架构。

- Comet（接入层）：TCP/WebSocket 连接管理，CityHash 分桶，Ring Buffer
- Logic（业务层）：认证、Session、负载均衡、节点发现
- Router（路由层）：双通道投递、幂等、ACK
- Worker（消费层）：Kafka 消费、gRPC 投递

### Q2（深）：为什么 Router 要独立为层？放在 Logic 里不行吗？

放在 Logic 里的问题：
1. Logic 已经有了认证、Session、节点发现、在线统计、HTTP API、gRPC Server——再加双通道逻辑，会成为"上帝对象"
2. Router 和 Logic 的故障域不同：Logic 的 Session 挂了应该影响新连接建立，但不应该影响已入队的消息投递
3. Router 可以独立压测、独立替换投递策略

分离的代价：
- 多了一层函数调用（但还在同一进程，零网络开销）
- 需要显式注入依赖（Producer、CometPusher、SessionManager）

### Q3（源码级追问）：Router 的依赖注入是怎么做的？

```go
// logic.go 中：
router := router.NewDispatchEngine(dao, dao, sessionMgr, cometPusher)
router.SetIDGenerator(snowflakeGen)
router.SetMQProducer(mqProducer)
router.SetBroadcastFallback(cometPusher)
```

采用了"构造函数 + Setter"模式，因为：
- 不是所有依赖都在初始化时可用（MQ Producer 可能后创建）
- 支持可选依赖（没有 IDGenerator 也能工作，只是不自动生成 msgID）
- 支持运行中替换（测试时可以注入 mock）

---

## 维度 3：双通道投递

### Q1（浅）：为什么需要双通道？

gRPC 直推延迟低（<50ms），但依赖长连接。客户端断线就丢了。所以需要可靠通道兜底——Kafka 做异步解耦，Redis ZSET 做离线存储。两条通道不是二选一，是"先快后慢"。

### Q2（深）：为什么不是直接用 Kafka 做所有推送？

两个原因：
1. **延迟**：Kafka 端到端延迟在生产环境通常是 100ms-2s（producer 批量 + broker 复制 + consumer 拉取）。秒杀通知、订单支付通知要求 < 200ms。gRPC 直推延迟 < 50ms。
2. **离线消息的结构化查询**：Kafka 是流式数据，不支持"查找用户 123 在 seq > 50 之后的所有离线消息"。Redis ZSET 天然支持这种范围查询。

### Q3（深）：为什么 Redis + Kafka 组合而不是二选一？

- **Redis ZSET**：结构化存储，支持精确的范围查询（`ZRANGEBYSCORE`），有 TTL 自动过期，但容量受内存限制
- **Kafka**：高吞吐（百万条/秒），持久化到磁盘，支持消费者组负载均衡，但不支持按条件查询

两者是**互补关系**，不是替代关系：
- Redis = 离线消息的"索引 + 短期存储"（7 天 TTL）
- Kafka = 消息的"传输管道 + 长期存储"

### Q4（源码级追问）：RouteByUser 的分支逻辑是怎样的？如果是你自己写的，讲一下代码逻辑。

```
RouteByUser(msgID, toUID, op, body, seq):
  Step 0: msgID 空 → Snowflake 生成
  Step 1: TrackMessage → HSETNX msg:{msgID} status pending
          如果已存在 → return nil（幂等跳过）
  Step 2: IsOnline(toUID) → HGETALL user_sessions:{uid}
  Step 3a: 在线 + sessions 非空 → directPush(sessions, 500ms timeout)
           全部成功 → MarkDelivered, return nil
           部分成功 → MarkDelivered + reliableEnqueue(failedSessions)
           全部失败 → reliableEnqueue(allSessions)
  Step 3b: 离线 / all-direct-failed → reliableEnqueue
           ZADD offline:{uid} + Kafka pushTopic
```

### Q5（面试官深挖）：directPush 中 anyOK 这个标记是干什么的？如果没有会怎样？

`anyOK` 用于区分"部分成功"和"全部失败"：

- 全部失败：`!anyOK && len(failed) > 0` → 返回 error → 触发 full fallback（所有 session 走可靠通道）
- 部分成功：`anyOK && len(failed) > 0` → 返回 nil error → failedSessions 走可靠通道 → 但不触发 full fallback

如果没有 `anyOK`：
- 3 个 session 中 2 个成功 1 个失败 → `failedSessions` 非空 → 如果不判断 anyOK，可能被当做全失败处理
- 后果：2 个已送达的设备会被重复推送（走 Kafka 再推一次）

### Q6（面试官深挖）：HSETNX 用 Redis 做分布式锁去重，Redis 挂了怎么办？

分两层回答：

**Redis 挂了的直接影响**：
1. IsOnline 返回空（查不到 session）→ 用户都被当作离线 → 全部走可靠通道（Kafka + ZSET）
2. TrackMessage（HSETNX）失败 → Router 返回 error → 上游 Logic 返回 500 → 业务方重试

**不会丢消息**：因为保守策略——拿不到状态就当作用户离线，走可靠通道兜底。

**Redis 恢复后**：
- Worker 从 Kafka 消费积压消息 → 正常投递
- 客户端上线 → 拉取离线队列 → 消息补推到位
- 用户可能收到重复消息 → 客户端幂等去重

### Q7（最高难度追问）：假设你上线后发现有 1% 的消息被重复投递了，你怎么排查？

排查思路：
1. 看直推/Kafka 比例：如果 kafkaTotal/directTotal 比例异常高（>30%），说明直推失败率高，可能有大量 partial success 触发的 reliable retry
2. 看 ACK 延迟分布：如果 P99 延迟很高，可能是 ACK 没及时回来，导致消息状态一直 pending，触发了 Worker 的重试
3. 看 idempotency checker 的命中率：如果内存层命中率过低（<90%），说明 5 分钟 TTL 不够，或者 msgID 不够随机
4. 看 Comet gRPC 的错误日志：是否有大量 timeout，说明 Comet 节点有问题
5. 看 Worker 的 consume lag：如果 lag 很大，Worker 处理慢 → offset 提交慢 → Kafka rebalance → 重复消费

---

## 维度 4：幂等与去重

### Q1（浅）：为什么要幂等？

Kafka 是 at-least-once 语义。消息可能被 Consumer 重复消费（处理成功但 offset 没提交、rebalance、Worker 崩溃）。如果客户端收到两条"订单已支付"通知，用户体验很差。

### Q2（深）：你的两层去重是怎么设计的？

第一层（内存）：64-shard FNV hash → `map[hash]expiry`，5 分钟 TTL，纳秒级查询。
第二层（Redis）：`HGETALL msg:{msgID}` → 检查 status 是否已 acked/delivered。

两层互补：内存覆盖高频短期重复（99%+），Redis 覆盖长期和跨节点重复。

### Q3（源码级追问）：为什么选 FNV 而不是 MD5 或 murmur3？

- FNV-64a：XOR + 乘法，无表查找，适合短字符串
- MD5：128 位输出，但需要 crypto 库，慢
- murmur3：分布更好但计算复杂

选择 FNV 的核心原因是"够快就够了"：
- msgID 是 Snowflake 生成的 int64 字符串（"1734567890123456789"），长度固定 19 字符
- 对高度随机的输入（时间戳 + 机器 ID + 序列号），FNV 的碰撞率极低
- 即使碰撞（误判为重复），也只是少发一条消息 → 依赖 Redis 第二层纠正
- 反过来（漏判，把重复的当成新的）不会发生——FNV 是确定性函数

### Q4（深）：64 个 shard，每个最多 2000 条，总共 128,000 条。如果瞬时涌入 20 万条消息怎么办？

- 老的 entry 会被**被动驱逐**（`evictShardLocked`）：
  1. 先扫一遍删掉所有过期的（5 分钟以上）
  2. 还不够 → 删掉 oldest-half（按过期时间排序，删最早到期的 50%）
- 驱逐后，不在内存缓存中的消息 ID 会 fallback 到 Redis 查询
- 性能影响：Redis 查询增加 ~1ms 延迟，但不会丢失去重能力
- 所以 128,000 是"极速去重"的上限，超过后性能有损但不丢功能

### Q5（最高难度追问）：如果我要把系统部署到 100 个 Logic 节点，现在的去重方案有什么问题？

当前方案：每个 Logic 节点有自己的 64-shard 内存去重缓存。

问题：
- 同一 msgID 可能被节点 A 处理（内存标记为 seen），然后被节点 B 再次处理（节点 B 的内存里没有 → 查 Redis → 也能去重）
- Redis 成为多节点的唯一去重点 → Redis 压力 = 节点数 × 单节点流量

优化方案：
- 把 msgID 路由到特定 Logic 节点（如 `msgID hash % N` 决定由哪个 Logic 处理）
- 这样每个 msgID 只被一个节点处理，内存去重就足够了
- 但代价是丧失了"任意节点可处理"的灵活性

---

## 维度 5：Session 管理

### Q1（浅）：Session 怎么管理的？

用 Redis 做持久化，五维索引：sid（会话 ID）、uid（用户 ID）、device（设备 ID）、key（连接 key）、server（Comet 节点）。辅助本地 sync.Map 做 1 分钟缓存。

### Q2（深）：五维索引的意义是什么？为什么不一个 Hash 搞定？

不同场景需要不同的查询入口：
- 推送时：uid → 所有 session（`HGETALL user_sessions:{uid}`）
- 心跳时：key → sid（`GET key_sid:{key}`）
- 设备互踢：uid+device → 旧 sid（`GET device_session:{uid}:{deviceID}`）

如果只用一个 Hash `session:{uid}` 存所有信息，心跳时无法用 key 快速定位到具体是哪个 session——需要把所有 session 的 key 都扫一遍。

**本质是用空间换 O(1) 查询**。

### Q3（源码级追问）：同设备互踢的原子性怎么保证的？

不保证完全原子性。流程是：
1. GET device_session:{uid}:{deviceID} → oldSID
2. GET session:oldSID → server, key
3. Pipeline DEL session + HDEL user_sessions + DEL device_session + DEL key_sid
4. gRPC KickConnection → Comet

步骤 1-2 和步骤 3 之间有**竞态窗口**：如果旧 session 的 TTL 刚好在这之间过期，Pipeline DEL 可能部分失败。

但这不是大问题：
- 即使 Redis DEL 失败，步骤 4 的 gRPC KickConnection 仍然会执行 → 旧连接仍然会被踢
- 窗口极小（微秒级）
- 如果一定要原子化，可以用 Lua 脚本把 1-3 合并——但当前实现已经够用

### Q4（深）：为什么用 Redis 而不是 etcd 或数据库？

- etcd 强一致性但写入性能有限（Raft 共识），不适合高频心跳续期
- 数据库可以做但需要处理连接池、索引维护
- Redis 的 TTL 机制天然匹配 Session 过期场景，不需要写定时任务清理
- 单机 Redis 可以轻松处理 10 万+ QPS 的 GET/SET/EXPIRE

---

## 维度 6：高并发与性能

### Q1（浅）：怎么支持百万连接？

- CityHash 32 路分桶 → 锁粒度降到 1/32
- 每连接独立读写协程（reader + dispatcher）→ 无共享状态
- Ring Buffer（SPSC lock-free）→ 消息中转无锁
- Round Buffer Pool → 预分配，减少 GC
- PriorityQueue 两级 → 控制消息不被业务消息阻塞

### Q2（深）：CityHash 为什么适合做连接分片？

- 对短字符串（连接 key）优化，分布均匀
- 计算速度快（比 MD5/SHA 快 10-100 倍）
- 非加密哈希，不用考虑安全性（连接 key 是本服务内部生成的）

其他选项：
- CRC32：对特定模式碰撞率高（"conn_1", "conn_2", "conn_3"... 哈希后可能扎堆）
- 简单取模：需要先 hash，不如 CityHash 一步到位

### Q3（源码级追问）：Ring Buffer 为什么是 SPSC？怎么保证无锁？

SPSC = Single Producer Single Consumer。

每连接的 Ring Buffer 只有两个 goroutine 操作：
- Producer = reader goroutine（从 TCP 读，写 CliProto）
- Consumer = dispatch goroutine（从 CliProto 读，写 TCP）

不需要 CAS 循环，因为：
- 只有一个 Producer 写 `wp`，一个 Consumer 读 `wp`
- Producer 用 `atomic.StoreUint64` 更新 `wp` → 提供 memory barrier
- Consumer 读 `wp` 时通过原子操作保证看到最新值

如果用 MPMC（多生产者多消费者），需要 CAS 循环（compare-and-swap loop），高并发下 CAS 失败重试会导致 CPU 空转。

### Q4（深）：PriorityQueue 的两级设计解决了什么问题？没有会怎样？

没有两级优先级的场景：假设一个直播间有 10 万观众，服务器广播了一条消息 → 10 万条消息涌入 normal channel → 某观众发了一条心跳 → 心跳回复被排在 10 万条消息后面 → 客户端心跳超时 → 断连 → 重连 → 再次涌入 10 万条 → 再次超时 → 雪崩。

**有了两级**：心跳回复走 high channel → 不管 normal channel 有多少消息，心跳回复都能立刻发送。

### Q5（最高难度追问）：如果 PriorityQueue 的 high channel 也满了怎么办？

high channel 容量 = `svrProto`（配置值，如 1024）。正常情况下，high channel 只放控制消息（心跳、ACK、Sync、踢人），这些消息数量远小于业务消息，不会满。

如果满了，`Push` 方法返回 `ErrSignalFullMsgDropped`——消息被丢弃。

但控制消息（如心跳回复）被丢弃的后果：
- 客户端收不到心跳回复 → 认为服务器死了 → 断连重连
- 但重连后，客户端的 Ring Buffer 也会清空 → 之前积压的消息全部丢失

所以 PriorityQueue 是**最后一道防线**，前面应该有：
1. 连接限速（`Channel.limiter`）→ 控制进入速率
2. Room 广播限速（`BroadcastReq.Speed`）→ 控制广播速率
3. 如果还是满了 → 宁可丢消息，不要 OOM

---

## 维度 7：Kafka

### Q1（浅）：Kafka 在你的系统里扮演什么角色？

- 可靠通道的传输管道：gRPC 直推失败或用户离线 → 消息进 Kafka
- 异步解耦：Logic 不用等 Worker 处理完
- 消费者组负载均衡：多个 Worker 实例分摊消费
- 消息持久化：磁盘存储，不怕 Worker 崩溃

### Q2（深）：怎么保证 Kafka 消息不丢？

**Producer 端**：`RequiredAcks = WaitForAll`（等所有 ISR 副本确认），`retry.Max = 10`
**Consumer 端**：手动提交 offset，处理成功才 `MarkMessage`
**补偿机制**：如果 Worker 处理失败 → 不提交 offset → Kafka redeliver → Worker 重试 → 幂等去重保护

### Q3（深）：你们的 offset commit 策略是什么？为什么选这个？

手动提交（Manual Commit），不是自动提交。

- 自动提交：每隔 N 秒自动提交当前读到的 offset，Worker 崩溃 → 已读到但未处理的消息丢失
- 手动提交：处理成功后才 `MarkMessage` → 消息不会丢，但可能重复（at-least-once）

**选手动提交是因为"通知不能丢"**。重复可以通过幂等去重解决；丢失无法补救。

### Q4（源码级追问）：Kafka Consumer 的 ConsumeClaim 里，handler 返回 error 后，代码是 continue——这意味着什么？会有什么问题？

```go
if err := h.handler(session.Context(), mqMsg); err != nil {
    log.Error(...)
    continue  // 不 MarkMessage，不提交 offset
}
session.MarkMessage(msg, "")
```

问题：如果这条消息一直处理失败（比如 message body 格式错误导致 unmarshal 失败），它会被无限重试，阻塞这个 partition 的后续消息。

**如何处理**：
1. 不可恢复的错误（unmarshal 失败、格式错误）→ 应该 return nil，跳过这条消息，直接提交 offset
2. 可恢复的错误（gRPC 超时、Comet 不可达）→ 应该 return error，等待重试
3. 需要加 DLQ：重试 N 次后→ 扔进死信队列，提交 offset，不阻塞后续消息

当前代码中，`processMessage` 对 unmarshal 失败的处理是 `return nil`（直接跳过），这是正确的。但对 gRPC 失败的处理需要区分可恢复和不可恢复。

---

## 维度 8：Redis

### Q1（浅）：Redis 在你的系统里扮演什么角色？

- Session 存储（五维索引 + TTL 自动过期）
- 离线消息队列（ZSET，范围查询）
- 消息状态追踪（Hash，HSETNX 原子去重）
- 在线统计（Hash，room online count）
- 用户序列号（INCR，单调递增）

### Q2（深）：为什么离线队列用 ZSET 而不是 List？

- ZSET 的 score 用 seq（用户级递增序列号）→ 支持"拉取 seq > N 的所有消息"
- List 只能按插入顺序 → 无法跳过已 ACK 的消息
- ZSET 的 ZRANGEBYSCORE 天然支持分页（LIMIT offset count）

场景：用户 seq 到了 100，有 3 条消息离线（seq=98, 99, 100）。seq=98 已推送并被 ACK，ZREM 删掉。用户断线重连，拉 seq > 97 → 只拉到 seq=99, 100。如果用 List，seq=98 被删后留下的空洞会导致后面的消息被跳过。

### Q3（源码级追问）：AddSession 用了 Redis Pipeline，为什么不是 MULTI/EXEC 事务？

Pipeline 和 MULTI/EXEC 的区别：
- **Pipeline**：批量发送命令，减少 RTT，但命令之间没有原子性保证
- **MULTI/EXEC**：命令在事务中原子执行，但需要额外的 round-trip（MULTI + 6 条命令 + EXEC = 1 RTT，和 Pipeline 一样）

实际上 AddSession 可以用 MULTI/EXEC 来保证 6 条命令的原子性。当前用 Pipeline 的原因可能是：6 条命令的原子性对于 Session 创建不是关键的——如果某条失败（如 key 冲突），Session 本来就不应该被创建，整个操作失败即可。

**更需要 MULTI/EXEC 的是 Kick（DelSession）**：删除 4 个 key 应该是原子的，否则可能出现 session key 被删了但 user_sessions 中还有引用（脏数据）。不过由于 TTL 会兜底清理，脏数据最终会被清除。

---

## 维度 9：可靠性

### Q1（浅）：你做了哪些保证消息可靠性的措施？

- 双通道投递（快通道失败 → 可靠通道兜底）
- HSETNX 幂等去重（防止重复消费）
- ACK 状态追踪（Pending → Delivered → Acked）
- 离线队列（Redis ZSET，7 天 TTL）
- WAL（Worker 房间批处理防崩溃）
- Kafka 手动 offset commit（处理成功才提交）
- DLQ（死信队列）

### Q2（深）：你说消息 100% 到达，具体是什么含义？

"100% 到达"是**最终一致性**的承诺，不是实时保证。

前提条件：
1. 用户最终会在线（登录到系统）
2. 系统组件不全部同时故障（Redis + Kafka + Worker 不同时挂）
3. 消息在 7 天 TTL 内被消费

在这些前提下，每条消息至少会被推送到客户端一次。实现方式：
- 在线用户：gRPC 直推，< 50ms 到达
- 离线用户：Kafka + ZSET，上线后拉取
- 推失败的：Kafka redeliver → 重试 → 幂等去重
- Worker 崩溃的：Kafka offset 未提交 → 新 Worker 重消费
- 房间批次丢失的：WAL 重放

### Q3（深）：ACK 超时怎么处理？

当前实现中，ACK 没有超时机制。
- 消息标记为 Delivered 后，就一直等待 ACK
- 客户端不发 ACK → 消息状态永远停留在 Delivered
- offline queue 中的消息不会被清理（因为没有 ACK 触发 ZREM）

这个设计是"宁可消息在离线队列里多待，也不能因为 ACK 超时而重新推送导致重复"。

如果想加超时重推：
- 启动一个定时任务扫描 `status=delivered AND now - updated_at > 30s` 的消息
- 重新 reliableEnqueue → 可能会有重复 → 但幂等去重会保护

### Q4（最高难度追问）：假设整个 Redis 集群宕机 1 小时，你的系统会发生什么？

1. **新连接无法建立**：Connect 依赖 Redis 创建 Session → 所有新登录失败
2. **已连接用户不受影响**：已建立的 TCP 连接不依赖 Redis，消息仍可 gRPC 直推
3. **消息去重降级**：HSETNX 失败 → Router 返回 error → 业务方 500 → 可重试
4. **离线消息全部丢失**：Kafka 消息仍然在，但 ZADD offline queue 失败（best-effort）
5. **心跳续期失败**：Session TTL 继续倒数 → 30 分钟后过期

恢复后：
- Worker 继续消费 Kafka 积压 → gRPC 推送 → 客户端收到消息
- 但在宕机期间产生的离线消息，Redis ZSET 是无法补的（ZADD 失败了）
- 可以通过补偿机制：扫描 Kafka 中宕机时段的消息 → 判断用户当时是否在线 → 补写 ZSET

**这个问题暴露了当前架构的一个改进点**：offline ZADD 和 Kafka 写入应该在同一事务中（比如把 offline queue 也写到 Kafka，由 Worker 同时写 Redis 和推送 Comet）。

---

## 维度 10：性能与压测

### Q1（浅）：你的系统性能目标是什么？

- 单 Comet 节点 10 万并发连接
- P99 推送延迟 < 10ms（gRPC 直推在线用户）
- P99 端到端延迟 < 200ms（含 Kafka 异步路径）
- 消息到达率 > 99.99%

### Q2（深）：怎么压测的？

benchmarks/ 目录下有 8 种压测场景：
1. `conn_bench`：连接压测，模拟 N 个客户端同时建立 TCP 连接
2. `push_bench`：Push 压测，模拟 Logic → Comet gRPC 的推送吞吐
3. `arrival_bench`：到达延迟压测，模拟 Push → ACK 完整链路延迟
4. `push_room`：房间推送压测
5. `worker_bench`：Worker 消费吞吐压测
6. `multi_push`：多用户并发推送

使用 `report_gen` 生成 HTML 报告。

### Q3（深）：如果 P99 延迟突然从 10ms 飙升到 500ms，你怎么排查？

1. 看 Prometheus：`PushLatency{direct}` 和 `PushLatency{kafka}` 分别的分布 → 定位是哪个通道慢了
2. 看 Goroutine 数量：是否 goroutine 泄漏 → runtime.NumGoroutine()
3. 看 Comet 的 PriorityQueue 丢弃率：是否 normal channel 满了大量丢弃
4. 看 GC 停顿：`go_gc_duration_seconds` → GC 是否成为瓶颈
5. 看 gRPC 延迟：`grpc_client_handling_seconds` → gRPC 调用本身是否慢了
6. 看 Redis 延迟：Redis 是否响应变慢（网络/CPU）
7. 看系统指标：CPU、内存、网络带宽、TCP 重传率

最常见的原因（按概率）：
1. GC 停顿（Ring Buffer 池被耗尽，临时分配导致 GC 压力）
2. TCP 缓冲区满（客户端消费慢，Write 阻塞）
3. Redis 慢查询（HGETALL 大 Hash）

---

## 综合实战题

### 实战 1：设计题——"如果老板让你把这个项目从支持 10 万连接扩展到 100 万连接，你会怎么做？"

**分层回答**：

**Comet 层**：
1. Bucket 数量从 32 增加到 128（减少锁竞争）
2. goroutine 数量 = 100 万 × 2 = 200 万 → 检查内存（200 万 × 2KB = 4GB，可接受）
3. 可能需要上 `epoll` 替代 goroutine-per-connection（如 gnet/evio 等框架），减少 goroutine 开销

**Logic 层**：
1. 增加 Logic 实例数量（无状态，水平扩展）
2. Router 的 64-shard 内存去重 → 增加到 256-shard
3. Snowflake machineID 要规划好（最多 1024 个 Logic 实例）

**Redis 层**：
1. Redis Cluster 分片（按 uid hash 分片）→ 单 Redis 处理不了 100 万连接的 Session 读写
2. 本地缓存 TTL 可以适当延长（1 分钟 → 5 分钟）

**Kafka 层**：
1. 增加 Partition 数量（匹配 Worker 实例数）
2. Worker 实例数 = Partition 数

### 实战 2：故障设计题——"如果 Comet-1 宕机了，上面 3 万连接同时断开重连到 Comet-2，会发生什么？"

1. **3 万连接同时断开**：
   - Comet-1 的 3 万 serveTCP goroutine 退出 → 每个调 `Disconnect` gRPC → Logic 收到 3 万条 Disconnect 请求
   - 3 万次 `DelSession` Pipeline → Redis 压力瞬间增大
   
2. **3 万连接同时重连到 Comet-2**：
   - 3 万次 TCP SYN → Comet-2 的 accept goroutine 压力
   - 3 万次 `authTCP` 握手 → 3 万次 gRPC Connect → Logic 处理 3 万次登录
   - 3 万次 `AddSession` Pipeline → Redis 压上加压
   - 3 万次 `OnUserOnline` → 3 万次 ZRANGEBYSCORE

3. **保护机制**：
   - 连接限流（Comet 的 TCP accept 有上限）
   - 随机延迟重连（客户端 jitter：1-30 秒随机延迟再重连）
   - Logic 的 HTTP/gRPC 有连接池限制

**面试官可能追问**："你需要多久才能完全恢复？"

Answer: 
- 如果客户端实现了 jitter 重连（1-30s 随机），3 万连接在 30 秒内平滑恢复
- 每条 Connect 约 5ms（含 Redis Pipeline），3 万条 = 150 秒（如果 Logic 有 10 个 gRPC goroutine = 并发 10，实际需要约 15 秒）
- 整体恢复时间约 30-45 秒

---

> **第五阶段总结**：以上就是完整的面试训练矩阵，覆盖 10 个维度 + 2 道实战设计题，从浅到深到源码级。核心原则是：**每个答案都要落到代码层面，每个设计决策都要讲出 Why 和 Trade-off，每个技术点都要能承受至少三层追问**。

---

> **最终项目一句话（面试压轴用）**：GoToIM 不是一个 Demo，它是一个经过可靠性设计、支持百万连接、有完整 ACK 闭环、具备生产级可观测性的消息投递中台。它的核心价值不在于"能发消息"，而在于"确保消息一定到达"——这是从功能思维到可靠性思维的跨越。

---

*学习笔记完。基于 goim 项目全部源码深度遍历分析。*

---

# 补充专题：节点发现与 bilibili/discovery 注册中心

---

## 需要掌握到什么程度？

**不需要深读 discovery 源码**，但需要能讲清楚：
1. discovery 在整个架构中的作用——谁注册、谁发现
2. Comet 节点变更时 Logic/Worker 如何感知——Watch + Fetch 机制
3. 节点故障时的检测链路——从宕机到被移除需要多久
4. CometPusher 的 copy-on-write 更新策略（这个才是亮点）
5. 自我保护机制（防止网络分区导致全集群被误踢）

---

## 1. 架构角色

```
┌──────────────┐     注册 + 心跳续期       ┌──────────────┐
│   Comet      │───HTTP POST /register────▶│  Discovery    │
│   :3101      │───HTTP POST /renew───────▶│  :7171        │
│   (注册自己)  │───HTTP POST /cancel──────▶│  (注册中心)    │
└──────────────┘                          └──────┬───────┘
                                                  │
┌──────────────┐      Watch + Fetch               │
│   Logic      │───GET /polls (long-poll)────────▶│
│   (发现Comet)│───GET /fetch─────────────────────▶│
└──────────────┘                                  │
                                                  │
┌──────────────┐      Watch + Fetch               │
│   Worker     │───GET /polls (long-poll)────────▶│
│   (发现Comet)│───GET /fetch─────────────────────▶│
└──────────────┘                          ┌───────┴──────┐
                                          │  Discovery    │
                                          │  (注册中心)    │
                                          └──────────────┘
```

**关键点**：
- Comet 是"被发现者"——注册自己 + 心跳续期
- Logic 和 Worker 是"发现者"——Watch Comet 列表变更
- AppID `"goim.comet"` 是约定的发现契约

---

## 2. 注册流程（Comet 侧）

```go
// cmd/comet/main.go
ins := &naming.Instance{
    AppID:    "goim.comet",
    Hostname: env.Host,           // comet-1
    Addrs:    []string{"grpc://comet-1:3109"},
    Metadata: map[string]string{
        "weight":   "10",          // 负载均衡权重
        "offline":  "false",       // 是否主动下线
        "addrs":    "tcp://:3101,ws://:3102",  // 客户端连接地址
    },
}
cancel, _ := dis.Register(ins)     // 注册 + 启动后台续期 goroutine

// 每 10 秒更新 metadata（connCount, ipCount → 用于负载均衡）
go func() {
    ticker := time.NewTicker(10 * time.Second)
    for range ticker.C {
        ins.Metadata["connCount"] = strconv.Itoa(conns)
        ins.Metadata["ipCount"] = strconv.Itoa(ips)
        dis.Set(ins)    // 更新 metadata
    }
}()
```

---

## 3. 发现流程（Logic/Worker 侧）

```go
// internal/logic/logic.go
res := dis.Build("goim.comet")     // 创建对 goim.comet 的 Resolver
event := res.Watch()               // 获取变更通知 channel

// 首次获取
instances, _ := res.Fetch()
l.newNodes(instances)              // 构建首次 gRPC 连接池

// 后台循环：Watch 变更事件
go func() {
    for {
        <-event                     // Discovery 推送变更通知
        instances, _ = res.Fetch()  // 重新拉取全量
        l.newNodes(instances)       // 重建节点列表 + gRPC 连接池
    }
}()
```

**Worker 侧完全相同的模式**——`cmd/job/main.go` 中的 `watchComets()`。

---

## 4. CometPusher 的 Copy-on-Write 更新**

这是你在面试中可以重点讲的设计亮点：

```go
// internal/logic/comet.go
func (c *CometPusher) UpdateNodes(nodes []*naming.Instance) {
    // Step 1: 构建新 map（读写锁外）
    newClients := make(map[string]comet.CometClient)
    newConns := make(map[string]*grpc.ClientConn)

    // Step 2: 复用已有连接（只读锁）
    c.mu.RLock()
    for _, node := range nodes {
        if old, ok := c.clients[node.Hostname]; ok {
            newClients[node.Hostname] = old  // 复用
        }
    }
    c.mu.RUnlock()

    // Step 3: 创建缺失的连接（无锁，最耗时的 gRPC dial 不持锁）
    for _, node := range nodes {
        if _, ok := newClients[node.Hostname]; !ok {
            conn, _ := grpc.Dial(node.Addrs[0], ...)
            newClients[node.Hostname] = comet.NewCometClient(conn)
            newConns[node.Hostname] = conn
        }
    }

    // Step 4: 原子替换（极短的写锁）
    c.mu.Lock()
    oldClients := c.clients
    c.clients = newClients
    c.conns = newConns
    c.mu.Unlock()

    // Step 5: 关闭旧连接（无锁，不影响新请求）
    for name, conn := range oldClients {
        if _, ok := newClients[name]; !ok {
            conn.Close()
        }
    }
}
```

**面试表达**：
> CometPusher 的节点更新用了 copy-on-write 策略。关键点是新建 gRPC 连接（dial）在锁外完成——dial 可能耗时几百毫秒，如果持锁会导致所有 PushMsg 被阻塞。只有在最后替换指针时短暂持写锁（微秒级）。

**对比 Worker 的 CometClientPool.Update**（[comet_pool.go](internal/worker/comet_pool.go)）没有用 copy-on-write，而是全程持锁——因为 Worker 是 Push 消息的消费者，频率远低于 Logic。

---

## 5. 节点故障检测全链路

```
Comet 宕机
  │
  ├─ [T+0s]     Comet 进程退出，renewal goroutine 停止
  │
  ├─ [T+30s]    Discovery: 最后一次 RenewTimestamp 是 30 秒前
  │              evict() 定时器：RenewTimestamp + 90s > now? → 还没到
  │
  ├─ [T+90s]    Discovery: evict() 检测到 90 秒未续期
  │              → 标记取消（如果自我保护未激活）
  │              → broadcasts 变更事件到所有 long-poll 连接
  │
  ├─ [T+90s+]   Logic/Worker: long-poll 连接释放
  │              → Watch() channel 触发
  │              → Fetch() 获取新列表（不含宕机节点）
  │              → newNodes() / watchComets() 重建连接池
  │              → 摘除宕机 Comet
  │
  └─ [T+~100s]  总计：约 90-120 秒完成故障节点摘除
```

**自我保护机制**：如果上一分钟的实际续期次数低于预期的 85%，说明可能有网络分区，Discovery 会**禁用自动驱逐**。只有续期超时超过 1 小时的节点才会被强制驱逐。这防止了网络分区导致的全集群雪崩。

---

## 6. 面试可能追问

**Q: 为什么用 bilibili/discovery 而不是 Consul/etcd？**

因为项目基于 goim（B 站开源），goim 原生依赖 bilibili/discovery。在生产部署时，可以替换为其他注册中心——项目中通过接口间接使用，但依赖是硬编码的。

**Q: 如果把 discovery 换成 Consul，要改哪些代码？**

主要改三处：
1. `cmd/comet/main.go`：注册逻辑 → Consul Agent API
2. `internal/logic/nodes.go`：Watch + Fetch 逻辑 → Consul Watch API
3. `internal/worker/` 中的 watch 逻辑

实际改动量不会太大，因为系统的核心逻辑（Router、Session、Dual-Channel）不直接依赖 discovery 的 API。

**Q: 如果 Discovery 挂了怎么办？**

- Comet 已经注册的实例**不会丢失**（Discovery 是内存存储，但会定期 dump 到磁盘）
- Logic/Worker 已有的 Comet 列表保存在内存中，不会因为 Discovery 挂而丢失
- 但新注册的 Comet 无法被 Logic/Worker 发现（直到 Discovery 恢复）
- gRPC keepalive（10s ping）可以提供额外的存活检测

---

> **掌握要点**：discovery 是基础设施，面试时讲清楚"谁注册谁发现"、Watch 机制、故障检测时间线、copy-on-write 更新策略即可。不需要深读源码。

