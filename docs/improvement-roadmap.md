# goim 改进方案完成度与实施路线图

> 生成日期：2026-05-19
> 基于完整代码库分析，对照原始改进方案逐项评估

---

## 总体结论

**整体完成度：约 65-70%**

| 模块 | 完成度 | 说明 |
|---|---:|---|
| IM 核心管道（Router/Worker/Logic） | 40% | 可靠投递语义升级大部分未开始 |
| Notify Server 业务层 | 85% | 三个阶段基本落地，细节待完善 |
| 前端 Dashboard | 90% | 链路追踪、DLQ 管理、SLA 看板已完成 |
| 可观测性 | 65% | 业务指标有，IM 层业务维度指标缺 |

---

## 一、IM 系统改善（完成度 ~40%）

### 1.1 消息投递状态机 — 完成度 30%

**目标状态：**
```
accepted -> routed -> direct_sent -> delivered -> acked
accepted -> routed -> direct_failed -> fallback_queued -> fallback_sent -> acked
accepted -> routed -> offline_stored -> synced -> acked
accepted -> expired -> dlq
```

**当前状态：**
- [x] Redis 层消息状态：`pending -> delivered -> acked / failed`（`router/ack.go`）
- [x] 双通道投递逻辑：gRPC 直连 + Kafka 回退（`router/dispatch.go`）

**待完成：**
- [ ] `accepted -> routed` 阶段无显式状态记录
- [ ] `direct_failed -> fallback_queued -> fallback_sent` 路径无状态机追踪
- [ ] IM 核心管道无 TTL 过期丢弃机制（`expired -> dlq`）
- [ ] 状态转换未在 `internal/router/` 中作为统一状态机落地

**涉及文件：**
- `internal/router/dispatch.go` — RouteByUser 主入口
- `internal/router/ack.go` — ACK 处理
- `internal/worker/dispatch.go` — Worker 投递

**实施步骤：**

```
步骤 1.1.1：定义投递状态枚举
  - 新建 internal/router/state.go
  - 定义 DeliveryState 枚举：accepted, routed, direct_sent, direct_failed,
    fallback_queued, fallback_sent, delivered, offline_stored, synced, acked, expired, dlq
  - 定义合法状态转换 map[DeliveryState][]DeliveryState

步骤 1.1.2：在 DispatchEngine 中集成状态追踪
  - 修改 RouteByUser()，在每个关键节点写入状态
  - ID 生成后 -> accepted
  - 开始路由 -> routed
  - gRPC 直连尝试 -> direct_sent / direct_failed
  - 写入 Kafka -> fallback_queued
  - Worker 投递成功 -> fallback_sent
  - ACK 收到 -> acked

步骤 1.1.3：消息状态持久化
  - 扩展 Redis msg:{msg_id} hash 增加 state 字段
  - 或新建 msg_state:{msg_id} key 记录状态时间线

步骤 1.1.4：TTL 过期机制
  - 在 Worker 消费时检查消息 TTL（从 Kafka header 读取）
  - 过期消息路由到 DLQ topic
  - 添加 expired -> dlq 状态转换
```

### 1.2 多设备 ACK — 完成度 70%

**当前状态：**
- [x] Notify Server 层 `NotificationAck` 有 `DeviceID`、`SessionID`、`AckKey`
- [x] 5 种 ACK 策略：`none`、`best_effort`、`any_device`、`primary_device`、`all_devices`
- [x] `RecordAckWithIdempotency` 按设备记录并评估策略满足

**待完成：**
- [ ] IM 层 `msg:{msg_id}` Redis hash 无 `device_id` / `session_id` 字段
- [ ] `router/ack.go` 的 `HandleACK` 不区分设备
- [ ] 业务层和 IM 层的 ACK 是两套独立系统，未打通

**涉及文件：**
- `internal/router/ack.go` — HandleACK
- `internal/logic/service/ack.go` — AckService（遗留层）
- `internal/logic/dao/redis.go` — Redis 操作

**实施步骤：**

```
步骤 1.2.1：扩展 IM 层 ACK 消息体
  - 修改 api/protocol/message.go 中 AckBody，增加 device_id、session_id 字段
  - 保持向后兼容（新字段 optional）

步骤 1.2.2：扩展 Redis 消息状态
  - msg:{msg_id} hash 增加 acks field，存储 JSON 数组：
    [{"device_id":"web-01","session_id":"sid-xxx","ack_time":"..."}]
  - 或新建 msg_acks:{msg_id} hash，field=device_id, value=session_id:ack_time

步骤 1.2.3：修改 HandleACK 支持设备级记录
  - 从 AckBody 中提取 device_id、session_id
  - 写入设备级 ACK 记录
  - 评估是否满足 ACK 策略（从 msg metadata 读取 expected_ack_count）

步骤 1.2.4：打通 Notify Server 与 IM 层 ACK
  - Notify Server 的 RecordAck 同时调用 IM 层 ACK 接口
  - 或通过 Kafka ACK topic 事件驱动同步
  - 确保 ACK 率指标来自同一数据源
```

### 1.3 离线 inbox/cursor 模型 — 完成度 50%

**当前状态：**
- [x] Redis ZSET `offline:{uid}` 离线队列
- [x] `user_seq:{uid}` 单调递增序列号
- [x] 重连时 `OnUserOnline(uid, lastSeq)` 按 cursor 拉取
- [x] `GET /goim/sync` 支持 `HasMore` 分页
- [x] 7 天 TTL

**待完成：**
- [ ] 无每设备独立 cursor
- [ ] 无消息合并/去重策略
- [ ] 无按优先级排序 inbox
- [ ] 无最大堆积限制和溢出策略

**涉及文件：**
- `internal/logic/service/sync.go` — OnUserOnline, GetOfflineMessages
- `internal/logic/dao/redis.go` — Redis 操作

**实施步骤：**

```
步骤 1.3.1：设备级 cursor
  - 新增 Redis key：device_cursor:{uid}:{device_id} 记录每设备 last_seq
  - 修改 OnUserOnline 支持按设备同步
  - 修改 GET /goim/sync 增加 device_id 参数

步骤 1.3.2：消息合并策略
  - 新建 internal/router/merge.go
  - 定义 MergePolicy 接口：ShouldMerge(existing, new) bool
  - 实现 OrderStatusMergePolicy：同订单短时间内的状态变更合并为最新状态
  - 在 RouteByUser 中，写入 offline queue 前检查合并

步骤 1.3.3：优先级排序
  - offline:{uid} ZSET 的 score 从纯 seq 改为 priority*10^10 + seq
  - 高优先级消息排在前面
  - OnUserOnline 拉取时按 score 顺序投递

步骤 1.3.4：堆积限制
  - 新增 Redis key：offline_count:{uid} 计数器
  - 写入前检查是否超过最大限制（如 1000 条）
  - 溢出策略：丢弃最旧低优先级消息，或拒绝新消息并告警
```

### 1.4 路由层投递尝试记录 — 完成度 75%

**当前状态：**
- [x] `TrackMessage()` Redis `HSETNX` 幂等
- [x] Notify Server `notification_attempts` 表完整记录
- [x] `PushClient.DeliveryResult` 返回 Path 信息

**待完成：**
- [ ] IM 核心 `router/dispatch.go` 的 `RouteByUser` 不写 attempt 记录
- [ ] IM 路由层无独立 attempt 存储

**涉及文件：**
- `internal/router/dispatch.go`

**实施步骤：**

```
步骤 1.4.1：定义投递尝试记录结构
  - 新建 internal/router/attempt.go
  - 定义 DeliveryAttempt 结构体（与 Notify Server 的 NotificationAttempt 对齐）

步骤 1.4.2：在 RouteByUser 中记录尝试
  - gRPC 直连尝试后：记录 channel=grpc_direct, status, latency, error
  - Kafka 回退写入后：记录 channel=kafka_fallback
  - 写入 offline queue 后：记录 channel=offline_stored

步骤 1.4.3：尝试记录存储
  - 短期方案：写入 Redis hash attempt:{msg_id}:{attempt_no}
  - 长期方案：通过回调或 Kafka 事件写入 Notify Server 的 notification_attempts 表
```

### 1.5 推送接口优先级和过期时间 — 完成度 60%

**当前状态：**
- [x] Notify Server `policy.go` 4 级优先级
- [x] 每事件类型独立 TTL（30s ~ 24h）
- [x] Outbox 按优先级排序投递

**待完成：**
- [ ] IM 核心管道 `DispatchEngine` 不识别优先级
- [ ] Kafka 层无 TTL 过期丢弃
- [ ] `dedupe_key` 只在 Notify Server 层

**涉及文件：**
- `internal/router/dispatch.go`
- `internal/mq/kafka/producer.go`
- `internal/mq/kafka/consumer.go`

**实施步骤：**

```
步骤 1.5.1：扩展 Logic push API 消息体
  - 修改 PushMsgRequest protobuf 增加 priority、ttl_seconds、dedupe_key、trace_id 字段
  - 重新生成 protobuf 代码

步骤 1.5.2：Kafka 消息头传递优先级和 TTL
  - producer 在 Kafka header 中写入 goim_priority、goim_ttl
  - consumer 读取 header 做相应处理

步骤 1.5.3：Consumer 端优先级处理
  - 方案 A（推荐）：按优先级分 topic（push.high、push.normal、push.low）
  - 方案 B：同一 topic 内 consumer 端排序（复杂，不推荐）

步骤 1.5.4：TTL 过期丢弃
  - Worker 消费时检查 goim_ttl header
  - 当前时间 > created_at + ttl_seconds 则丢弃并路由 DLQ
```

### 1.6 背压、限流和降级 — 完成度 20%

**当前状态：**
- [x] `router/ratelimit.go` token bucket 限流器
- [x] `comet/channel.go` 每连接限流
- [x] 广播按实例分摊限速

**待完成：**
- [ ] **Router RateLimiter 未接入 DispatchEngine**（最关键缺口）
- [ ] 无按业务方限流
- [ ] 无按消息类型限流
- [ ] 无 Kafka backlog 监控和自动降级
- [ ] 无 Comet 节点压力感知

**涉及文件：**
- `internal/router/dispatch.go` — 接入限流
- `internal/router/ratelimit.go` — 扩展限流维度

**实施步骤：**

```
步骤 1.6.1：接入 Router RateLimiter（最小改动，最高收益）
  - 在 DispatchEngine.RouteByUser() 入口处调用 limiter.AllowUser(uid, rate, burst)
  - 拒绝时返回限流错误，记录 metric
  - 配置化：rate 和 burst 从配置文件读取

步骤 1.6.2：多维度限流扩展
  - 扩展 RateLimiter 支持 business_type 维度
  - AllowBusinessType(bt, rate, burst) — 按业务方限流
  - AllowMsgType(mt, rate, burst) — 按消息类型限流
  - 在 RouteByUser 中从消息 header 读取 business_type 并检查

步骤 1.6.3：Kafka backlog 监控
  - 新建 internal/router/backpressure.go
  - 定期查询 Kafka consumer group lag（通过 Sarama AdminClient）
  - lag 超过阈值时触发降级：拒绝低优先级消息，只接受 high/critical

步骤 1.6.4：Comet 节点压力感知
  - Comet gRPC 接口增加 ReportLoad 方法（连接数、内存、CPU）
  - Router 在选择目标 Comet 时考虑负载
  - 高负载 Comet 只接受 high/critical 优先级消息
```

### 1.7 业务链路可观测性 — 完成度 65%

**当前状态：**
- [x] Notify Server `PlatformStats` 聚合指标
- [x] `BusinessSLA` 按 business_type / delivery_path 分维度
- [x] `GetNotificationTrace` 单通知链路
- [x] `GetOrderTimeline` 订单级时间线
- [x] go.mod 已有 Prometheus + OpenTelemetry 依赖

**待完成：**
- [ ] IM 核心层无业务类型维度指标
- [ ] 无 `per priority drop count`、`oldest offline message age`
- [ ] OpenTelemetry trace 未串联全链路
- [ ] Notify Server 无 Prometheus metrics 端点

**涉及文件：**
- `internal/notify/server.go` — 暴露 metrics 端点
- `internal/router/dispatch.go` — 增加 metrics

**实施步骤：**

```
步骤 1.7.1：Notify Server 暴露 Prometheus 端点
  - 引入 ginprom 或自定义 middleware
  - /metrics 端点暴露：http_request_duration_seconds, notification_total,
    outbox_pending, dlq_total, ack_total 等

步骤 1.7.2：IM 核心层增加业务维度指标
  - Router 层增加 counter：goim_push_total{business_type, priority, channel}
  - Router 层增加 counter：goim_push_drop_total{business_type, priority, reason}
  - Worker 层增加 histogram：goim_delivery_latency_seconds{channel}
  - 新增 gauge：goim_offline_oldest_age_seconds（最老离线消息年龄）

步骤 1.7.3：OpenTelemetry trace 串联
  - Notify Server 创建 span：notify.create, notify.outbox, notify.deliver
  - 通过 HTTP header 传递 trace_id 到 Logic
  - Logic -> Router -> Worker -> Comet 各层创建子 span
  - 最终可查询从"订单状态变更"到"客户端 ACK"的完整 trace

步骤 1.7.4：Grafana Dashboard 模板
  - 提供 JSON dashboard 模板
  - 包含：投递成功率、ACK 延迟分位、DLQ 趋势、优先级丢弃、离线堆积
```

---

## 二、业务系统改善（完成度 ~85%）

### 2.1 持久化存储 — 完成度 95%

**当前状态：**
- [x] 13 张 MySQL 表完整覆盖
- [x] 状态历史 append-only（`order_status_events`）
- [x] Schema 自动迁移（`addColumnIfMissing`）

**待完成：**
- [ ] SQLite 清理（`go.mod` 中 `modernc.org/sqlite` 待移除）
- [ ] 幂等键无过期清理机制

**实施步骤：**

```
步骤 2.1.1：完成 SQLite 移除
  - 按 docs/superpowers/specs/2026-05-19-remove-sqlite-mysql-only-design.md 执行
  - 从 go.mod 移除 modernc.org/sqlite
  - 确认所有测试使用 MySQL

步骤 2.1.2：幂等键过期清理
  - 新增定时任务：清理 created_at > 24h 的 idempotency_keys 记录
  - 或在 initSchema 中创建 MySQL EVENT 定时清理
  - OutboxWorker 中增加 cleanupIdempotencyKeys() 调用
```

### 2.2 Outbox Pattern — 完成度 95%

**当前状态：**
- [x] 事务性写入（order + notification + outbox + idempotency key）
- [x] OutboxWorker 后台轮询 + 锁机制 + 指数退避
- [x] 成功/失败/DLQ 三态流转
- [x] 场景运行计数器联动

**基本完成，无重大缺口。**

### 2.3 幂等键 — 完成度 90%

**当前状态：**
- [x] 所有写操作支持 `Idempotency-Key`
- [x] `idempotency_keys` 表存储 scope + key + response snapshot
- [x] Handler 层从 header 或 body 提取

**待完成：**
- [ ] 幂等键无过期清理（已在 2.1.2 中覆盖）

### 2.4 状态机强约束 — 完成度 90%

**当前状态：**
- [x] `ValidTransition(from, to)` 严格定义 7 状态流转
- [x] 非法状态变更返回 HTTP 409
- [x] 前端同步定义相同状态机

**待完成：**
- [ ] 拒绝原因日志不够结构化

**实施步骤：**

```
步骤 2.4.1：结构化拒绝日志
  - 在 ValidTransition 返回 TransitionError 结构体
  - 包含：from, to, allowed_transitions, reason, timestamp
  - Handler 将此信息写入日志和响应 body
```

### 2.5 Notification Policy 层 — 完成度 85%

**当前状态：**
- [x] 按事件类型定义优先级、TTL、ACK 策略、重试次数、DLQ 开关
- [x] 7 种事件类型独立策略
- [x] 5 种 ACK 策略模式

**待完成：**
- [ ] 策略硬编码，无法运行时调整
- [ ] 无通知模板系统
- [ ] 无显式"失败补偿策略"字段

**实施步骤：**

```
步骤 2.5.1：策略配置化
  - 新建 internal/notify/policy/config.go
  - 从 YAML/TOML 文件加载策略配置
  - 支持运行时热加载（fsnotify 或定时重读）
  - 保留代码内默认值作为 fallback

步骤 2.5.2：通知模板系统
  - 新建 internal/notify/template/ 包
  - 定义 Template 结构：name, title_template, content_template, variables
  - 存储在 notification_templates 表
  - Policy 增加 template_name 字段
  - 创建通知时根据模板渲染 title 和 content

步骤 2.5.3：失败补偿策略字段
  - Policy 增加 CompensationStrategy 枚举：
    - retry_then_dlq（默认）
    - retry_then_drop
    - retry_then_alert
    - immediate_dlq
  - OutboxWorker 根据策略决定失败后行为
```

### 2.6 Campaign 闪购模型 — 完成度 70%

**当前状态：**
- [x] `campaigns` + `campaign_targets` 表
- [x] 按用户列表或房间广播
- [x] 批量写入（500 条一批）
- [x] Campaign 级统计

**待完成：**
- [ ] 无分批投递、暂停、恢复、取消 API
- [ ] 无速率限制控制
- [ ] 无动态 audience_rule 人群筛选

**实施步骤：**

```
步骤 2.6.1：Campaign 生命周期 API
  - PATCH /api/campaigns/:id/pause — 暂停投递
  - PATCH /api/campaigns/:id/resume — 恢复投递
  - PATCH /api/campaigns/:id/cancel — 取消并清理 outbox
  - campaigns 表增加 status 字段（draft, active, paused, completed, cancelled）
  - OutboxWorker 检查 campaign status，暂停/取消的跳过

步骤 2.6.2：投递速率控制
  - campaigns 表增加 rate_limit (QPS) 字段
  - OutboxWorker 处理 campaign targets 时使用 token bucket 限速
  - 支持动态调整 rate_limit

步骤 2.6.3：动态人群筛选
  - campaigns 表增加 audience_rule JSON 字段
  - 定义规则 DSL：{"min_orders": 5, "city": "上海", "last_active_days": 7}
  - FlashSaleService 在投递前按规则筛选用户
  - 与现有 user 表或外部用户服务对接
```

### 2.7 Scenario Run — 完成度 80%

**当前状态：**
- [x] `scenario_runs` + `scenario_events` 表
- [x] 4 个 API 端点
- [x] 场景运行计数器
- [x] 前端 3 种业务场景

**待完成：**
- [ ] ScenarioRun 缺 P99 延迟、错误列表返回
- [ ] 无压测结果报告导出

**实施步骤：**

```
步骤 2.7.1：丰富 ScenarioRun 返回内容
  - ScenarioRun 结构增加字段：
    P50Latency, P95Latency, P99Latency, MaxLatency
    ErrorCount, ErrorSummary []ErrorEntry
    DLQCount, RetryCount
  - 在 scenario 运行过程中实时更新这些指标

步骤 2.7.2：压测结果报告
  - 新增 GET /api/scenarios/:id/report 端点
  - 返回 JSON 格式的完整报告
  - 包含：时间线、阶段指标、错误分布、延迟分布、建议
  - 前端增加"导出报告"按钮
```

---

## 三、边界定义（完成度 ~60%）

### 3.1 当前状态

- [x] Notify Server 通过 HTTP API 调用 Logic，不接触 Comet
- [x] IM 层不理解订单状态
- [x] 消息结构有 `business_type`、`event_type`、`priority`、`trace_id`

### 3.2 待完成

- [ ] 消息协议未标准化（自定义 JSON，非统一结构）
- [ ] `dedupe_key` 未在 IM 层传递
- [ ] 业务层和 IM 层 ACK 系统独立

**实施步骤：**

```
步骤 3.2.1：标准化消息协议
  - 定义统一消息信封 protobuf：
    message BizEnvelope {
      string msg_id = 1;
      string business_id = 2;
      string business_type = 3;
      string event_type = 4;
      string user_id = 5;
      string priority = 6;
      int32  ttl_seconds = 7;
      string dedupe_key = 8;
      string trace_id = 9;
      bytes  payload = 10;
    }
  - Notify Server 发送时包装为 BizEnvelope
  - Logic/Router 解析 envelope 获取 business_type 等字段

步骤 3.2.2：打通 ACK 系统
  - IM 层 ACK 事件（Kafka ACK topic）同时写入 Notify Server
  - 或 Notify Server 订阅 Kafka ACK topic 做增量同步
  - 消除两套独立 ACK 系统的数据不一致风险
```

---

## 四、分阶段实施计划

### 阶段总览

```
阶段 0  快速修复（1-2d）     ──  补洞，无风险，立即可见改善
  │
阶段 1  基础收尾（1 周）     ──  把已完成的 90% 推到 100%
  │
阶段 2  IM 可靠投递（2 周）  ──  核心缺口：状态机、多设备 ACK、优先级、限流
  │
阶段 3  业务编排增强（2 周）  ──  策略配置化、模板、Campaign、Scenario 报告
  │
阶段 4  生产治理（2-3 周）   ──  全链路 trace、多维限流、降级、Chaos 测试
```

---

### 阶段 0：快速修复

**目标：** 把已存在的基础设施接入主线，零架构风险
**工期：** 1-2 天
**验收标准：** Router 限流生效、幂等键不再无限增长、Prometheus 可抓取 Notify 指标

| # | 任务 | 改动点 | 工时 |
|---:|---|---|---:|
| 0.1 | **接入 Router RateLimiter** | `router/dispatch.go` RouteByUser 入口加一行 `limiter.AllowUser()` | 0.5d |
| 0.2 | **幂等键过期清理** | `outbox_worker.go` 增加 `cleanupStaleIdempotencyKeys()` 定时清理 >24h 的记录 | 0.5d |
| 0.3 | **Notify Server /metrics 端点** | `server.go` 注册 promhttp handler，暴露 http_requests / notifications / outbox / dlq 计数 | 0.5d |
| 0.4 | **结构化拒绝日志** | `model/order.go` ValidTransition 返回 TransitionError 结构体，handler 写入响应 body | 0.5d |

**前置依赖：** 无
**风险：** 无 — 都是新增代码或一行调用，不改变现有逻辑

---

### 阶段 1：基础收尾

**目标：** 把已接近完成的功能推到 100%，清理技术债
**工期：** 1 周
**验收标准：** SQLite 完全移除、ScenarioRun 返回完整指标、IM 层 ACK 携带设备信息、路由层有 attempt 记录

| # | 任务 | 改动点 | 工时 | 依赖 |
|---:|---|---|---:|---|
| 1.1 | **完成 SQLite 移除** | `store/store.go` 删除 OpenSQLite/SQLiteStore 分支；`go.mod` 移除 modernc.org/sqlite | 0.5d | 无 |
| 1.2 | **ScenarioRun 增加延迟和错误字段** | `model/notification.go` ScenarioRun 增加 P50/P95/P99/MaxLatency + ErrorSummary；`simulator/engine.go` 实时计算 | 1d | 无 |
| 1.3 | **IM 层 ACK 增加 device_id** | `api/protocol/message.go` AckBody 增加 device_id/session_id；`router/ack.go` HandleACK 写入 Redis | 1d | 无 |
| 1.4 | **路由层投递 attempt 记录** | 新建 `router/attempt.go`；`router/dispatch.go` RouteByUser 在 gRPC/Kafka/offline 各路径写 attempt 到 Redis | 1.5d | 无 |
| 1.5 | **打通 ACK 系统** | Notify Server 订阅 Kafka ACK topic 或 RecordAck 回写 IM 层；消除两套 ACK 数据源 | 1d | 1.3 |

**前置依赖：** 阶段 0 完成
**风险：** 1.1（SQLite 移除）需要跑通全部测试；1.3/1.5 涉及协议变更需滚动升级

---

### 阶段 2：IM 可靠投递

**目标：** 补全 IM 核心管道的可靠投递语义，让消息从"能发"变成"可追踪、可解释"
**工期：** 2 周
**验收标准：** 消息有完整状态机、Kafka 支持优先级和 TTL、离线消息按设备同步、限流按业务维度生效

| # | 任务 | 改动点 | 工时 | 依赖 |
|---:|---|---|---:|---|
| 2.1 | **投递状态机** | 新建 `router/state.go` 定义状态枚举和转换表；`dispatch.go` 在各节点写状态到 Redis | 2d | 1.4 |
| 2.2 | **消息优先级在 Kafka 层落地** | `logic.proto` PushMsgRequest 增加 priority/ttl/trace_id；`mq/kafka/producer.go` 写入 Kafka header；按优先级分 topic（push.high/push.normal/push.low） | 2d | 无 |
| 2.3 | **TTL 过期丢弃** | `mq/kafka/consumer.go` Worker 消费时检查 goim_ttl header，过期路由 DLQ | 1d | 2.1 |
| 2.4 | **设备级离线 cursor** | `dao/redis.go` 新增 device_cursor:{uid}:{device_id}；`service/sync.go` OnUserOnline 支持按设备同步 | 1.5d | 1.3 |
| 2.5 | **离线消息合并策略** | 新建 `router/merge.go` MergePolicy 接口 + OrderStatusMergePolicy 实现；`dispatch.go` 写入 offline queue 前检查合并 | 1.5d | 2.4 |
| 2.6 | **标准化消息协议** | 定义 BizEnvelope protobuf（msg_id/business_id/business_type/event_type/priority/ttl/dedupe_key/trace_id/payload）；Notify Server 发送时包装 | 1.5d | 2.2 |
| 2.7 | **多维度限流** | `ratelimit.go` 扩展 business_type 维度；`dispatch.go` 从消息 header 读取 business_type 做限流检查 | 1.5d | 0.1 |

**前置依赖：** 阶段 1 完成
**风险：** 2.1/2.2/2.6 涉及核心路径和协议变更，需要：
- 灰度发布（先上线 producer，再上线 consumer）
- Kafka topic 提前创建
- 滚动升级期间新旧协议兼容（BizEnvelope 的 payload 字段兜底）

---

### 阶段 3：业务编排增强

**目标：** 让 Notify Server 从"能用"变成"好运营"，支撑真实业务场景
**工期：** 2 周
**验收标准：** 策略可热更新、Campaign 可暂停恢复、有通知模板、Scenario 有完整报告

| # | 任务 | 改动点 | 工时 | 依赖 |
|---:|---|---|---:|---|
| 3.1 | **策略配置化** | 新建 `notify/policy/config.go`；从 YAML 加载策略；fsnotify 热加载；保留代码默认值 fallback | 1.5d | 无 |
| 3.2 | **失败补偿策略字段** | Policy 增加 CompensationStrategy 枚举（retry_then_dlq / retry_then_drop / retry_then_alert / immediate_dlq）；OutboxWorker 按策略决策 | 1d | 3.1 |
| 3.3 | **通知模板系统** | 新建 `notify/template/` 包；notification_templates 表；Policy 增加 template_name；创建通知时按模板渲染 | 2d | 无 |
| 3.4 | **Campaign 生命周期 API** | campaigns 表增加 status 字段；PATCH /api/campaigns/:id/{pause,resume,cancel}；OutboxWorker 检查 campaign status | 2d | 无 |
| 3.5 | **Campaign 速率控制** | campaigns 表增加 rate_limit 字段；OutboxWorker 处理 campaign targets 时 token bucket 限速 | 1d | 3.4 |
| 3.6 | **压测结果报告** | GET /api/scenarios/:id/report 端点；JSON 报告含时间线、阶段指标、错误分布、延迟分布；前端增加导出按钮 | 1.5d | 1.2 |

**前置依赖：** 阶段 1 完成（3.6 依赖 1.2 的 ScenarioRun 字段增强）
**风险：** 低 — 都是 Notify Server 内部改动，不影响 IM 核心管道

---

### 阶段 4：生产治理

**目标：** 面向大规模生产环境的治理能力，支撑大促和故障恢复
**工期：** 2-3 周
**验收标准：** 全链路 trace 可查询、Grafana Dashboard 可用、降级策略自动触发、Chaos 测试通过

| # | 任务 | 改动点 | 工时 | 依赖 |
|---:|---|---|---:|---|
| 4.1 | **Kafka backlog 监控与自动降级** | 新建 `router/backpressure.go`；定期查 consumer group lag；超阈值拒绝低优先级消息 | 2d | 2.2 |
| 4.2 | **Comet 节点压力感知** | Comet gRPC 增加 ReportLoad 方法；Router 选目标时考虑负载；高负载只接受 high/critical | 2d | 2.1 |
| 4.3 | **离线消息堆积限制** | `dao/redis.go` 新增 offline_count:{uid} 计数器；写入前检查上限；溢出丢弃最旧低优先级 | 1d | 2.4 |
| 4.4 | **IM 层业务维度 Prometheus 指标** | Router 增加 goim_push_total{business_type, priority, channel}、goim_push_drop_total；Worker 增加 goim_delivery_latency_seconds | 1.5d | 2.6 |
| 4.5 | **OpenTelemetry 全链路 trace** | Notify Server span -> HTTP header -> Logic -> Router -> Worker -> Comet 各层子 span；采样策略 | 3d | 2.6 |
| 4.6 | **Grafana Dashboard 模板** | 提供 JSON 模板：投递成功率、ACK 延迟分位、DLQ 趋势、优先级丢弃、离线堆积 | 1.5d | 4.4 |
| 4.7 | **动态人群筛选** | campaigns 表增加 audience_rule JSON；定义规则 DSL；FlashSaleService 按规则筛选用户 | 2d | 3.4 |
| 4.8 | **Chaos 测试框架** | 模拟 Logic 挂 / Comet 挂 / Kafka 延迟 / Redis 抖动 / 客户端离线；验证降级和恢复 | 3d | 全部 |

**前置依赖：** 阶段 2、3 完成
**风险：** 4.5 全链路 trace 在高 QPS 下有性能开销，需要采样率可配置；4.2 Comet 压力感知需要 Comet 侧配合改动

---

## 五、阶段依赖关系图

```
                    ┌─────────────────────────────────────────────────┐
                    │              阶段 0：快速修复 (1-2d)             │
                    │  0.1 限流接入  0.2 幂等清理  0.3 metrics  0.4 日志 │
                    └────────────────────┬────────────────────────────┘
                                         │
                    ┌────────────────────▼────────────────────────────┐
                    │              阶段 1：基础收尾 (1w)               │
                    │  1.1 SQLite移除  1.2 Scenario字段  1.3 ACK设备  │
                    │  1.4 attempt记录  1.5 ACK打通                   │
                    └──────────┬─────────────────────┬───────────────┘
                               │                     │
          ┌────────────────────▼───────┐   ┌────────▼──────────────────┐
          │    阶段 2：IM 可靠投递 (2w)  │   │  阶段 3：业务编排增强 (2w) │
          │  2.1 状态机    2.2 优先级   │   │  3.1 策略配置  3.2 补偿   │
          │  2.3 TTL       2.4 设备cursor│  │  3.3 模板      3.4 Campaign│
          │  2.5 合并      2.6 协议     │   │  3.5 限速      3.6 报告   │
          │  2.7 多维限流               │   │                           │
          └────────────────┬───────────┘   └───────────┬───────────────┘
                           │                           │
                    ┌──────▼───────────────────────────▼──────────────┐
                    │              阶段 4：生产治理 (2-3w)              │
                    │  4.1 背压  4.2 Comet感知  4.3 堆积限制           │
                    │  4.4 指标  4.5 OTel trace  4.6 Grafana          │
                    │  4.7 动态人群  4.8 Chaos 测试                    │
                    └─────────────────────────────────────────────────┘
```

**阶段 2 和阶段 3 可以并行开发**，因为：
- 阶段 2 改的是 IM 核心管道（Router/Worker/Kafka）
- 阶段 3 改的是 Notify Server 业务层（Policy/Template/Campaign）
- 两者唯一的交叉点是 2.6（标准化消息协议），Notify Server 侧的适配可以后做

---

## 六、每阶段交付物与验收方式

### 阶段 0 验收
```
✓ curl localhost:3119/metrics 能看到 goim_push_limiter_rejected_total 指标增长
✓ SELECT COUNT(*) FROM idempotency_keys WHERE created_at < NOW() - INTERVAL 1 DAY 会自动减少
✓ curl localhost:3121/metrics 能看到 notify_notifications_total 等指标
✓ POST /api/order/status-change 传非法状态返回 {"code":409, "error":"invalid transition", "allowed":["paid","cancelled"]}
```

### 阶段 1 验收
```
✓ go build ./... 不再依赖 modernc.org/sqlite
✓ GET /api/scenarios/:id 返回包含 p99_latency、error_summary 字段
✓ Redis HGETALL msg:{id} 包含 acks 字段，值为 JSON 数组
✓ Redis HGETALL attempt:{id}:1 包含 channel、status、latency_ms
✓ ACK 率指标在 Notify Server 和 IM 层一致
```

### 阶段 2 验收
```
✓ Redis HGET msg:{id} state 返回 acked，state_timeline 包含完整路径
✓ Kafka topic push.high / push.normal / push.low 分开
✓ 过期消息自动进入 DLQ（TTL 测试：发一条 ttl=5s 的消息，6s 后查 DLQ）
✓ 客户端重连时 /goim/sync?device_id=web-01 只返回该设备未同步的消息
✓ 同一订单 5s 内 status paid->confirmed->shipped 合并为一条 shipped 通知
✓ POST /goim/push/mids 超过 rate limit 返回 429
```

### 阶段 3 验收
```
✓ 修改 policy.yaml 后不重启服务，新策略 5s 内生效
✓ 创建订单通知自动套用模板渲染 title/content
✓ POST /api/campaigns/:id/pause 后 OutboxWorker 跳过该 campaign 的 outbox
✓ campaigns 表 rate_limit=10 时，实际投递 QPS 不超过 10
✓ GET /api/scenarios/:id/report 返回完整 JSON 报告
```

### 阶段 4 验收
```
✓ Kafka consumer lag > 10000 时，自动拒绝 low 优先级消息
✓ Comet 节点连接数 > 90% 容量时，新消息只路由到低负载节点
✓ offline:{uid} 超过 1000 条时，新消息被拒绝并返回 429
✓ Jaeger/Tempo 中可查询从 "订单创建" 到 "客户端 ACK" 的完整 trace
✓ Grafana 导入 dashboard JSON 后立即可用
✓ Kill Logic 进程后，消息自动走 Kafka 回退并恢复
```

---

## 七、文件变更影响矩阵

| 文件 | 阶段 0 | 阶段 1 | 阶段 2 | 阶段 3 | 阶段 4 |
|---|---|---|---|---|---|
| `internal/router/dispatch.go` | 0.1 | 1.4 | 2.1 2.2 2.3 2.5 2.7 | - | 4.1 4.2 |
| `internal/router/ack.go` | - | 1.3 1.5 | - | - | - |
| `internal/router/ratelimit.go` | 0.1 | - | 2.7 | - | - |
| `internal/logic/service/sync.go` | - | - | 2.4 2.5 | - | 4.3 |
| `internal/logic/dao/redis.go` | - | 1.3 | 2.4 | - | 4.3 |
| `internal/mq/kafka/producer.go` | - | - | 2.2 | - | - |
| `internal/mq/kafka/consumer.go` | - | - | 2.2 2.3 | - | 4.1 |
| `internal/notify/store/store.go` | - | 1.1 | - | 3.1 3.4 | 4.7 |
| `internal/notify/model/notification.go` | - | 1.2 | - | 3.2 | - |
| `internal/notify/handler/` | 0.4 | - | - | 3.4 3.6 | 4.7 |
| `internal/notify/policy/policy.go` | - | - | - | 3.1 3.2 | - |
| `internal/notify/server.go` | 0.3 | - | - | - | - |
| `internal/notify/service/outbox_worker.go` | 0.2 | - | - | 3.2 3.5 | - |
| `internal/notify/service/flash_sale.go` | - | - | - | 3.4 | 4.7 |
| `internal/notify/simulator/engine.go` | - | 1.2 | - | 3.6 | - |
| `api/protocol/message.go` | - | 1.3 | - | - | - |
| `api/logic/logic.proto` | - | - | 2.2 2.6 | - | - |
| `api/comet/comet.proto` | - | - | - | - | 4.2 |
| `web/dashboard/` | - | 1.2 | - | 3.6 | 4.6 |

---

## 八、风险与注意事项

| 风险 | 影响阶段 | 缓解措施 |
|---|---|---|
| `router/dispatch.go` 是核心路径，改动可能影响所有消息投递 | 阶段 2 | 灰度发布；Feature flag 控制新逻辑开关；充分压测 |
| Kafka 消息格式变更（增加 header / 分 topic）需滚动升级 | 阶段 2 | 先上线 producer（兼容旧 consumer），再上线 consumer；过渡期双写 |
| Redis 内存增长（设备 ACK、attempt、device_cursor） | 阶段 1-2 | 评估容量；设置合理 TTL；监控 Redis 内存指标 |
| SQLite 移除可能暴露测试中隐藏的 MySQL 兼容问题 | 阶段 1 | 先在 CI 中用 MySQL 跑全量测试，确认通过后再移除 SQLite |
| OpenTelemetry 全链路 trace 在高 QPS 下有性能开销 | 阶段 4 | 采样率可配置（默认 1%）；支持按 business_type 动态调整 |
| Campaign 动态人群筛选依赖外部用户数据 | 阶段 4 | 先支持传入固定 UID 列表；人群筛选作为可选增强 |
| 阶段 2 和阶段 3 并行开发时，2.6（标准化协议）需要两边协调 | 阶段 2-3 | 2.6 的 protobuf 定义先确定，Notify Server 侧适配放最后 |
