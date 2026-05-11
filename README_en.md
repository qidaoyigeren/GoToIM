# goim v3.0 - English Documentation

[![Language](https://img.shields.io/badge/Language-Go-blue.svg)](https://golang.org/)
[![Build Status](https://github.com/Terry-Mao/goim/workflows/Go/badge.svg)](https://github.com/Terry-Mao/goim/actions)

## Overview

goim is a high-performance, horizontally scalable distributed IM (Instant Messaging) server written in Go. Originally open-sourced by Bilibili, it uses a three-tier microservice architecture (Comet / Logic / Job) supporting millions of concurrent connections with sub-50ms message delivery latency.

## Architecture

### Three-Tier Microservices

| Service | Responsibility | Ports | Dependencies |
|---------|---------------|-------|--------------|
| **Comet** | Connection gateway, manages TCP/WebSocket long connections | TCP:3101, WS:3102, gRPC:3109 | Discovery, Logic |
| **Logic** | Business logic, session management, message routing | HTTP:3111, gRPC:3119 | Redis, Kafka, Discovery |
| **Job** | Kafka consumer, dispatches messages to Comet | None | Kafka, Discovery |

### System Topology

```mermaid
graph TB
    subgraph Clients
        C1[Web]
        C2[Android]
        C3[iOS]
    end

    subgraph Comet["Comet Layer"]
        COMET1["Comet 1<br/>TCP:3101 WS:3102 gRPC:3109"]
        COMET2["Comet N"]
    end

    subgraph Logic["Logic Layer"]
        LOGIC["Logic<br/>HTTP:3111 gRPC:3119"]
    end

    subgraph Job["Job Layer"]
        JOB["Job<br/>Kafka Consumer"]
    end

    subgraph Infra["Infrastructure"]
        REDIS[("Redis")]
        KAFKA[("Kafka")]
        DISCOVERY["Discovery<br/>:7171"]
    end

    C1 & C2 & C3 -->|TCP / WebSocket| COMET1 & COMET2
    COMET1 & COMET2 -->|gRPC| LOGIC
    LOGIC -->|gRPC: Push| COMET1 & COMET2
    LOGIC -->|Produce| KAFKA
    KAFKA -->|Consume| JOB
    JOB -->|gRPC: Push| COMET1 & COMET2
    LOGIC --> REDIS
    COMET1 & COMET2 -->|Register| DISCOVERY
    LOGIC -->|Register| DISCOVERY
    JOB -->|Watch| DISCOVERY
```

### Message Push Flow

```mermaid
sequenceDiagram
    participant A as Alice (Client A)
    participant C1 as Comet 1
    participant L as Logic
    participant K as Kafka
    participant J as Job
    participant C2 as Comet 2
    participant B as Bob (Client B)

    A->>C1: WebSocket op=7 (Auth)
    C1->>L: gRPC Connect()
    L->>L: Create Session (Redis)
    L-->>C1: mid, key, heartbeat
    C1-->>A: op=8 (AuthReply)

    Note over A,B: Alice sends message to Bob

    A->>L: POST /goim/push/mids?mids=1002
    L->>L: Lookup Bob's Comet (Redis mapping)

    alt Bob online - Fast path
        L->>K: Produce(push-topic, PushMsg)
        K->>J: Consume
        J->>C2: gRPC PushMsg(keys=[bob-key])
        C2->>B: WebSocket op=9 (OpRaw)
        B->>C2: op=19 (ACK)
        C2->>L: gRPC Receive(ACK)
        L->>L: Update message status (Redis)
    else Bob offline - Reliable path
        L->>L: Store in offline queue (Redis ZSET, 7-day TTL)
        Note over B: Auto-sync when Bob reconnects
    end
```

### Dual-Channel Push Architecture

```mermaid
graph LR
    API["HTTP API<br/>/goim/push/*"] --> R{DispatchEngine}
    R -->|"Online: direct gRPC<br/>< 50ms"| CP[CometPusher]
    R -->|"Offline: degrade"| OFF["Offline Queue<br/>(Redis ZSET)"]
    R -->|"Broadcast: async"| MQ["MQ Producer<br/>(Kafka)"]

    CP -->|PushMsg| COMET["Comet gRPC"]
    OFF -->|"7-day TTL"| SYNC["SyncService<br/>auto-replay on login"]
    MQ -->|Kafka| JOB["Job Consumer"]
    JOB -->|PushMsg / BroadcastRoom| COMET
```

### v3 Architecture: Router + Worker

```mermaid
graph LR
    subgraph Producer
        P["Logic KafkaProducer"]
    end

    subgraph MQ["Message Queue"]
        PUSH["goim-push-topic"]
        ROOM["goim-room-topic"]
        ALL["goim-all-topic"]
        ACK["goim-ack-topic"]
    end

    subgraph Worker["DeliveryWorker"]
        D["Dispatch"]
        CA["CometClientPool"]
        RA["RoomAggregator"]
        AR["ACKReporter"]
    end

    subgraph Gateway
        COMET["Comet gRPC"]
    end

    P --> PUSH & ROOM & ALL
    PUSH & ROOM & ALL --> D
    D --> CA --> COMET
    D --> RA --> COMET
    COMET --> AR --> ACK
```

## Features

- **Dual-Channel Push**: Online users get direct gRPC push (<50ms); offline messages degrade to Kafka + Redis offline queue with auto-replay on reconnect
- **Message ACK + Retry**: Full lifecycle tracking (pending -> delivered -> acked/failed), exponential backoff retry (max 3), idempotent delivery
- **Offline Message Sync**: Redis ZSET storage (7-day TTL), automatic push on user login, manual sync via OpSyncReq
- **Multi-Device Session**: Same-device kick, cross-device coexistence, heartbeat auto-renewal (default 4 min), Redis persistence + local cache
- **Message Router (v3)**: Producer -> Router -> MQ -> DeliveryWorker -> Gateway
- **Dual Protocol**: TCP / WebSocket with binary protocol (16-byte header, big-endian)
- **Prometheus Monitoring**: Connection count, message throughput, latency metrics exposed at `/metrics`
- **Token Bucket Rate Limiting**: Per-connection rate limiting to prevent abuse
- **Snowflake ID**: Distributed unique message ID generation
- **Region-Aware Load Balancing**: IP geolocation -> province -> region, same-region Comet priority

## Quick Start

### Docker Compose (Recommended)

```bash
# Start all services
docker compose up -d

# Check status
docker compose ps

# Test API
curl http://localhost:3111/goim/online/total

# Open chat demo
# Browse http://localhost:8080
```

Startup order: Redis + Kafka + Discovery -> Logic -> Comet + Job

### Manual Build

```bash
# Prerequisites: Go 1.20+, Redis, Kafka, Discovery
make build

# Start Logic
nohup target/logic -conf=target/logic.toml -region=sh -zone=sh001 -deploy.env=dev -weight=10 &

# Start Comet
nohup target/comet -conf=target/comet.toml -region=sh -zone=sh001 -deploy.env=dev -weight=10 -addrs=127.0.0.1 &

# Start Job
nohup target/job -conf=target/job.toml -region=sh -zone=sh001 -deploy.env=dev &
```

## Configuration

### Comet Config

| Key | Description | Default |
|-----|-------------|---------|
| `discovery.nodes` | Discovery addresses | `["127.0.0.1:7171"]` |
| `tcp.bind` | TCP port | `[":3101"]` |
| `websocket.bind` | WebSocket port | `[":3102"]` |
| `protocol.handshakeTimeout` | Handshake timeout | `8s` |
| `protocol.rateLimit` | Rate limit per second | `100.0` |
| `bucket.size` | Connection bucket count | `32` |

### Logic Config

| Key | Description | Default |
|-----|-------------|---------|
| `node.heartbeat` | Heartbeat interval | `4m` |
| `kafka.brokers` | Kafka addresses | `["127.0.0.1:9092"]` |
| `redis.addr` | Redis address | `"127.0.0.1:6379"` |
| `redis.expire` | Session expiry | `30m` |
| `backoff.maxDelay` | Max retry delay | `300s` |

## HTTP API

| Method | Path | Description | Parameters |
|--------|------|-------------|------------|
| POST | `/goim/push/keys` | Push by keys | `operation`, `keys[]` |
| POST | `/goim/push/mids` | Push by user IDs | `operation`, `mids[]` |
| POST | `/goim/push/room` | Room broadcast | `operation`, `type`, `room` |
| POST | `/goim/push/all` | Global broadcast | `operation`, `speed` |
| POST | `/goim/push/offline` | Offline store | `mid`, `op`, `seq` |
| GET | `/goim/sync` | Sync offline | `mid`, `last_seq`, `limit` |
| GET | `/goim/online/total` | Total online | - |
| GET | `/goim/online/room` | Room online | `type`, `rooms[]` |
| GET | `/goim/online/top` | Top rooms | `type`, `limit` |
| GET | `/goim/nodes/weighted` | Node list | - |
| GET | `/metrics` | Prometheus | - |

## Operation Codes

| Op | Name | Direction | Description |
|----|------|-----------|-------------|
| 0 | OpHandshake | C->S | Handshake |
| 2 | OpHeartbeat | C->S | Heartbeat |
| 3 | OpHeartbeatReply | S->C | Heartbeat reply (includes online count) |
| 4 | OpSendMsg | C->S | Send message |
| 7 | OpAuth | C->S | Authentication |
| 8 | OpAuthReply | S->C | Auth reply |
| 9 | OpRaw | S->C | Raw message push |
| 12 | OpChangeRoom | C->S | Change room |
| 14 | OpSub | C->S | Subscribe operations |
| 18 | OpSendMsgAck | S->C | Message send ACK |
| 19 | OpPushMsgAck | C->S | Push received ACK |
| 20 | OpSyncReq | C->S | Sync offline request |
| 21 | OpSyncReply | S->C | Sync offline reply |

## Redis Data Model

| Key Pattern | Type | Description |
|-------------|------|-------------|
| `mid_{uid}` | Hash | User -> connection key + server |
| `key_{key}` | String | Connection key -> server |
| `session:{sid}` | Hash | Session metadata |
| `user_sessions:{uid}` | Hash | All user sessions |
| `device_session:{uid}:{device}` | String | Device session (kick) |
| `msg:{msg_id}` | Hash | Message status tracking |
| `offline:{uid}` | ZSET | Offline message queue |
| `user_seq:{uid}` | String | Message sequence number |

## Benchmarks

```bash
# Connection benchmark
go run benchmarks/conn_bench.go -host=localhost:3101 -count=1000

# Push benchmark
go run benchmarks/push_bench.go -logic-host=localhost:3111 -comet-host=localhost:3102

# Full benchmark + HTML report
bash benchmarks/run.sh
```

### Historical Data

| Metric | Value |
|--------|-------|
| Online connections | 1,000,000 |
| Test duration | 15 min |
| Room broadcast rate | 40/s |
| Message receive throughput | 35,900,000/s |
| CPU usage | 2000%~2300% |
| Memory usage | 14GB |

## License

goim is distributed under the terms of the MIT License.
