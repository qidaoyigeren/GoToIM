// 路由分发核心——双通道消息投递引擎。
//
// 本文件实现 IM 系统中最关键的消息路由逻辑：
//   - RouteByUser  : 单用户消息（私聊/系统通知），采用"直连优先 + 可靠兜底"双通道策略
//   - RouteByRoom  : 群聊/房间消息，通过 Kafka/Room 管道广播给房间内所有成员
//   - RouteBroadcast : 全服广播，推送给所有在线用户
//
// 设计原则：
//   1. 在线用户优先走直连通道（gRPC → Comet），低延迟
//   2. 直连失败或用户离线，走可靠通道（Kafka + 离线队列），保证消息不丢
//   3. msgID 去重通过 Redis HSETNX 原子操作实现，保证幂等投递

package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/service"
	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/Terry-Mao/goim/internal/tracectx"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// ============================================================================
// RouteByUser —— 单用户消息投递（双通道核心入口）
// ============================================================================
//
// 这是整个路由引擎的"心脏"方法。每条发给特定用户的消息（私聊消息、系统通知、
// 回执等）都从这里进入，然后走"双通道"分岔路：
//
//	 ┌─────────────┐
//	 │  RouteByUser │
//	 └──────┬──────┘
//	        │
//	   ┌────▼────┐
//	   │ HSETNX  │  ← msgID 去重（Redis 原子操作）
//	   └────┬────┘
//	        │
//	   ┌────▼────┐
//	   │ 在线？   │
//	   └─┬───┬───┘
//	     │   │
//	  是 │   │ 否 ──────────────────────┐
//	     │   │                         │
//	┌────▼─┐                           │
//	│直连推送│  gRPC 逐设备推送           │
//	│(gRPC) │                           │
//	└───┬───┘                           │
//	    │                               │
//	┌───┴────┬─────────┐                │
//	│        │         │                │
//	▼        ▼         ▼                ▼
//
// 全部成功  部分失败   全部失败      离线用户
//
//	│        │         │                │
//	▼        ▼         ▼                │
//
// Mark     Mark      全量降级           │
// Delivered Delivered │                │
// return   失败设备   │                │
//
//	走可靠通道  │                │
//	(补推)     │                │
//	           ▼                ▼
//	    ┌────────────┐
//	    │  可靠通道    │ ← Kafka + 离线队列
//	    └─────┬──────┘
//	          │
//	    ┌─────▼──────┐
//	    │Consumer 消费│
//	    │→ Comet 推送 │
//	    │→ 离线拉取   │
//	    └────────────┘
//
// 参数说明：
//
//	ctx     - 上下文，用于超时控制与链路追踪
//	msgID   - 消息唯一 ID；若为空则由 idGen（雪花算法）自动生成
//	toUID   - 目标用户 ID
//	op      - 操作码（对应 api/logic 中定义的 Operation 枚举）
//	body    - 消息体（protobuf 序列化后的二进制数据）
//	seq     - 消息序号，用于客户端排序/去重
//
// 返回值：
//
//	nil     - 消息已成功投递（直连成功 或 已进入 Kafka）
//	error   - 可靠通道也投递失败（此时消息可能已丢失，需业务层重试）
func (e *DispatchEngine) RouteByUser(ctx context.Context, msgID string, toUID int64, op int32, body []byte, seq int64) error {
	// ── 步骤 0：msgID 自动生成 ──────────────────────────────────────────
	// 如果调用方未提供 msgID，则通过 Snowflake 算法（idGen）生成一个全局唯一的
	// 字符串 ID。Snowflake 保证分布式环境下的唯一性，无需中心化协调。
	if msgID == "" {
		id, err := e.generateMsgID()
		if err != nil {
			return err
		}
		msgID = id
	}

	// ── 步骤 0.5：记录 accepted 状态（Phase 2 状态机）
	e.recordState(ctx, msgID, "", "accepted", "message accepted", "")

	// ── 步骤 1：消息去重（幂等性保证） ──────────────────────────────────
	// 利用 Redis HSETNX 命令的原子性来做"抢占式注册"：
	//   - HSETNX 返回 1 → 当前进程抢到了这条消息的处理权，继续投递
	//   - HSETNX 返回 0 → 已有其他 goroutine/节点在处理同一 msgID，直接返回 nil
	//
	// 这保证了：即使上游重复投递（网络重试等），同一 msgID 也只会被推送一次。
	if err := e.ackHandler.TrackMessage(ctx, msgID, 0, toUID, op, body); err != nil {
		log.V(1).Infof("msg already tracked: msg_id=%s err=%v", msgID, err)
		return nil
	}

	// ── 步骤 1.2：记录 routed 状态（Phase 2 状态机）
	e.recordState(ctx, msgID, "accepted", "routed", "dispatch started", "")

	// ── 步骤 1.5：限流检查 ──────────────────────────────────────────────
	if e.limiter != nil {
		if !e.limiter.AllowUser(toUID, 100, 200) { // rate=100/s, burst=200
			metrics.RateLimitedTotal.Inc()
			return fmt.Errorf("rate limited: uid=%d", toUID)
		}
	}

	// 记录开始时间，用于后续耗时统计（Prometheus metrics）
	start := time.Now()

	// ── 步骤 2：检查用户在线状态 ────────────────────────────────────────
	// 从 SessionManager（基于 Redis 的会话管理）查询该用户当前是否有活跃连接。
	// online = true   → 有至少一个活跃 session
	// sessions        → 所有活跃 session 列表（一个用户可能多端登录）
	online, sessions := e.sessMgr.IsOnline(ctx, toUID)

	// ── 步骤 3：在线 → 尝试直连通道（fast path） ────────────────────────
	// 直连完成后，根据 failedSessions 决定下一步：
	//   - failedSessions=nil, err=nil  → 全部设备推送成功，标记已送达，返回
	//   - failedSessions 非空, err=nil → 部分设备失败，标记已送达 + 只对失败设备走可靠通道补推
	//   - failedSessions 非空, err!=nil → 全部失败，全量走可靠通道兜底
	if online {
		failedSessions, pushErr := directPush(ctx, e.pusher, sessions, op, body)
		if pushErr == nil && len(failedSessions) == 0 {
			// 全部成功 → 标记已送达
			if err := e.markDelivered(ctx, msgID); err != nil {
				return err
			}
			e.directTotal.Add(1)
			metrics.PushTotal.WithLabelValues("direct", "success").Inc()
			metrics.PushLatency.WithLabelValues("direct").Observe(time.Since(start).Seconds())
			// 记录 grpc_direct 成功 attempt
			e.recordAttempt(ctx, msgID, "grpc_direct", "success", time.Since(start).Milliseconds(), "", firstServer(sessions))
			e.recordState(ctx, msgID, "routed", "direct_sent", "all sessions pushed", "")
			return nil
		}
		if pushErr == nil && len(failedSessions) > 0 {
			// 部分失败：成功设备已收到消息，不需补推；只对失败设备走可靠通道
			metrics.PushTotal.WithLabelValues("direct", "partial_failed").Inc()
			log.Warningf("partial direct push failed: uid=%d msg_id=%s succeeded=%d failed=%d",
				toUID, msgID, len(sessions)-len(failedSessions), len(failedSessions))
			if err := e.markDelivered(ctx, msgID); err != nil {
				return err
			}
			e.recordAttempt(ctx, msgID, "grpc_direct", "success", time.Since(start).Milliseconds(), "partial", firstServer(sessions))
			e.recordState(ctx, msgID, "routed", "direct_sent", "partial success", "")
			err := e.reliableEnqueue(ctx, msgID, toUID, op, body, seq, failedSessions)
			e.directTotal.Add(1)
			if err == nil {
				e.kafkaTotal.Add(1)
				metrics.PushTotal.WithLabelValues("kafka", "partial_success").Inc()
			} else {
				metrics.PushTotal.WithLabelValues("kafka", "failed").Inc()
			}
			e.recordAttempt(ctx, msgID, "kafka_fallback", statusFromErr(err), time.Since(start).Milliseconds(), errStr(err), "")
			return err
		}
		// 全部失败 → 全量降级到可靠通道
		metrics.PushTotal.WithLabelValues("direct", "failed").Inc()
		log.Warningf("direct push failed, falling back to reliable path: uid=%d msg_id=%s", toUID, msgID)
		e.recordAttempt(ctx, msgID, "grpc_direct", "failed", time.Since(start).Milliseconds(), pushErr.Error(), firstServer(sessions))
		e.recordState(ctx, msgID, "routed", "direct_failed", pushErr.Error(), "")
	}

	// ── 步骤 4：离线 或 直连全部失败 → 走可靠通道（slow path） ──────────
	// reliableEnqueue 做两件事：
	//   1. 将消息写入离线队列（Redis ZSet），供用户下次上线时拉取
	//   2. 将消息投递到 Kafka（producer），由 DeliveryWorker（internal/worker/dispatch.go）消费后推送给 Comet
	err := e.reliableEnqueue(ctx, msgID, toUID, op, body, seq, sessions)
	channel := "kafka_fallback"
	if !online {
		channel = "offline_stored"
	}
	e.recordAttempt(ctx, msgID, channel, statusFromErr(err), time.Since(start).Milliseconds(), errStr(err), "")
	if channel == "offline_stored" {
		e.recordState(ctx, msgID, "routed", "offline_stored", "user offline", "")
	} else {
		e.recordState(ctx, msgID, "direct_failed", "fallback_queued", "kafka fallback", "")
	}
	if err == nil {
		e.kafkaTotal.Add(1)
		metrics.PushTotal.WithLabelValues("kafka", "success").Inc()
		metrics.PushLatency.WithLabelValues("kafka").Observe(time.Since(start).Seconds())
	} else {
		metrics.PushTotal.WithLabelValues("kafka", "failed").Inc()
	}
	return err
}

// ============================================================================
// RouteByRoom —— 房间/群组消息投递
// ============================================================================
//
// 将消息推送到某个聊天室/直播间的所有成员。
//
// 双通道策略（Kafka 优先 + 直推兜底）：
//  1. 优先将消息写入 Kafka Room Topic，由 DeliveryWorker 消费后广播
//  2. 若 Kafka 写入失败且有 broadcaster 兜底 → 直接 gRPC 广播到所有 Comet 节点
//     （每个 Comet 自行路由到房间内的客户端）
//  3. 无 producer 且无 broadcaster → 走 DAO 旧路径（同样是 Kafka，纯代码兼容）
//
// 参数说明：
//
//	op      - 操作码
//	roomKey - 房间唯一标识（如直播间 ID、群组 ID）
//	body    - 消息体（protobuf 序列化后的二进制数据）
func (e *DispatchEngine) RouteByRoom(ctx context.Context, op int32, roomKey string, body []byte) error {
	// 优先走 Kafka
	if e.producer != nil {
		pushMsg := pushMsgBytes(pbRoom, op, "", nil, roomKey, body, 0)
		if err := e.producer.EnqueueToRoom(ctx, roomKey, &mq.Message{Key: roomKey, Value: pushMsg}); err != nil {
			// Kafka 写入失败 → 兜底：直接 gRPC 广播到所有 Comet 节点
			log.Warningf("kafka enqueue room failed, falling back to direct broadcast: room=%s err=%v", roomKey, err)
			metrics.PushTotal.WithLabelValues("kafka", "failed").Inc()
			if e.broadcaster != nil {
				if err := e.broadcaster.BroadcastRoom(ctx, op, roomKey, body); err != nil {
					return err
				}
				e.directTotal.Add(1)
				return nil
			}
			return err
		}
		e.kafkaTotal.Add(1)
		return nil
	}
	// 无 Kafka → 兜底：直接广播或 DAO
	if e.broadcaster != nil {
		if err := e.broadcaster.BroadcastRoom(ctx, op, roomKey, body); err != nil {
			return err
		}
		e.directTotal.Add(1)
		return nil
	}
	if err := e.dao.BroadcastRoomMsg(ctx, op, roomKey, body); err != nil {
		return err
	}
	e.kafkaTotal.Add(1)
	return nil
}

// ============================================================================
// RouteBroadcast —— 全服广播
// ============================================================================
//
// 将消息推送给所有在线用户（如系统维护公告、全员通知等）。
//
// 双通道策略：优先 Kafka → 失败则直接 gRPC 广播到所有 Comet 节点。
//
// 参数说明：
//
//	op    - 操作码
//	speed - 广播速率控制（限制推送 QPS，避免瞬间打爆下游）
//	body  - 消息体
func (e *DispatchEngine) RouteBroadcast(ctx context.Context, op, speed int32, body []byte) error {
	if e.producer != nil {
		pushMsg := pushMsgBytes(pbBroadcast, op, "", nil, "", body, speed)
		if err := e.producer.EnqueueBroadcast(ctx, &mq.Message{Value: pushMsg}, speed); err != nil {
			// Kafka 写入失败 → 兜底：直接 gRPC 广播到所有 Comet 节点
			log.Warningf("kafka enqueue broadcast failed, falling back to direct broadcast: err=%v", err)
			metrics.PushTotal.WithLabelValues("kafka", "failed").Inc()
			if e.broadcaster != nil {
				if err := e.broadcaster.BroadcastAll(ctx, op, speed, body); err != nil {
					return err
				}
				e.directTotal.Add(1)
				return nil
			}
			return err
		}
		e.kafkaTotal.Add(1)
		return nil
	}
	if e.broadcaster != nil {
		if err := e.broadcaster.BroadcastAll(ctx, op, speed, body); err != nil {
			return err
		}
		e.directTotal.Add(1)
		return nil
	}
	if err := e.dao.BroadcastMsg(ctx, op, speed, body); err != nil {
		return err
	}
	e.kafkaTotal.Add(1)
	return nil
}

// ============================================================================
// directPush —— 直连通道：gRPC 推送到 Comet 网关
// ============================================================================
//
// 遍历用户的所有活跃 session（多端登录场景），通过 gRPC 逐一会话推送消息。
//
// 容错策略（修改后）：
//   - 单个 session 推送失败 → 收集到 failedSessions，继续推下一个（best-effort）
//   - 全部 session 推送成功 → 返回 nil, nil
//   - 部分 session 推送失败 → 返回失败 session 列表 + nil error，
//     由上层 RouteByUser 只对失败的 session 走可靠通道补推
//   - 全部 session 推送失败 → 返回全部 session 列表 + error，
//     由上层走可靠通道全量兜底
//
// 这样避免了旧版 anyOK 逻辑的缺陷：
//
//	"3 台设备中 2 台成功 1 台失败 → 标记已送达 → 跳过可靠通道 → 失败设备永久丢消息"
//
// 超时控制：
//
//	每个 session 的 gRPC 调用有 500ms 独立超时（context.WithTimeout），
//	避免某个 Comet 节点故障导致整个 push 操作被拖死。
func directPush(ctx context.Context, pusher CometPusher, sessions []*service.Session, op int32, body []byte) (failedSessions []*service.Session, err error) {
	if pusher == nil {
		return sessions, fmt.Errorf("comet pusher not configured")
	}
	var lastErr error
	anyOK := false
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		// 每个 session 独立超时 500ms，防止单点 Comet 阻塞整个循环
		pushCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		pushErr := pusher.PushMsg(pushCtx, sess.Server, []string{sess.Key}, op, body)
		cancel()

		if pushErr != nil {
			lastErr = pushErr
			failedSessions = append(failedSessions, sess)
			log.Warningf("direct push to server=%s key=%s failed: %v", sess.Server, sess.Key, pushErr)
			continue
		}
		anyOK = true
	}
	// 全部失败 → 返回错误，触发上层全量兜底
	if !anyOK && len(failedSessions) > 0 {
		return failedSessions, fmt.Errorf("all direct pushes failed: %w", lastErr)
	}
	// 部分失败或全部成功 → 返回 nil error + 失败列表（可能为空）
	return failedSessions, nil
}

// RouteByUserResult mirrors RouteByUser and returns a structured path result for business outbox auditing.
func (e *DispatchEngine) RouteByUserResult(ctx context.Context, msgID string, toUID int64, op int32, body []byte, seq int64) (DeliveryResult, error) {
	start := time.Now()
	traceID := traceIDFromBodyOrContext(ctx, body)
	result := DeliveryResult{
		MsgID:     msgID,
		Path:      "failed",
		AttemptNo: 1,
		TraceID:   traceID,
	}
	defer func() {
		result.LatencyMs = float64(time.Since(start).Microseconds()) / 1000.0
	}()

	if msgID == "" {
		id, err := e.generateMsgID()
		if err != nil {
			result.ErrorCode = "id_generation_failed"
			result.ErrorMessage = err.Error()
			return result, err
		}
		msgID = id
		result.MsgID = id
	}
	ctx = tracectx.WithTraceID(ctx, traceID)
	if err := e.ackHandler.TrackMessage(ctx, msgID, 0, toUID, op, body); err != nil {
		log.V(1).Infof("msg already tracked: msg_id=%s err=%v", msgID, err)
		result.Path = "grpc_direct"
		return result, nil
	}
	if e.limiter != nil && !e.limiter.AllowUser(toUID, 100, 200) {
		metrics.RateLimitedTotal.Inc()
		result.ErrorCode = "rate_limited"
		result.ErrorMessage = fmt.Sprintf("rate limited: uid=%d", toUID)
		return result, ErrRateLimited
	}

	online, sessions := e.sessMgr.IsOnline(ctx, toUID)
	if len(sessions) > 0 && sessions[0] != nil {
		result.TargetNode = sessions[0].Server
	}
	if online {
		failedSessions, pushErr := directPush(ctx, e.pusher, sessions, op, body)
		if pushErr == nil && len(failedSessions) == 0 {
			if err := e.markDelivered(ctx, msgID); err != nil {
				result.ErrorCode = "mark_delivered_failed"
				result.ErrorMessage = err.Error()
				return result, err
			}
			e.directTotal.Add(1)
			metrics.PushTotal.WithLabelValues("direct", "success").Inc()
			metrics.PushLatency.WithLabelValues("direct").Observe(time.Since(start).Seconds())
			result.Path = "grpc_direct"
			return result, nil
		}
		if pushErr == nil && len(failedSessions) > 0 {
			if err := e.markDelivered(ctx, msgID); err != nil {
				result.ErrorCode = "mark_delivered_failed"
				result.ErrorMessage = err.Error()
				return result, err
			}
			err := e.reliableEnqueue(ctx, msgID, toUID, op, body, seq, failedSessions)
			e.directTotal.Add(1)
			result.Path = "kafka_fallback"
			if err == nil {
				e.kafkaTotal.Add(1)
				metrics.PushTotal.WithLabelValues("kafka", "partial_success").Inc()
			} else {
				result.ErrorCode = "kafka_fallback_failed"
				result.ErrorMessage = err.Error()
				metrics.PushTotal.WithLabelValues("kafka", "failed").Inc()
			}
			return result, err
		}
		metrics.PushTotal.WithLabelValues("direct", "failed").Inc()
	}

	err := e.reliableEnqueue(ctx, msgID, toUID, op, body, seq, sessions)
	if err == nil {
		e.kafkaTotal.Add(1)
		metrics.PushTotal.WithLabelValues("kafka", "success").Inc()
		metrics.PushLatency.WithLabelValues("kafka").Observe(time.Since(start).Seconds())
		if online {
			result.Path = "kafka_fallback"
		} else {
			result.Path = "offline_stored"
		}
		return result, nil
	}
	metrics.PushTotal.WithLabelValues("kafka", "failed").Inc()
	result.ErrorCode = "reliable_enqueue_failed"
	result.ErrorMessage = err.Error()
	return result, err
}

// ============================================================================
// reliableEnqueue —— 可靠通道：离线队列 + Kafka 投递
// ============================================================================
//
// 可靠通道是消息投递的"兜底"路径，做两件事：
//
//  1. 离线队列（Redis ZSet）
//     将 msgID 和 seq 写入以用户 ID 为维度的 Redis ZSet 中。
//     用户下次上线时，Comet 会从 ZSet 拉取未读消息（按 seq 排序），
//     确保离线期间的消息不会丢失。
//
//  2. Kafka 投递（异步解耦）
//     将消息序列化为 pb.PushMsg（type=pbPush），写入 Kafka User Topic。
//     DeliveryWorker（internal/worker/dispatch.go）消费后：
//     - 若用户在线 → 通过 gRPC 推送给 Comet → 客户端
//     - 若用户离线 → 消息已存离线队列，用户上线后拉取
//     Kafka 的分区键为用户 ID（toUID），保证同一用户的消息有序。
//
// 连接键（keys）提取逻辑：
//   - 优先使用活跃 session 的真实连接 key（如 TCP 连接的 fd 标识）
//   - 若无活跃 session（用户离线），使用占位 key "uid:xxx"，
//     后续由 DeliveryWorker 消费时识别并走离线队列拉取路径
//
// 参数说明：
//
//	msgID    - 消息唯一 ID
//	toUID    - 目标用户 ID
//	op       - 操作码
//	body     - 消息体
//	seq      - 消息序号
//	sessions - 用户活跃会话列表（可能为空）
func (e *DispatchEngine) reliableEnqueue(ctx context.Context, msgID string, toUID int64, op int32, body []byte, seq int64, sessions []*service.Session) error {
	// Phase 2: 从 body 解析 BizEnvelope，提取 priority/TTL/trace 等 header
	env := parseBizEnvelope(body, msgID)

	// ── 步骤 1：写入离线队列（Redis ZSet） ──────────────────────────────
	// 即使用户在线，也写入离线队列作为"最后防线"。
	// ZSet 的 score 为 seq，value 为 msgID，方便客户端按序拉取。
	var offlineErr error
	if err := e.msgDAO.AddToOfflineQueue(ctx, toUID, msgID, float64(seq)); err != nil {
		log.Warningf("add to offline queue failed: uid=%d msg_id=%s err=%v", toUID, msgID, err)
		// 离线队列写入失败不阻塞后续 Kafka 投递
		offlineErr = err
	}

	// ── 步骤 2：提取有效连接键 ──────────────────────────────────────────
	server := ""
	var keys []string
	for _, sess := range sessions {
		if sess != nil && sess.Key != "" {
			keys = append(keys, sess.Key)
			// 记录第一个非空 server 地址，供 Comet 路由用
			if server == "" && sess.Server != "" {
				server = sess.Server
			}
		}
	}
	// 用户离线时 sessions 为空，使用占位 key "uid:xxx"。
	// DeliveryWorker 消费时通过 "uid:" 前缀识别这是离线用户消息，走离线拉取流程。
	if len(keys) == 0 {
		keys = []string{fmt.Sprintf("uid:%d", toUID)}
	}

	// ── 步骤 3：投递到 Kafka ────────────────────────────────────────────
	if e.producer != nil {
		// 序列化为 pb.PushMsg（type=pbPush），分区键为 uidKey，保证同用户消息有序
		pushMsg := pushMsgBytes(pbPush, op, server, keys, "", body, 0)
		uidKey := fmt.Sprintf("%d", toUID)
		msg := &mq.Message{
			Key:     uidKey,
			Value:   pushMsg,
			Headers: buildMQHeaders(env),
		}
		if err := e.producer.EnqueueToUser(ctx, toUID, msg); err != nil {
			return e.spoolReliableMessage(msgID, toUID, op, body, seq, server, keys, msg.Headers, offlineErr, err)
		}
		if offlineErr != nil {
			if err := e.spoolReliableMessage(msgID, toUID, op, body, seq, server, keys, msg.Headers, offlineErr, nil); err != nil {
				log.Warningf("redis offline repair spool failed after kafka success: msg_id=%s uid=%d err=%v", msgID, toUID, err)
			}
		}
		return nil
	}
	// 降级路径：无 Kafka 时直接调用 DAO 推送
	if err := e.dao.PushMsg(ctx, op, server, keys, body); err != nil {
		return e.spoolReliableMessage(msgID, toUID, op, body, seq, server, keys, buildMQHeaders(env), offlineErr, err)
	}
	if offlineErr != nil {
		if err := e.spoolReliableMessage(msgID, toUID, op, body, seq, server, keys, buildMQHeaders(env), offlineErr, nil); err != nil {
			log.Warningf("redis offline repair spool failed after dao enqueue success: msg_id=%s uid=%d err=%v", msgID, toUID, err)
		}
	}
	return nil
}

type localSpoolRecord struct {
	MsgID     string            `json:"msg_id"`
	ToUID     int64             `json:"to_uid"`
	Op        int32             `json:"op"`
	Body      []byte            `json:"body"`
	Seq       int64             `json:"seq"`
	Server    string            `json:"server"`
	Keys      []string          `json:"keys"`
	Headers   map[string]string `json:"headers,omitempty"`
	Reason    string            `json:"reason"`
	CreatedAt int64             `json:"created_at_unix_ms"`
}

func (e *DispatchEngine) spoolReliableMessage(msgID string, toUID int64, op int32, body []byte, seq int64, server string, keys []string, headers map[string]string, offlineErr, enqueueErr error) error {
	if offlineErr == nil && enqueueErr == nil {
		return nil
	}
	if offlineErr == nil {
		return enqueueErr
	}
	reason := fmt.Sprintf("offline queue failed: %v", offlineErr)
	if enqueueErr != nil {
		reason = fmt.Sprintf("%s; enqueue failed: %v", reason, enqueueErr)
	}
	if err := writeLocalSpool(localSpoolRecord{
		MsgID:     msgID,
		ToUID:     toUID,
		Op:        op,
		Body:      body,
		Seq:       seq,
		Server:    server,
		Keys:      keys,
		Headers:   headers,
		Reason:    reason,
		CreatedAt: time.Now().UnixMilli(),
	}); err != nil {
		if enqueueErr != nil {
			return fmt.Errorf("%s; local spool failed: %w", reason, err)
		}
		return fmt.Errorf("local spool failed: %w", err)
	}
	log.Warningf("message spooled locally for reliable path repair: msg_id=%s uid=%d reason=%s", msgID, toUID, reason)
	if enqueueErr != nil {
		return fmt.Errorf("%s; spooled locally", reason)
	}
	return nil
}

func writeLocalSpool(record localSpoolRecord) error {
	root := os.Getenv("GOIM_UNDELIVERED_SPOOL_DIR")
	if root == "" {
		root = filepath.Join(os.TempDir(), "goim-undelivered")
	}
	dir := filepath.Join(root, time.Now().Format("20060102"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%d.json", record.MsgID, time.Now().UnixNano())
	tmp := filepath.Join(dir, name+".tmp")
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	metrics.SpoolWriteTotal.Inc()
	return nil
}

// ============================================================================
// pb 消息类型常量
// ============================================================================
// 对应 api/logic 中 PushMsg_Type 枚举值，用于标识消息的目标分发模式：
//
//	pbPush      (0) → 单用户推送
//	pbRoom      (1) → 群组/房间推送
//	pbBroadcast (2) → 全服广播
//
// recordAttempt is a thin wrapper that logs a delivery attempt without affecting the main flow.
func (e *DispatchEngine) recordAttempt(ctx context.Context, msgID, channel, status string, latencyMs int64, errStr, server string) {
	if e.attemptRec != nil {
		e.attemptRec.Record(ctx, msgID, channel, status, latencyMs, errStr, server)
	}
}

func firstServer(sessions []*service.Session) string {
	for _, s := range sessions {
		if s != nil && s.Server != "" {
			return s.Server
		}
	}
	return ""
}

func statusFromErr(err error) string {
	if err == nil {
		return "success"
	}
	return "failed"
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (e *DispatchEngine) generateMsgID() (string, error) {
	if e.idGen == nil {
		return "", fmt.Errorf("message id generator is not configured")
	}
	id, err := e.idGen.GenerateString()
	if err != nil {
		return "", fmt.Errorf("generate message id: %w", err)
	}
	if id == "" {
		return "", fmt.Errorf("generate message id: empty id")
	}
	return id, nil
}

func (e *DispatchEngine) markDelivered(ctx context.Context, msgID string) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		if err := e.ackHandler.MarkDelivered(ctx, msgID); err == nil {
			return nil
		} else {
			lastErr = err
			time.Sleep(time.Duration(i+1) * 10 * time.Millisecond)
		}
	}
	metrics.PushTotal.WithLabelValues("direct", "mark_delivered_failed").Inc()
	return fmt.Errorf("mark delivered: %w", lastErr)
}

// parseBizEnvelope attempts to unmarshal a BizEnvelope from the message body.
// Falls back to an empty envelope if the body is not a valid BizEnvelope (old format).
func parseBizEnvelope(body []byte, msgID string) *mq.BizEnvelope {
	env := &mq.BizEnvelope{MsgID: msgID}
	if len(body) == 0 {
		return env
	}
	// Try JSON BizEnvelope; on failure treat body as raw payload
	var parsed mq.BizEnvelope
	if err := json.Unmarshal(body, &parsed); err != nil {
		if mb, mbErr := protocol.UnmarshalMsgBody(body); mbErr == nil && len(mb.Content) > 0 {
			return parseBizEnvelope(mb.Content, msgID)
		}
		env.Payload = body
		return env
	}
	if parsed.MsgID == "" {
		parsed.MsgID = msgID
	}
	return &parsed
}

func traceIDFromBodyOrContext(ctx context.Context, body []byte) string {
	if traceID := tracectx.TraceID(ctx); traceID != "" {
		return traceID
	}
	if env := parseBizEnvelope(body, ""); env != nil && env.TraceID != "" {
		return env.TraceID
	}
	return tracectx.FromJSONPayload(body)
}

// buildMQHeaders converts a BizEnvelope into Kafka message headers.
func buildMQHeaders(env *mq.BizEnvelope) map[string]string {
	if env == nil {
		return nil
	}
	h := make(map[string]string)
	if env.Priority != "" {
		h[mq.HeaderPriority] = env.Priority
	}
	if env.TTLSeconds > 0 {
		h[mq.HeaderTTLSeconds] = strconv.Itoa(int(env.TTLSeconds))
	}
	if env.TraceID != "" {
		h[mq.HeaderTraceID] = env.TraceID
	}
	if env.BusinessType != "" {
		h[mq.HeaderBusinessType] = env.BusinessType
	}
	if env.EventType != "" {
		h[mq.HeaderEventType] = env.EventType
	}
	if env.DedupeKey != "" {
		h[mq.HeaderDedupeKey] = env.DedupeKey
	}
	if env.BizID != "" {
		h[mq.HeaderBizID] = env.BizID
	}
	if env.CreatedAtMS > 0 {
		h[mq.HeaderCreatedAtUnixMS] = strconv.FormatInt(env.CreatedAtMS, 10)
	}
	return h
}

// recordState is a thin wrapper that records a state transition without affecting the main flow.
func (e *DispatchEngine) recordState(ctx context.Context, msgID, from, to, reason, traceID string) {
	if e.stateRec != nil {
		e.stateRec.RecordState(ctx, msgID, DeliveryState(from), DeliveryState(to), reason, traceID)
	}
}

// GetDeliveryState returns the current delivery state of a message.
func (e *DispatchEngine) GetDeliveryState(ctx context.Context, msgID string) (DeliveryState, error) {
	if e.stateRec != nil {
		return e.stateRec.GetCurrentState(ctx, msgID)
	}
	return "", nil
}

// GetStateTimeline returns the full state transition history of a message.
func (e *DispatchEngine) GetStateTimeline(ctx context.Context, msgID string) ([]StateTransition, error) {
	if e.stateRec != nil {
		return e.stateRec.GetTimeline(ctx, msgID)
	}
	return nil, nil
}

const (
	pbPush      = 0
	pbRoom      = 1
	pbBroadcast = 2
)
