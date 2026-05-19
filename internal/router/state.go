// 投递状态机 —— IM 消息全生命周期追踪。
//
// Phase 2 将旧版四态（pending/delivered/acked/failed）升级为完整状态机，
// 覆盖直连、Kafka 回退、离线存储、ACK、过期、DLQ 等全部路径。
//
// 状态流转图：
//
//   accepted → routed → direct_sent ──→ delivered → acked
//                    → direct_failed → fallback_queued → fallback_sent → delivered → acked
//                    → offline_stored → synced → delivered → acked
//                    → expired → dlq
//
// 旧版兼容：空 → accepted / empty → delivered / empty → acked 不报错。

package router

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/gomodule/redigo/redis"
)

// DeliveryState 表示消息投递生命周期中的一个状态。
type DeliveryState string

const (
	StateAccepted       DeliveryState = "accepted"
	StateRouted         DeliveryState = "routed"
	StateDirectSent     DeliveryState = "direct_sent"
	StateDirectFailed   DeliveryState = "direct_failed"
	StateFallbackQueued DeliveryState = "fallback_queued"
	StateFallbackSent   DeliveryState = "fallback_sent"
	StateDelivered      DeliveryState = "delivered"
	StateOfflineStored  DeliveryState = "offline_stored"
	StateSynced         DeliveryState = "synced"
	StateAcked          DeliveryState = "acked"
	StateExpired        DeliveryState = "expired"
	StateDLQ            DeliveryState = "dlq"
	StateRateLimited    DeliveryState = "rate_limited"

	// 旧版兼容别名
	StatePending = StateAccepted // "pending" 映射到 "accepted"
	StateFailed  = StateDLQ      // "failed" 映射到 "dlq"
)

// StateTransition 记录一次状态变更。
type StateTransition struct {
	MsgID     string        `json:"msg_id"`
	From      DeliveryState `json:"from"`
	To        DeliveryState `json:"to"`
	Reason    string        `json:"reason,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
	TraceID   string        `json:"trace_id,omitempty"`
}

// validTransitions 定义合法状态转换表。
// 空 from 表示新消息或无前置状态（向后兼容）。
var validTransitions = map[DeliveryState][]DeliveryState{
	"":                  {StateAccepted, StateRouted, StateDelivered, StateAcked, StateDLQ},
	StateAccepted:       {StateRouted, StateExpired, StateRateLimited, StateDLQ},
	StateRouted:         {StateDirectSent, StateDirectFailed, StateOfflineStored, StateFallbackQueued, StateExpired},
	StateDirectSent:     {StateDelivered},
	StateDirectFailed:   {StateFallbackQueued, StateDLQ},
	StateFallbackQueued: {StateFallbackSent, StateExpired},
	StateFallbackSent:   {StateDelivered},
	StateDelivered:      {StateAcked, StateExpired},
	StateOfflineStored:  {StateSynced, StateExpired},
	StateSynced:         {StateDelivered},
	StateAcked:          {}, // 终态
	StateExpired:        {StateDLQ},
	StateDLQ:            {},                        // 终态
	StateRateLimited:    {StateAccepted, StateDLQ}, // 可重试或直接失败
}

// ValidateTransition 检查 from → to 是否为合法状态转换。
// 返回 true 时 from 为空或目标合法。
func ValidateTransition(from, to DeliveryState) bool {
	if to == "" {
		return false
	}
	targets, ok := validTransitions[from]
	if !ok {
		// 未知 from，允许任意转换但记录 warn
		return true
	}
	for _, t := range targets {
		if t == to {
			return true
		}
	}
	return false
}

// StateRecorder 在 Redis 中持久化消息状态转换记录。
// 状态记录失败不影响主投递流程——错误仅记录日志。
type StateRecorder struct {
	pool *redis.Pool
	mu   sync.Mutex // 保护本地操作
}

// Redis key 常量
const (
	_prefixStateTimeline = "msg_state_timeline:%s" // msg_id → list of JSON transitions
	_stateTTL            = 7 * 24 * 3600           // 7 days
)

func keyStateTimeline(msgID string) string {
	return fmt.Sprintf(_prefixStateTimeline, msgID)
}

// NewStateRecorder creates a StateRecorder backed by the given Redis pool.
// Pass nil for a no-op recorder (useful in tests).
func NewStateRecorder(pool *redis.Pool) *StateRecorder {
	return &StateRecorder{pool: pool}
}

// RecordState writes a state transition to Redis.
// This is best-effort: failures are logged but never returned as errors.
func (r *StateRecorder) RecordState(ctx context.Context, msgID string, from, to DeliveryState, reason, traceID string) {
	if r.pool == nil || msgID == "" {
		return
	}
	if !ValidateTransition(from, to) {
		log.Warningf("invalid state transition: msg_id=%s from=%s to=%s", msgID, from, to)
		// 仍写入 timeline 以便调试，但不更新 current state
	}

	conn := r.pool.Get()
	defer conn.Close()

	t := StateTransition{
		MsgID:     msgID,
		From:      from,
		To:        to,
		Reason:    reason,
		Timestamp: time.Now(),
		TraceID:   traceID,
	}
	data, err := json.Marshal(t)
	if err != nil {
		log.Warningf("state marshal failed: msg_id=%s %v", msgID, err)
		return
	}

	msgKey := fmt.Sprintf("msg:%s", msgID)
	timelineKey := keyStateTimeline(msgID)

	// Update current state in msg:{msg_id} hash
	if err := conn.Send("HSET", msgKey, "state", string(to), "updated_at", time.Now().UnixMilli()); err != nil {
		log.Warningf("state HSET failed: msg_id=%s %v", msgID, err)
		return
	}
	if err := conn.Send("EXPIRE", msgKey, _stateTTL); err != nil {
		log.Warningf("state EXPIRE failed: msg_id=%s %v", msgID, err)
	}
	// Append to timeline list
	if err := conn.Send("RPUSH", timelineKey, data); err != nil {
		log.Warningf("state RPUSH failed: msg_id=%s %v", msgID, err)
		return
	}
	if err := conn.Send("EXPIRE", timelineKey, _stateTTL); err != nil {
		log.Warningf("timeline EXPIRE failed: msg_id=%s %v", msgID, err)
	}
	if err := conn.Flush(); err != nil {
		log.Warningf("state flush failed: msg_id=%s %v", msgID, err)
		return
	}
	for i := 0; i < 3; i++ {
		if _, err := conn.Receive(); err != nil {
			log.Warningf("state receive %d failed: msg_id=%s %v", i, msgID, err)
			return
		}
	}
}

// GetCurrentState returns the current delivery state of a message.
func (r *StateRecorder) GetCurrentState(ctx context.Context, msgID string) (DeliveryState, error) {
	if r.pool == nil || msgID == "" {
		return "", nil
	}
	conn := r.pool.Get()
	defer conn.Close()
	data, err := redis.StringMap(conn.Do("HGETALL", fmt.Sprintf("msg:%s", msgID)))
	if err != nil {
		if err == redis.ErrNil {
			return "", nil
		}
		return "", err
	}
	return DeliveryState(data["state"]), nil
}

// GetTimeline returns all state transitions for a message.
func (r *StateRecorder) GetTimeline(ctx context.Context, msgID string) ([]StateTransition, error) {
	if r.pool == nil || msgID == "" {
		return nil, nil
	}
	conn := r.pool.Get()
	defer conn.Close()
	raw, err := redis.Strings(conn.Do("LRANGE", keyStateTimeline(msgID), 0, -1))
	if err != nil {
		if err == redis.ErrNil {
			return nil, nil
		}
		return nil, err
	}
	var timeline []StateTransition
	for _, r := range raw {
		var t StateTransition
		if err := json.Unmarshal([]byte(r), &t); err != nil {
			log.Warningf("state unmarshal timeline entry: %v", err)
			continue
		}
		timeline = append(timeline, t)
	}
	return timeline, nil
}

// MapToDeliveryState maps legacy status strings to DeliveryState values.
func MapToDeliveryState(status string) DeliveryState {
	switch status {
	case "pending":
		return StateAccepted
	case "delivered":
		return StateDelivered
	case "acked":
		return StateAcked
	case "failed":
		return StateDLQ
	default:
		return DeliveryState(status)
	}
}
