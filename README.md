# goim v3.0

[![Language](https://img.shields.io/badge/Language-Go-blue.svg)](https://golang.org/)
[![Build Status](https://github.com/Terry-Mao/goim/workflows/Go/badge.svg)](https://github.com/Terry-Mao/goim/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/Terry-Mao/goim)](https://goreportcard.com/report/github.com/Terry-Mao/goim)

goim 是一个高性能分布式 IM（即时通讯）服务端，基于 Go 语言实现，支持单播、多播、广播消息推送，支持 TCP/WebSocket 双协议接入。

[English](./README_en.md) | [中文详细文档](./README_cn.md)

## 架构

### 系统拓扑

```
                ┌─────────────┐
                │   Clients    │  (Web / Android / iOS)
                └──────┬──────┘
                   TCP │ WebSocket
           ┌───────────┴───────────┐
           ▼                       ▼
    ┌──────────────┐       ┌──────────────┐
    │    Comet     │  ...  │    Comet N   │  连接层
    │ TCP:3101     │       │              │  (Connection Gateway)
    │ WS :3102     │       │              │
    │ gRPC:3109    │       │              │
    └──────┬───────┘       └──────┬───────┘
           │ gRPC                  │
           ▼                       ▼
    ┌──────────────────────────────────┐
    │             Logic                 │  业务层
    │        HTTP:3111 gRPC:3119       │  (Business Layer)
    └──┬──────────────┬───────────┬────┘
       │              │           │
       ▼              ▼           ▼
    ┌──────┐    ┌─────────┐   ┌──────────┐
    │ Redis │    │  Kafka  │   │Discovery │  基础设施
    └──────┘    └────┬────┘   └──────────┘
                     │
                     ▼
    ┌──────────────────────────────────┐
    │             Job                   │  消费层
    │    DeliveryWorker (Kafka → gRPC) │  (Consumer / DeliveryWorker)
    └──────────────────────────────────┘
```

- **Comet** — 连接网关，维护 TCP/WebSocket 长连接，提供 gRPC 接口供上层推送
- **Logic** — 业务逻辑层，处理认证、心跳、会话管理、消息路由
- **Job** — Kafka 消费者（DeliveryWorker），将消息可靠投递到 Comet
- **Discovery** — 服务注册发现（基于 [bilibili/discovery](https://github.com/Terry-Mao/goim/tree/main/third_party/discovery)）

### 消息路由引擎 (DispatchEngine)

v3 核心是 `internal/router/` 中的 **DispatchEngine**，实现了双通道消息投递：

```
RouteByUser (单播)
  ├── 在线 → directPush (gRPC 直连, < 50ms)
  │   ├── 全部成功 → MarkDelivered → 返回
  │   ├── 部分失败 → MarkDelivered + 失败设备走 Kafka 补推
  │   └── 全部失败 → 全量降级到 Kafka + 离线队列
  └── 离线 → reliableEnqueue
      ├── Redis ZSET 离线队列 (用户上线后拉取)
      └── Kafka → DeliveryWorker → Comet

RouteByRoom (房间广播)
  ├── Kafka Room Topic → DeliveryWorker → BroadcastRoom
  └── [Kafka 失效] 直接 gRPC BroadcastRoom 兜底

RouteBroadcast (全服广播)
  ├── Kafka Broadcast Topic → DeliveryWorker → Broadcast
  └── [Kafka 失效] 直接 gRPC Broadcast 兜底
```

### 消息推送流程

```
Client A                Comet 1          Logic           Kafka          Job           Comet 2       Client B
   │                       │               │               │             │               │              │
   │── WS OpAuth ──────────►│               │               │             │               │              │
   │                       │── gRPC Connect │               │             │               │              │
   │                       │◄──────────────│               │             │               │              │
   │◄─ WS OpAuthReply ─────│               │               │             │               │              │
   │                       │               │               │             │               │              │
   │── HTTP POST /push/mids ──────────────►│               │             │               │              │
   │                       │               │── RouteByUser │             │               │              │
   │                       │               │               │             │               │              │
   │                       │  [在线] directPush(gRPC) ──────────────────────────────────►│              │
   │                       │               │               │             │               │── WS OpRaw ──►│
   │                       │               │               │             │               │              │
   │                       │  [离线] ──────►│── Produce ───►│── Consume──►│── PushMsg ───►│── WS OpRaw ──►│
   │                       │               │               │             │               │              │
```

## 特性

- **双通道推送**: 在线走 gRPC 直连（< 50ms），离线走 Kafka + Redis 离线队列，保证消息不丢
- **消息幂等**: Redis HSETNX 原子去重，同一 msgID 只投递一次
- **消息 ACK + 状态追踪**: 全链路消息状态 (pending → delivered → acked)，支持指数退避重试
- **离线消息同步**: Redis ZSET 存储，用户上线自动拉取
- **多设备 Session**: 同设备互踢，跨设备共存，心跳自动续期
- **双协议接入**: TCP（自定义二进制协议） + WebSocket
- **MQ 抽象层**: Producer/Consumer 接口，当前实现为 Kafka，可替换
- **房间消息聚合**: RoomAggregator 批量聚合房间消息后发送，减少 gRPC 调用
- **Prometheus 监控**: 连接数、推送吞吐、延迟、ACK 状态等指标
- **OpenTelemetry 链路追踪**: gRPC/HTTP 自动埋点，支持 OTLP 导出
- **令牌桶限流**: 每连接独立限流
- **Snowflake ID**: 分布式唯一消息 ID
- **区域感知负载均衡**: 按 IP 地理位置优先路由到同区域 Comet

## 快速开始

### Docker Compose 一键部署

```bash
docker compose up -d
```

服务启动后访问：

| 服务 | 地址 |
|------|------|
| Logic HTTP API | `http://localhost:3111/goim/online/total` |
| WebSocket 聊天 Demo | `http://localhost:8080` |
| Comet WebSocket | `ws://localhost:3102/sub` |
| Discovery | `http://localhost:7171/discovery/fetch?env=dev&appid=goim.comet` |

### 手动构建

```bash
# 构建
make build

# 启动（需先启动 Redis、Kafka、Discovery）
nohup target/logic -conf=target/logic.toml -region=sh -zone=sh001 -deploy.env=dev -weight=10 &
nohup target/comet -conf=target/comet.toml -region=sh -zone=sh001 -deploy.env=dev -weight=10 -addrs=127.0.0.1 &
nohup target/job -conf=target/job.toml -region=sh -zone=sh001 -deploy.env=dev &
```

## HTTP API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/goim/push/keys` | 按连接 key 推送 |
| POST | `/goim/push/mids` | 按用户 ID 推送 |
| POST | `/goim/push/room` | 房间广播 |
| POST | `/goim/push/all` | 全局广播 |
| GET | `/goim/sync` | 同步离线消息 |
| GET | `/goim/online/top` | 热门房间 |
| GET | `/goim/online/room` | 房间在线数 |
| GET | `/goim/online/total` | 总连接数 |
| GET | `/goim/nodes/weighted` | Comet 节点列表（加权） |
| GET | `/metrics` | Prometheus 指标 |

## 配置

配置文件模板在 `cmd/*/` 目录下：

| 服务 | 配置文件 |
|------|----------|
| Comet | `cmd/comet/comet-example.toml` |
| Logic | `cmd/logic/logic-example.toml` |
| Job | `cmd/job/job-example.toml` |

## 目录结构

```
goim/
├── api/                        # Protobuf 定义 + Go 生成代码
│   ├── comet/                  # Comet gRPC 服务 (PushMsg, Broadcast, BroadcastRoom, Rooms)
│   ├── logic/                  # Logic gRPC 服务 (Connect, Heartbeat, Receive, SyncOffline...)
│   └── protocol/               # 二进制协议 (22 种操作码) + 消息体结构
├── benchmarks/                 # 压测工具
│   ├── client/                 # TCP 测试客户端
│   ├── conn_bench/             # 连接压测
│   ├── push_bench/             # 推送压测
│   └── push_room/              # 房间广播压测
├── cmd/                        # 服务入口
│   ├── comet/main.go           # Comet (TCP/WS 网关, v2.0.0)
│   ├── logic/main.go           # Logic (业务逻辑, v2.0.0)
│   └── job/main.go             # Job (Kafka 消费者, v3.0.0)
├── deploy/                     # 部署配置
│   ├── configs/                # Docker 环境 TOML 配置
│   ├── discovery/              # 简化版 Discovery 服务
│   └── nginx.conf
├── examples/
│   ├── chat-demo/              # 浏览器聊天 Demo
│   ├── e2e_test_client/        # 端到端 TCP 测试客户端
│   └── javascript/             # JS WebSocket 客户端
├── internal/                   # 核心业务逻辑（私有包）
│   ├── comet/                  # 连接层：TCP/WS Server, Bucket, Channel, Room, Timer Wheel
│   ├── grpcx/                  # gRPC 拦截器（panic recovery + 请求日志）
│   ├── logic/                  # 业务层
│   │   ├── dao/                # 数据访问 (Redis + Kafka 旧路径)
│   │   ├── grpc/               # gRPC LogicServer 实现
│   │   ├── http/               # Gin HTTP 路由 + 中间件
│   │   ├── model/              # 数据模型 (Online, Room, Metadata)
│   │   └── service/            # 服务层 (SessionManager, SyncService)
│   ├── mq/                     # 消息队列抽象 (Producer/Consumer 接口)
│   │   └── kafka/              # Kafka 实现 (producer, consumer, DLQ)
│   ├── router/                 # DispatchEngine — 消息路由引擎 (RouteByUser/RouteByRoom/RouteBroadcast)
│   └── worker/                 # DeliveryWorker — Kafka 消费调度 (pushKeys, broadcast, RoomAggregator)
├── pkg/                        # 公共工具包
│   ├── bufio/                  # TCP 缓冲 I/O
│   ├── bytes/                  # 字节缓冲池 + Writer
│   ├── encoding/binary/        # 大端序编解码
│   ├── ip/                     # 内网 IP 检测
│   ├── log/                    # 文件 + stderr 日志
│   ├── metrics/                # Prometheus 指标定义
│   ├── ratelimit/              # 令牌桶限流
│   ├── snowflake/              # Snowflake 分布式 ID
│   ├── strings/                # Int slice 工具
│   ├── time/                   # Timer/Duration 封装
│   ├── tracing/                # OpenTelemetry Tracer 初始化
│   └── websocket/              # WebSocket 连接封装
├── third_party/discovery/      # 本地 vendored bilibili/discovery
├── docker-compose.yml          # 7 服务一键部署
├── Dockerfile                  # 多阶段构建
└── Makefile
```

## 压测

```bash
# 连接压测
go run benchmarks/conn_bench.go -host=localhost:3101 -count=1000

# 推送压测
go run benchmarks/push_bench.go -logic-host=localhost:3111 -comet-host=localhost:3102
```

### 历史数据

| 指标 | 数值 |
|------|------|
| 在线连接数 | 1,000,000 |
| 测试时长 | 15 分钟 |
| 房间广播频率 | 40/s |
| 消息接收吞吐 | 35,900,000/s |

[详细数据 (中文)](./docs/benchmark_cn.md) | [Detailed (English)](./docs/benchmark_en.md)

## SDK

- Android: [goim-sdk](https://github.com/roamdy/goim-sdk)
- iOS: [goim-oc-sdk](https://github.com/roamdy/goim-oc-sdk)

## License

goim is distributed under the terms of the MIT License.
