# Order Notification Platform — Design Spec

> **Date**: 2026-05-16
> **Project**: goim — Real-time Order Status & Notification Pipeline
> **Goal**: Transform goim from a generic IM server into a resume-worthy e-commerce notification platform that demonstrates high-concurrency infrastructure engineering

---

## 1. Problem & Motivation

goim is a technically impressive project (1M concurrent connections, dual-channel delivery, ACK tracking, idempotency), but as a generic IM server it lacks business context on a resume. Recruiters for backend/infrastructure roles want to see distributed systems solving real business problems.

**This spec adds a thin business layer** that positions goim as a "Real-time Order Notification Platform" — the infrastructure that powers order status updates, flash sale alerts, and logistics notifications for an e-commerce platform. The existing goim architecture stays untouched; the new layer demonstrates understanding of how infrastructure serves business needs.

---

## 2. Architecture

```
Order Tracker Frontend (web/order-tracker/index.html)
       │ HTTP REST + WebSocket (to Comet :3102)
       ▼
Notify Server (cmd/notify-server/main.go, port :3121)
  ├── Business APIs: order status, flash sale, logistics, stats
  └── Load Generator: lifecycle / normal / peak / flash_sale scenarios
       │ HTTP calls to goim push API
       ▼
Existing goim Infrastructure (unchanged)
  Logic (:3111) → Router → dual-channel → Comet (:3102) → Client
                   │          │
                   ▼          ▼
               Kafka       gRPC direct
```

**Key principle**: The notify server is a *consumer* of goim's push infrastructure, not a modification of it. Same relationship a real platform service would have with its message delivery middle-platform.

---

## 3. Business Domain

### 3.1 Order State Machine

```
created → paid → confirmed → shipped → delivered
  │                              │
  └── cancelled                  └── delivery_failed
```

Each state transition triggers a real-time push notification via goim.

### 3.2 Core Entities

- **Order**: order_id, user_id, status, items, total, timestamps
- **Notification**: notify_id, user_id, type (order_status/flash_sale/logistics/system), title, content, order_id, status (pending→delivered→acked)
- **FlashSale**: sale_id, title, description, target_uids, start_at

### 3.3 Notification Types

| Type | Trigger | Push Method | Concurrency Profile |
|------|---------|------------|---------------------|
| order_status | Order state transition | PushKeys (per user) | Steady, per-user |
| flash_sale | Manual / simulator trigger | PushMids (batch) | Burst, many users |
| logistics | Tracking update | PushKeys (per user) | High-frequency stream |
| system | Platform announcement | PushAll (broadcast) | Rare, all users |

---

## 4. REST API Design

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/order/status-change` | Trigger order status change notification |
| `POST` | `/api/flash-sale/notify` | Send flash sale alert to targeted users |
| `POST` | `/api/logistics/update` | Send logistics tracking update |
| `GET` | `/api/user/:uid/notifications` | User notification history |
| `GET` | `/api/orders/:uid` | User orders with status badges |
| `GET` | `/api/platform/stats` | Aggregate metrics (push rate, ACK rate, latency, connections) |
| `POST` | `/api/simulate/start` | Start load simulation |
| `POST` | `/api/simulate/stop` | Stop simulation |
| `GET` | `/api/simulate/status` | Simulation state + metrics |

---

## 5. Frontend — Order Tracker SPA

Standalone page at `web/order-tracker/index.html`. Pure HTML/CSS/JS.

**3-panel layout:**
- **Left**: User selector with connection badges, WS health, ACK rate
- **Center**: Order timeline with status icons and ACK badges, flash sale banners
- **Right**: Live stats dashboard (push rate, latency P50/P99, delivery path ratio, simulation controls)

**Key features:**
- WebSocket auto-reconnect with exponential backoff
- ACK status badges on every message (✓ delivered, ✓✓ acked, ✗ failed)
- Order progress bar visualization
- Flash sale alert banners
- Simulation start/stop controls

---

## 6. Load Generator

Built into notify server binary (`--simulate` flag).

| Mode | Pattern | Rate |
|------|---------|------|
| `lifecycle` | One order full lifecycle in 30s | For demo walkthrough |
| `normal` | Steady mixed order traffic across 10K users | 50-200 orders/s |
| `peak` | High traffic | 500-2000 orders/s |
| `flash_sale` | Burst: 50K notifications in 2s window | Spike test |

---

## 7. File Plan

```
New files:
├── cmd/notify-server/
│   └── main.go                    # Service entry point
├── internal/notify/
│   ├── server.go                  # Gin HTTP server setup
│   ├── handler/
│   │   ├── order.go               # Order status change handlers
│   │   ├── flash_sale.go          # Flash sale handlers
│   │   ├── logistics.go           # Logistics update handlers
│   │   ├── platform.go            # Stats + simulation control
│   │   └── user.go                # User notification history
│   ├── model/
│   │   ├── order.go               # Order, OrderStatus, OrderItem
│   │   ├── notification.go        # Notification, NotifyType
│   │   └── flash_sale.go          # FlashSale
│   ├── service/
│   │   ├── order_notify.go        # Business logic: order → notification
│   │   ├── flash_sale.go          # Flash sale burst logic
│   │   └── push_client.go         # HTTP client to goim Logic push API
│   ├── simulator/
│   │   ├── engine.go              # Simulation engine (start/stop/status)
│   │   ├── order_lifecycle.go     # Order lifecycle scenario
│   │   ├── flash_sale.go          # Flash sale burst scenario
│   │   └── traffic.go             # Normal/peak traffic generator
│   └── conf/
│       └── config.go              # Notify server config
└── web/order-tracker/
    └── index.html                 # SPA frontend

Modified files:
├── go.mod                         # New dependencies (if any)
└── Makefile                       # Build target for notify-server
```

---

## 8. Verification

1. `make build` — all three services (comet, logic, job) + notify-server compile cleanly
2. `docker compose up -d` — full stack starts, notify-server connects to Logic
3. `curl -X POST localhost:3121/api/simulate/start -d '{"mode":"lifecycle"}'` — demo order lifecycle runs, notifications appear in frontend
4. `curl -X POST localhost:3121/api/simulate/start -d '{"mode":"flash_sale","qps":5000}'` — burst test shows high-throughput delivery with ACK rate > 99%
5. Open `web/order-tracker/index.html` — see real-time order notifications, ACK badges update, stats dashboard populates
6. `GET /api/platform/stats` — returns valid metrics JSON with push_rate, ack_rate, latency distribution, delivery path ratio
