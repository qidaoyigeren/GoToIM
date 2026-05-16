# goim

[![Language](https://img.shields.io/badge/Language-Go-blue.svg)](https://golang.org/)
[![Go Report Card](https://goreportcard.com/badge/github.com/Terry-Mao/goim)](https://goreportcard.com/report/github.com/Terry-Mao/goim)

**goim** 是一个基于 Go 的实时消息投递中台，支持百万级并发连接下的可靠消息推送。在 [Bilibili goim v2](https://github.com/Terry-Mao/goim) 基础上，从聊天转发系统升级为具备 **ACK 确认、双通道投递、离线补偿、多端同步** 能力的分布式消息基础设施。

作为一个消息中台，goim 向上支撑多种业务场景——**订单状态推送、物流更新、秒杀通知、直播间弹幕、IM 聊天**——由业务层通过标准 HTTP API 接入即可获得可靠的消息投递能力。

内置的 **Order Notification Platform** (Notify Server) 是一个完整的业务应用示例，展示了消息中台如何服务于电商场景。

## 架构

### 系统拓扑

```
                       ┌─────────────────┐
                       │  Business Layer  │
                       │  Order / IM /    │
                       │  Live-Streaming  │
                       └────────┬────────┘
                                │ HTTP REST
                                ▼
                  ┌─────────────────────────┐
                  │    Notify Server :3121   │  业务 Demo
                  │  Order Status / Flash    │  (电商通知平台)
                  │  Sale / Logistics / Sim  │
                  └────────┬────────────────┘
                           │ HTTP Push API
                           ▼
┌──────────────────────────────────────────────────────────────┐
│                        goim 中台                              │
│                                                              │
│   ┌──────────┐     ┌──────────┐     ┌──────────┐            │
│   │  Comet   │ ... │ Comet N  │     │  Logic   │            │
│   │ TCP:3101 │     │ WS:3102  │     │ HTTP:3111│            │
│   │ gRPC:3109│     │          │     │ gRPC:3119│            │
│   └────┬─────┘     └────┬─────┘     └────┬─────┘            │
│        │ gRPC           │               │                    │
│        └────────────────┼───────────────┘                    │
│                         │         │                          │
│                    ┌────▼──┐ ┌───▼────┐                      │
│                    │ Redis │ │ Kafka  │                      │
│                    └───────┘ └───┬────┘                      │
│                                  │                           │
│                          ┌───────▼──────┐                    │
│                          │     Job      │                    │
│                          │ DeliveryWorker│                   │
│                          └──────────────┘                    │
└──────────────────────────────────────────────────────────────┘
```

| 组件 | 职责 | 端口 |
|------|------|------|
| **Comet** | 连接网关，维护 TCP/WebSocket 长连接 | TCP :3101, WS :3102, gRPC :3109 |
| **Logic** | 业务逻辑，认证、路由、会话管理、消息分发 | HTTP :3111, gRPC :3119 |
| **Job** | Kafka 消费投递，将消息可靠推送到 Comet | — |
| **Notify Server** | 电商业务 Demo，订单/秒杀/物流通知 + 负载模拟 | HTTP :3121 |
| **Discovery** | 服务注册发现 | HTTP :7171 |

### 消息路由引擎

核心路由模块 `internal/router/` 实现了双通道投递策略：

```
RouteByUser (单播用户)
  ├── 在线 → directPush (gRPC 直连, < 50ms)
  │   ├── 全部成功 → MarkDelivered → 返回
  │   ├── 部分失败 → 成功设备 MarkDelivered + 失败设备 Kafka 补推
  │   └── 全部失败 → 全量降级 Kafka + Redis 离线队列
  └── 离线 → reliableEnqueue
      ├── Redis ZSET 离线队列（上线后拉取）
      └── Kafka → DeliveryWorker → Comet

RouteByRoom (房间广播)
  ├── Kafka Room Topic → DeliveryWorker → BroadcastRoom
  └── [Kafka 失效] 直接 gRPC BroadcastRoom 兜底

RouteBroadcast (全服广播)
  ├── Kafka Broadcast Topic → DeliveryWorker → Broadcast
  └── [Kafka 失效] 直接 gRPC Broadcast 兜底
```

### 消息投递全链路

```
Client A          Comet 1         Logic          Kafka          Job          Comet 2       Client B
   │                  │               │              │             │              │              │
   │─ WS OpAuth ──────►│               │              │             │              │              │
   │                  │─ gRPC Connect─►│              │             │              │              │
   │◄ WS OpAuthReply ─│◄──────────────│              │             │              │              │
   │                  │               │              │             │              │              │
   │─ HTTP POST /push/mids ──────────►│              │             │              │              │
   │                  │               │─ RouteByUser │             │              │              │
   │                  │               │              │             │              │              │
   │                  │ [在线] directPush(gRPC) ─────────────────────────────►│              │
   │                  │               │              │             │           │─ WS OpRaw ──►│
   │                  │               │              │             │           │              │
   │                  │ [离线] ───────►│─ Produce ───►│─ Consume ──►│─ PushMsg ─►│─ WS OpRaw ──►│
```

## 核心特性

### 可靠性
- **双通道投递**: 在线 gRPC 直连（< 50ms），离线/失败 Kafka 可靠补偿，消息零丢失
- **消息 ACK + 状态追踪**: pending → delivered → acked，全链路可观测，支持指数退避重试
- **消息幂等**: Redis HSETNX 原子去重 + 64 分片内存 TTL 缓存，同一 msgID 只投递一次
- **离线消息同步**: Redis ZSET 按时间排序存储，用户上线自动拉取，支持分页同步

### 性能
- **百万并发连接**: CityHash 分桶 + 优先级发送队列，单机 10W+ 长连接
- **房间消息聚合**: RoomAggregator 批量聚合房间消息，减少 gRPC 调用次数
- **令牌桶限流**: 每连接独立限流，防止单用户打爆网关
- **Snowflake ID**: 分布式唯一消息 ID，支持按用户 ID 作为 Kafka partition key 保证有序

### 多端 & 会话
- **多设备共存**: Web/App/PC 可同时在线，消息同步推送到所有设备
- **同设备互踢**: 相同 device_id 只保留最新连接，防止消息错乱
- **统一 Session 体系**: user_id × device_id × server × room_key 四维索引
- **心跳自动续期**: 应用层心跳 + 服务端超时断开，精确感知上下线

### 可观测性
- **Prometheus 指标**: 连接数、推送吞吐、延迟分布、ACK 率、消息队列深度
- **OpenTelemetry 链路追踪**: gRPC/HTTP 自动埋点，OTLP 导出
- **结构化日志**: zap 高性能日志，按请求 trace_id 串联

### 扩展性
- **MQ 抽象层**: Producer/Consumer 接口，当前实现为 Kafka，可替换为 Redis Streams / NATS
- **区域感知路由**: 按客户端 IP 地理位置优先路由到同区域 Comet，减少跨机房延迟
- **水平扩容**: Comet/Logic/Job 三层独立扩缩容，Discovery 自动感知节点上下线

## 快速开始

### Docker Compose 一键部署

```bash
docker compose up -d
```

启动后访问：

| 服务 | 地址 |
|------|------|
| **Order Tracker** (电商 Demo 前端) | `file://web/order-tracker/index.html` |
| Notify Server HTTP API | `http://localhost:3121/api/platform/stats` |
| Logic HTTP API | `http://localhost:3111/goim/online/total` |
| Comet WebSocket | `ws://localhost:3102/sub` |
| Prometheus Metrics | `http://localhost:3111/metrics` |

### 手动构建

```bash
make build          # 构建 comet, logic, job, notify-server
make build-notify   # 仅构建 notify-server
```

### 电商 Demo 快速体验

```bash
# 1. 启动全栈
docker compose up -d

# 2. 启动 Notify Server
target/notify-server -conf=cmd/notify-server/notify-example.toml

# 3. 启动订单生命周期 Demo（一条订单在 30s 内走完完整流程）
curl -X POST localhost:3121/api/simulate/start \
  -H 'Content-Type: application/json' \
  -d '{"mode":"lifecycle"}'

# 4. 启动秒杀压测（5 万用户 2 秒内推送）
curl -X POST localhost:3121/api/simulate/start \
  -H 'Content-Type: application/json' \
  -d '{"mode":"flash_sale","qps":5000,"users":50000}'

# 5. 查看实时统计
curl localhost:3121/api/platform/stats

# 6. 打开 web/order-tracker/index.html 查看实时通知效果
```

## HTTP API

### goim 消息推送 API（Logic :3111）

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/goim/push/keys` | 按连接 key 推送（精确设备推送） |
| POST | `/goim/push/mids` | 按用户 ID 推送（多设备广播） |
| POST | `/goim/push/room` | 房间广播 |
| POST | `/goim/push/all` | 全服广播 |
| GET | `/goim/sync` | 同步离线消息 |
| GET | `/goim/online/top` | 热门房间 |
| GET | `/goim/online/room` | 房间在线数 |
| GET | `/goim/online/total` | 总连接数 |
| GET | `/goim/nodes/weighted` | Comet 节点列表（加权） |
| GET | `/metrics` | Prometheus 指标 |

### Notify Server 业务 API（:3121）

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/order/create` | 创建订单 + 推送通知 |
| POST | `/api/order/status-change` | 订单状态变更 + 推送通知 |
| GET | `/api/orders/:order_id` | 查询订单详情 |
| GET | `/api/orders/user/:uid` | 查询用户所有订单 |
| POST | `/api/flash-sale/notify` | 秒杀通知（支持广播/批量/单推） |
| POST | `/api/logistics/update` | 物流信息更新通知 |
| GET | `/api/user/:uid/notifications` | 用户通知历史 |
| GET | `/api/platform/stats` | 平台聚合指标 |
| POST | `/api/simulate/start` | 启动负载模拟 |
| POST | `/api/simulate/stop` | 停止模拟 |
| GET | `/api/simulate/status` | 模拟器状态 |

## 压测数据

```bash
# 连接压测
go run benchmarks/conn_bench.go -host=localhost:3101 -count=1000

# 推送压测
go run benchmarks/push_bench.go -logic-host=localhost:3111 -comet-host=localhost:3102
```

| 指标 | 数值 |
|------|------|
| 在线连接数 | 1,000,000 |
| 测试时长 | 15 min |
| 房间广播频率 | 40/s |
| 消息接收吞吐 | 35,900,000/s |

[详细压测数据 (中文)](./docs/benchmark_cn.md) | [Details (English)](./docs/benchmark_en.md)

## 项目结构

```
goim/
├── api/                          # Protobuf 定义 + 生成代码
│   ├── comet/                    # Comet gRPC (PushMsg, Broadcast, BroadcastRoom)
│   ├── logic/                    # Logic gRPC (Connect, Heartbeat, Receive, SyncOffline)
│   └── protocol/                 # 二进制协议 (22 种操作码)
├── benchmarks/                   # 压测工具
├── cmd/
│   ├── comet/main.go             # Comet 网关入口
│   ├── logic/main.go             # Logic 业务层入口
│   ├── job/main.go               # Job Kafka 消费者入口
│   └── notify-server/main.go     # Notify Server 电商 Demo 入口
├── deploy/                       # Docker 配置 + Discovery
├── internal/
│   ├── comet/                    # 连接层: TCP/WS Server, Bucket, Channel, Room
│   ├── logic/                    # 业务层: Session, Sync, Push, ACK
│   ├── router/                   # 消息路由引擎 (双通道决策 + 幂等)
│   ├── worker/                   # DeliveryWorker (Kafka → gRPC Comet)
│   ├── mq/                       # MQ 抽象 (Producer/Consumer 接口 + Kafka 实现)
│   ├── notify/                   # 电商通知平台 (Demo 业务层)
│   │   ├── server.go             # Gin HTTP Server
│   │   ├── handler/              # API 处理器 (order, flash_sale, logistics, platform)
│   │   ├── model/                # 数据模型 (Order, Notification, FlashSale)
│   │   ├── service/              # 业务服务 (PushClient, OrderNotify, FlashSale)
│   │   └── simulator/            # 负载模拟引擎 (lifecycle/normal/peak/flash_sale)
│   └── grpcx/                    # gRPC 拦截器
├── pkg/                          # 公共工具包
│   ├── bufio/                    # TCP 缓冲 I/O
│   ├── bytes/                    # 字节缓冲池
│   ├── encoding/binary/          # 大端序编解码
│   ├── metrics/                  # Prometheus 指标
│   ├── ratelimit/                # 令牌桶限流
│   ├── snowflake/                # Snowflake 分布式 ID
│   ├── time/                     # Timer/Duration 封装
│   ├── tracing/                  # OpenTelemetry 初始化
│   └── websocket/                # WebSocket 封装
├── web/order-tracker/            # 订单追踪 SPA Demo
│   └── index.html
├── docker-compose.yml
├── Dockerfile
└── Makefile
```

## 配置

| 服务 | 配置文件 |
|------|----------|
| Comet | `cmd/comet/comet-example.toml` |
| Logic | `cmd/logic/logic-example.toml` |
| Job | `cmd/job/job-example.toml` |
| Notify | `cmd/notify-server/notify-example.toml` |

## 业务场景

goim 作为消息中台，向上支撑多种实时推送场景：

| 场景 | 推送模式 | 并发特征 |
|------|----------|----------|
| **订单状态通知** | PushByKeys（单用户多设备） | 稳态，按用户粒度 |
| **秒杀/促销推送** | PushByMids（批量）/ PushRoom（广播） | 突增，万级 QPS |
| **物流追踪更新** | PushByKeys（单用户） | 高频流式 |
| **直播间弹幕** | PushRoom（房间广播）+ 聚合 | 持续高吞吐 |
| **IM 聊天消息** | PushByKeys（单播）+ 离线队列 | 稳态，可靠送达 |
| **系统公告** | PushAll（全服广播） | 低频，全量覆盖 |

## License

MIT
