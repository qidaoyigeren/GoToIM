# 消息推送全链路图

## Mermaid 图

```mermaid
flowchart TB
    subgraph 入口层["🔵 入口层 (HTTP API)"]
        A["POST /goim/push/mids<br/>internal/logic/http/push.go<br/>pushMids()"]
    end

    subgraph 路由层["🟡 路由层 (Router)"]
        B["RouteByUser<br/>internal/router/dispatch.go:91"]
        C["TrackMessage<br/>Redis HSETNX 原子争用<br/>internal/router/ack.go:146"]
        D{"在线检测<br/>sessMgr.IsOnline()"}
    end

    subgraph 幂等层["🟢 幂等去重"]
        E1["第一层: 内存分片哈希<br/>64分片 FNV-64a 5min TTL<br/>internal/router/idempotency.go:144"]
        E2["第二层: Redis 状态查询<br/>GetMessageStatus()"]
    end

    subgraph 直连通道["🟣 直连通道 (Fast Path)"]
        F["directPush<br/>internal/router/dispatch.go:266"]
        G["CometPusher.PushMsg<br/>OpRaw 预编码<br/>internal/logic/comet.go:97"]
        H["gRPC 调用<br/>500ms 独立超时"]
    end

    subgraph 可靠通道["🔴 可靠通道 (Slow Path)"]
        I["reliableEnqueue<br/>internal/router/dispatch.go:328"]
        J["ZADD offline:{uid}<br/>Redis ZSet 离线队列<br/>7天TTL"]
        K["Kafka Producer<br/>uid 分区键保证有序<br/>internal/mq/kafka/producer.go:61"]
        L["Kafka Consumer<br/>Consumer Group 消费<br/>internal/mq/kafka/consumer.go:33"]
        M["DeliveryWorker<br/>processMessage()<br/>internal/worker/worker.go:101"]
        N["CometClientPool<br/>fan-out channel 无锁轮转<br/>internal/worker/comet_pool.go:172"]
    end

    subgraph Comet层["🟠 Comet 接入层"]
        O["gRPC Server.PushMsg<br/>internal/comet/grpc/server.go:185"]
        P["Bucket.Get<br/>CityHash32 分片<br/>internal/comet/bucket.go"]
        Q["Channel.NeedPush<br/>客户端 Watch 过滤<br/>internal/comet/channel.go:57"]
        R["Channel.Push<br/>入队 PriorityQueue<br/>internal/comet/channel.go:68"]
    end

    subgraph 推送层["🟤 推送执行层"]
        S["PriorityQueue<br/>High: 控制信令<br/>Normal: 业务消息<br/>internal/comet/priority_queue.go:48"]
        T["Channel.Signal<br/>ProtoReady 唤醒<br/>internal/comet/channel.go:78"]
        U["dispatchTCP 写协程<br/>阻塞等 Pop → Ring Buffer<br/>internal/comet/server_tcp.go:392"]
        V["Ring Buffer<br/>rp/wp 指针管理<br/>满则丢弃<br/>internal/comet/ring.go"]
        W["WriteTCP / WebSocket<br/>写入客户端"]
    end

    subgraph ACK层["⚪ ACK 确认层"]
        X1["客户端 OpPushMsgAck<br/>internal/comet/server_tcp.go:316"]
        X2["Logic.Receive → HandleACK<br/>internal/logic/conn.go"]
        X3["UpdateMessageStatus(acked)<br/>RemoveFromOfflineQueue<br/>PublishACK → Kafka<br/>internal/router/ack.go:103"]
        X4["状态机: pending → delivered → acked<br/>internal/logic/service/ack.go"]
    end

    A --> B
    B --> C
    C --> E1
    E1 -- 未命中 --> E2
    E2 -- 新消息 --> D
    E1 -- 命中/重复 --> Z1["跳过, return nil"]
    E2 -- 已处理 --> Z1
    C -- HSETNX 失败 --> Z1

    D -- 在线 --> F
    D -- 离线 --> I
    F -- 全部成功 --> Y1["MarkDelivered<br/>return nil"]
    F -- 部分失败 --> Y2["MarkDelivered + 失败设备<br/>走可靠通道补推"]
    F -- 全部失败 --> I
    Y2 --> I

    F --> G
    G --> H
    H --> O

    I --> J
    I --> K
    K --> L
    L --> M
    M --> N
    N --> O

    O --> P
    P --> Q
    Q --> R
    R --> S
    S --> T
    T --> U
    U --> V
    V --> W

    W --> X1
    X1 --> X2
    X2 --> X3
    X3 --> X4
```

## 直连通道（Fast Path）简化版

```mermaid
flowchart LR
    A["HTTP API<br/>push.go"] --> B["RouteByUser<br/>dispatch.go"]
    B --> C["TrackMessage<br/>Redis HSETNX<br/>ack.go"]
    C --> D{"在线?"}
    D -->|是| E["directPush<br/>dispatch.go"]
    E --> F["CometPusher<br/>logic/comet.go"]
    F --> G["gRPC → Comet<br/>grpc/server.go"]
    G --> H["Bucket.Channel<br/>CityHash 分片"]
    H --> I["PriorityQueue<br/>priority_queue.go"]
    I --> J["dispatchTCP<br/>server_tcp.go"]
    J --> K["Ring Buffer<br/>ring.go"]
    K --> L["WriteTCP<br/>→ Client"]
```

## 可靠通道（Slow Path）简化版

```mermaid
flowchart LR
    A["直连失败 / 用户离线"] --> B["reliableEnqueue<br/>dispatch.go"]
    B --> C["ZADD offline:{uid}<br/>Redis ZSet"]
    B --> D["Kafka Producer<br/>kafka/producer.go"]
    D --> E["Kafka Consumer<br/>kafka/consumer.go"]
    E --> F["DeliveryWorker<br/>worker/worker.go"]
    F --> G["CometClientPool<br/>fan-out 无锁轮转<br/>worker/comet_pool.go"]
    G --> H["gRPC → Comet"]
    H --> I["... PriorityQueue<br/>→ dispatchTCP<br/>→ Client"]
```

## 消息状态机

```mermaid
stateDiagram-v2
    [*] --> Pending: HSETNX 抢占成功
    Pending --> Delivered: 推送成功 (MarkDelivered)
    Pending --> Failed: 多次重试失败 (MarkFailed)
    Delivered --> Acked: 客户端 ACK (HandleACK)
    Delivered --> Failed: 超时未 ACK
    Acked --> [*]: 终态
    Failed --> [*]: 终态

    note right of Pending
        已写入 Redis
        等待推送
    end note
    note right of Delivered
        Socket 已写入
        等待客户端 ACK
    end note
    note right of Acked
        客户端已确认
        移除离线队列
    end note
    note right of Failed
        投递失败
        不再重试
    end note
```

## 并发模型

```mermaid
flowchart TB
    subgraph Comet["Comet 实例"]
        subgraph Buckets["32 个 Bucket (CityHash 分片)"]
            B0["Bucket[0]"]
            B1["Bucket[1]"]
            B31["Bucket[31]"]
        end
    end

    subgraph Channel["单个 Channel"]
        direction LR
        G1["读协程<br/>ServeTCP"]
        G2["PriorityQueue<br/>high / normal"]
        G3["写协程<br/>dispatchTCP"]
        G4["Ring Buffer<br/>CliProto"]
    end

    G1 -- "Signal()" --> G2
    G2 -- "Ready()/Pop()" --> G3
    G1 -- "SetAdv()" --> G4
    G3 -- "Get()/GetAdv()" --> G4
```

---

## D2 图

```d2
direction: right

# ==================== 层级定义 ====================
layers: {
  entry: {
    label: "入口层 (HTTP API)"
    position: top-left
  }
  router: {
    label: "路由层 (Router)"
  }
  idempotent: {
    label: "幂等去重"
  }
  direct: {
    label: "直连通道 (Fast Path)"
  }
  reliable: {
    label: "可靠通道 (Slow Path)"
  }
  comet: {
    label: "Comet 接入层"
  }
  dispatch: {
    label: "推送执行层"
  }
  ack: {
    label: "ACK 确认层"
  }
}

# ==================== 入口层 ====================
entry.api: "POST /goim/push/mids\ninternal/logic/http/push.go\npushMids()" {
  shape: hexagon
  style.fill: "#E3F2FD"
}

# ==================== 路由层 ====================
router.dispatch: "RouteByUser\ninternal/router/dispatch.go:91" {
  shape: diamond
  style.fill: "#FFF9C4"
}

router.track: "TrackMessage\nRedis HSETNX 原子争用\ninternal/router/ack.go:146" {
  shape: diamond
  style.fill: "#FFF9C4"
}

# ==================== 幂等层 ====================
idempotent.memory: "第一层: 内存分片哈希\n64分片 FNV-64a 5min TTL\ninternal/router/idempotency.go" {
  shape: rectangle
  style.fill: "#C8E6C9"
}

idempotent.redis: "第二层: Redis 状态查询\nGetMessageStatus()" {
  shape: rectangle
  style.fill: "#C8E6C9"
}

# ==================== 直连通道 ====================
direct.push: "directPush\ninternal/router/dispatch.go:266" {
  shape: rectangle
  style.fill: "#E1BEE7"
}

direct.comet_pusher: "CometPusher.PushMsg\nOpRaw 预编码\ninternal/logic/comet.go:97" {
  shape: rectangle
  style.fill: "#E1BEE7"
}

direct.grpc_call: "gRPC 调用\n500ms 独立超时" {
  shape: rectangle
  style.fill: "#E1BEE7"
}

# ==================== 可靠通道 ====================
reliable.enqueue: "reliableEnqueue\ninternal/router/dispatch.go:328" {
  shape: rectangle
  style.fill: "#FFCDD2"
}

reliable.zadd: "ZADD offline:{uid}\nRedis ZSet 离线队列\n7天TTL" {
  shape: cylinder
  style.fill: "#FFCDD2"
}

reliable.kafka_producer: "Kafka Producer\nuid 分区键保证有序\ninternal/mq/kafka/producer.go" {
  shape: rectangle
  style.fill: "#FFCDD2"
}

reliable.kafka_consumer: "Kafka Consumer\nConsumer Group 消费\ninternal/mq/kafka/consumer.go" {
  shape: rectangle
  style.fill: "#FFCDD2"
}

reliable.worker: "DeliveryWorker\nprocessMessage()\ninternal/worker/worker.go" {
  shape: rectangle
  style.fill: "#FFCDD2"
}

reliable.pool: "CometClientPool\nfan-out channel 无锁轮转\ninternal/worker/comet_pool.go" {
  shape: rectangle
  style.fill: "#FFCDD2"
}

# ==================== Comet 层 ====================
comet.grpc: "gRPC Server.PushMsg\ninternal/comet/grpc/server.go:185" {
  shape: rectangle
  style.fill: "#FFE0B2"
}

comet.bucket: "Bucket.Get\nCityHash32 分片\ninternal/comet/bucket.go" {
  shape: rectangle
  style.fill: "#FFE0B2"
}

comet.channel: "Channel\nNeedPush + Push\ninternal/comet/channel.go" {
  shape: rectangle
  style.fill: "#FFE0B2"
}

# ==================== 推送执行层 ====================
dispatch.pq: "PriorityQueue\nHigh: 控制信令\nNormal: 业务消息\ninternal/comet/priority_queue.go" {
  shape: queue
  style.fill: "#D7CCC8"
}

dispatch.signal: "Channel.Signal\nProtoReady 唤醒\ninternal/comet/channel.go:78" {
  shape: rectangle
  style.fill: "#D7CCC8"
}

dispatch.tcp: "dispatchTCP 写协程\n阻塞等 Pop → Ring Buffer\ninternal/comet/server_tcp.go:392" {
  shape: rectangle
  style.fill: "#D7CCC8"
}

dispatch.ring: "Ring Buffer\nrp/wp 指针管理\n满则丢弃\ninternal/comet/ring.go" {
  shape: rectangle
  style.fill: "#D7CCC8"
}

dispatch.client: "WriteTCP / WebSocket\n客户端收到消息" {
  shape: hexagon
  style.fill: "#D7CCC8"
}

# ==================== ACK 层 ====================
ack.client_ack: "客户端 OpPushMsgAck\ninternal/comet/server_tcp.go:316" {
  shape: rectangle
  style.fill: "#BDBDBD"
}

ack.handle: "HandleACK\nUpdateMessageStatus → RemoveOffline\n→ PublishACK → Kafka\ninternal/router/ack.go:103" {
  shape: rectangle
  style.fill: "#BDBDBD"
}

ack.state: "状态机\npending → delivered → acked\ninternal/logic/service/ack.go" {
  shape: class
  style.fill: "#BDBDBD"
}

# ==================== 连接关系 ====================

# 主路径
entry.api -> router.dispatch -> router.track

router.track -> idempotent.memory: "检查内存缓存"
idempotent.memory -> idempotent.redis: "未命中"
idempotent.memory -> router.dispatch: "命中→跳过"
idempotent.redis -> router.dispatch: "新消息"

# 在线 → 直连通道
router.track -> direct.push: "用户在线"
direct.push -> direct.comet_pusher
direct.comet_pusher -> direct.grpc_call
direct.grpc_call -> comet.grpc

# 离线/失败 → 可靠通道
router.track -> reliable.enqueue: "离线 / 直连失败"
reliable.enqueue -> reliable.zadd: "写入离线队列"
reliable.enqueue -> reliable.kafka_producer: "投递 Kafka"
reliable.kafka_producer -> reliable.kafka_consumer
reliable.kafka_consumer -> reliable.worker
reliable.worker -> reliable.pool
reliable.pool -> comet.grpc: "gRPC"

# Comet 内部路径
comet.grpc -> comet.bucket
comet.bucket -> comet.channel
comet.channel -> dispatch.pq: "Push"
comet.channel -> dispatch.signal: "Signal"
dispatch.signal -> dispatch.tcp: "唤醒"
dispatch.tcp -> dispatch.ring: "读取"
dispatch.ring -> dispatch.client: "写入"

# ACK 回路
dispatch.client -> ack.client_ack: "客户端 ACK"
ack.client_ack -> ack.handle
ack.handle -> ack.state
```

---

## 核心决策逻辑 D2 图

```d2
direction: right

decision: "RouteByUser\n双通道决策\ninternal/router/dispatch.go:91" {
  shape: diamond
  style.fill: "#FFF9C4"
}

online: "IsOnline?\nsessMgr.IsOnline()" {
  shape: diamond
  style.fill: "#C8E6C9"
}

direct: "直连通道 (Fast Path)\n────────────\ndirectPush()\ngRPC → Comet\n500ms 超时" {
  shape: rectangle
  style.fill: "#E1BEE7"
}

reliable: "可靠通道 (Slow Path)\n────────────\nreliableEnqueue()\nZADD 离线队列 + Kafka\n→ Worker → Comet" {
  shape: rectangle
  style.fill: "#FFCDD2"
}

result_ok: "全部成功\n→ MarkDelivered\n→ return nil" {
  shape: rectangle
  style.fill: "#C8E6C9"
  style.border-radius: 20
}

result_partial: "部分失败\n→ MarkDelivered\n→ 失败设备走可靠通道" {
  shape: rectangle
  style.fill: "#FFF9C4"
  style.border-radius: 20
}

result_fail: "全部失败\n→ 全量走可靠通道" {
  shape: rectangle
  style.fill: "#FFCDD2"
  style.border-radius: 20
}

decision -> online: "HSETNX 抢占成功"
online -> direct: "在线"
online -> reliable: "离线"

direct -> result_ok: "全部成功"
direct -> result_partial: "部分失败"
direct -> result_fail: "全部失败"
result_partial -> reliable: "失败设备补推"
result_fail -> reliable: "全量兜底"
```
