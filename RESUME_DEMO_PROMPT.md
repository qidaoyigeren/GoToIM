# goim 简历演示增强 — 4 个 Prompt

以下是 4 个独立 prompt，每个都可以单独交给 AI/agent 执行。按编号顺序执行效果最好。

---

## Prompt 1: Docker Compose 一键部署

```
## 任务

为 goim 项目编写 docker-compose.yml 和配套配置，实现一键启动全部服务。

## 项目背景

goim 是一个 IM 即时通讯服务端，包含 4 个微服务：

| 服务 | 目录 | 端口 | 依赖 |
|------|------|------|------|
| discovery（服务发现） | 需要自行构建 | 7171 | 无 |
| comet（连接层，TCP/WS） | cmd/comet | 3101(TCP) 3102(WS) 3109(gRPC) | discovery + logic |
| logic（业务层） | cmd/logic | 3111(HTTP) 3119(gRPC) | discovery + Redis + Kafka |
| job（消费层） | cmd/job | 无 | discovery + Kafka |

所有服务通过 `github.com/bilibili/discovery` 做服务注册发现（`discovery:///` gRPC resolver）。
Redis 端口 6379，Kafka 端口 9092。
配置文件模板在 `cmd/*/xxx-example.toml` 和 `target/*.toml`。

## 要求

1. 编写一个简单的 Discovery HTTP 服务器（Go 代码），监听 7171 端口，提供 `/discovery/register` 和 `/discovery/fetch` 两个接口，能注册实例和拉取实例列表即可。不需要完整实现 bilibili/discovery 协议，只要 goim 的 naming client 能连通。

2. 编写 docker-compose.yml，包含 6 个容器：
   - redis:7-alpine
   - kafka（用 bitnami/kafka 单节点即可，不需要 zookeeper）
   - discovery（上面写的简单服务）
   - comet
   - logic
   - job

3. comet/logic/job 用多阶段 Dockerfile 构建：golang:1.25-alpine 编译，alpine 运行。

4. 各服务的配置文件挂载为 volume，Kafka topics 在启动脚本中自动创建。

5. docker-compose up 后 30 秒内所有服务进入 ready 状态，comet 和 logic 的日志中能看到注册成功的信息。

## 输出

- docker-compose.yml
- Dockerfile（多阶段构建，3 个服务共用）
- cmd/discovery/main.go（简单的服务发现实现）
- 各服务的配置文件（comet.toml / logic.toml / job.toml）
- scripts/create-topics.sh（自动创建 Kafka topics）
```

---

## Prompt 2: WebSocket 聊天 Demo 页面

```
## 任务

为 goim 创建一个单页 WebSocket 聊天 demo，用于浏览器端演示 IM 实时消息推送。

## 项目背景

goim Comet 服务在 3102 端口提供 WebSocket 连接。客户端协议：

1. **握手包**：连接后先发送一个认证包（packLen(4) + headerLen(2) + ver(2) + op(4) + seq(4) + body）
   - op=7（OpAuth），body 为 JSON `{"mid":1001,"key":"xxx","room":"live://1000","platform":"web","accepts":[4,9,18,19]}`
2. **心跳**：每 4 分钟发送 op=2 的包（空 body）
3. **接收消息**：服务器下发的 op=9（OpRaw）和 op=3（心跳回复）

包的二进制格式（大端序）：
```
[packLen:4字节] [headerLen:2字节,固定16] [ver:2字节,固定1] [op:4字节] [seq:4字节] [body:剩余字节]
```
packLen = headerLen + bodyLen

## 要求

1. 写一个 `cmd/websocket_demo/main.go`：
   - 启动一个 HTTP 服务器（端口 8080）
   - 提供静态文件服务（demo 页面）
   - 提供一个 `/api/auth` 接口，返回测试账号的认证信息（mid、key、room）

2. 写一个 `cmd/websocket_demo/static/index.html`（单文件，内嵌 CSS + JS）：
   - 两个浏览器窗口分别模拟用户 A（mid=1001，name="Alice"）和用户 B（mid=1002，name="Bob"）
   - 使用下拉框切换当前显示的账号，避免用户开两个浏览器
   - 左侧联系人列表，右侧聊天窗口
   - 聊天框输入消息 → 通过 WebSocket 发送（op=4, OpSendMsg, body 用 MsgBody JSON 编码）
   - 收到的消息自动显示在聊天区域，区分自己和对方的样式（左右对齐、不同颜色气泡）
   - 显示连接状态指示器（绿色圆点=已连接，红色=断开）
   - 页面顶部显示当前账号信息（mid + name）

3. MsgBody 的 JSON 格式：
   ```json
   {"msg_id":"xxx","from_uid":1001,"to_uid":1002,"timestamp":1700000000000,"seq":1,"content":"hello"}
   ```

4. 同时打开两个浏览器标签页（或把页面分成左右两栏），A 发消息 → B 实时收到。

## 输出

- cmd/websocket_demo/main.go
- cmd/websocket_demo/static/index.html
- 启动说明（go run 即可，不需要 docker）
```

---

## Prompt 3: 架构图 + README 重写

```
## 任务

为 goim 项目重新编写 README.md，并生成架构图。

## 项目背景

该项目基于 goim v2.0 进行了大量改进。核心架构：

```
┌──────────┐    TCP/WS     ┌──────────┐    gRPC     ┌──────────┐
│  Client  │ ───────────→  │  Comet   │ ────────→  │  Logic   │
│ (Browser)│ ←───────────  │(连接层)   │ ←────────  │(业务层)   │
└──────────┘               └──────────┘            └──────────┘
                                  ↑                      │
                                  │ gRPC Push            │ Kafka
                                  │                      ↓
                            ┌──────────┐            ┌──────────┐
                            │   Job    │ ←─────── │  Kafka   │
                            │(消费层)   │            └──────────┘
                            └──────────┘
                                                       ↑
                            ┌──────────┐               │
                            │  Redis   │ ←─────────────┘
                            └──────────┘
```

关键特性：
- 双通道推送：在线直连 gRPC（<50ms）+ 离线 Kafka 降级
- 消息 ACK + 指数退避重试（最多 3 次）
- 离线消息同步（Redis ZSET + 上线回放）
- 多设备 Session 管理（同设备互踢）
- Message Router + Delivery Worker（v3 架构）
- 支持 TCP / WebSocket 双协议
- Prometheus 指标暴露
- 令牌桶限流 + Snowflake ID 生成

## 要求

1. 用 Mermaid 语法画架构图（数据流），放在 README 的 "Architecture" 章节下，包括：
   - 整体架构图（上面的拓扑）
   - 消息推送流程图（双通道决策流程）
   - v3 架构图（Producer → Router → MQ → Worker → Gateway）

2. README 需要包含：
   - 项目简介（一句话说清楚是什么）
   - Badge：Go version / Build Status
   - Architecture（Mermaid 图 + 文字说明）
   - Features（核心特性列表）
   - Quick Start（docker-compose up 一键启动）
   - Configuration（关键配置项说明）
   - API（HTTP 接口列表）
   - Project Structure（目录结构说明）
   - Benchmarks（占位，后续填数据）
   - License

3. 用中文写 README（架构图注释用英文也行）。

## 输出

- README.md（覆盖原文件）
```

---

## Prompt 4: 压力测试脚本 + 数据采集

```
## 任务

为 goim 编写基准测试脚本，测试核心指标并输出人类可读的报告。

## 项目背景

goim Comet 服务在 3101(TCP) 和 3102(WS) 端口接收连接。
goim Logic HTTP API 在 3111 端口：
- POST /goim/push/keys?operation=9 → 推送给指定 key
- POST /goim/push/mids?operation=9&mids=1001,1002 → 推送给指定用户
- GET  /goim/online/top?type=room&limit=10 → 在线统计

## 要求

1. 编写 `benchmarks/conn_bench.go`（连接数压测）：
   - 并发创建 N 个 TCP 连接到 Comet 3101 端口
   - 每个连接完成握手认证（op=7）
   - 维持连接并每 30 秒发送心跳（op=2）
   - 30 秒后输出：成功连接数 / 失败数 / 握手平均延迟 / 服务端内存占用
   - 参数可调：-n 连接数（默认 1000），-addr 地址

2. 编写 `benchmarks/push_bench.go`（推送吞吐压测）：
   - 先创建 100 个连接（准备阶段）
   - 通过 HTTP API 连续推送 N 条消息给这些连接的用户
   - 记录每条消息从 POST 到连接收到的时间（end-to-end 延迟）
   - 输出：P50 / P99 / P999 延迟，QPS，成功率
   - 参数可调：-n 消息数（默认 10000），-c 并发连接数（默认 100）

3. 编写 `benchmarks/run.sh`（一键压测脚本）：
   - 先跑连接压测（1000 → 5000 → 10000 连接）
   - 再跑推送压测（1w → 5w → 10w 条消息）
   - 输出结果到 benchmarks/results/ 目录，JSON 格式
   - 生成一个简单的 HTML 报告页面（表格 + 折线图）

4. 所有压测工具在 Docker 外运行，指向 localhost 的 Comet/Logic 端口。

## 输出

- benchmarks/conn_bench.go
- benchmarks/push_bench.go
- benchmarks/run.sh
- benchmarks/report_template.html（报告模板）
```

---

## 执行顺序建议

```
Prompt 1 (docker-compose) → 验证服务能全量启动
    ↓
Prompt 2 (WebSocket demo) → 验证消息收发全链路
    ↓
Prompt 3 (README + 架构图) → 把前面的成果展示出来
    ↓
Prompt 4 (压测数据) → 填上 README 里的 Benchmarks 章节
```

每个 prompt 预计执行时间 1-2 小时，总计 4-8 小时完成全部简历包装。
