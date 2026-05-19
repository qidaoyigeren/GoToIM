// 离线消息合并策略 —— Phase 2 离线消息去重与合并。
//
// 当同一业务对象（如订单）短时间内产生多条状态变更通知时，
// 离线用户不需要收到所有中间态，可以合并为最新状态以降低离线堆积。
//
// 合并条件（可配置）：
//   1. business_type == "order"
//   2. biz_id（如 order_id）相同
//   3. user_id 相同
//   4. 时间窗口内（默认 30s）
//
// 合并策略：
//   - 更新旧消息 payload 为最新 BizEnvelope
//   - 保留原 seq，避免 cursor 混乱
//   - 更新时间戳

package router

import (
	"encoding/json"
	"time"

	log "github.com/Terry-Mao/goim/pkg/log"
)

// MergePolicy determines whether and how two offline messages should be merged.
type MergePolicy interface {
	// ShouldMerge returns true if incoming can merge into existing.
	ShouldMerge(existing, incoming *OfflineMessage) bool
	// Merge combines two messages, producing the merged result.
	Merge(existing, incoming *OfflineMessage) *OfflineMessage
}

// OfflineMessage is the offline representation of a queued message.
type OfflineMessage struct {
	MsgID        string `json:"msg_id"`
	UserID       string `json:"user_id"`
	Seq          int64  `json:"seq"`
	BusinessType string `json:"business_type"`
	BizID        string `json:"biz_id"`
	EventType    string `json:"event_type"`
	Priority     string `json:"priority"`
	TTLSeconds   int32  `json:"ttl_seconds"`
	Payload      []byte `json:"payload"`
	TraceID      string `json:"trace_id"`
	CreatedAtMS  int64  `json:"created_at_unix_ms"`
	UpdatedAtMS  int64  `json:"updated_at_unix_ms"`
}

// OrderStatusMergePolicy 将同一订单的连续状态变更合并为最新状态。
type OrderStatusMergePolicy struct {
	// WindowMS is the maximum age of an existing message to consider for merging.
	WindowMS int64 // default: 30000 (30s)
}

// DefaultOrderMergePolicy returns a merge policy with sensible defaults.
func DefaultOrderMergePolicy() *OrderStatusMergePolicy {
	return &OrderStatusMergePolicy{WindowMS: 30000}
}

// ShouldMerge returns true when:
//   - Both messages belong to "order" business_type
//   - They share the same biz_id (order_id) and user_id
//   - The existing message is within the merge window
func (p *OrderStatusMergePolicy) ShouldMerge(existing, incoming *OfflineMessage) bool {
	if existing == nil || incoming == nil {
		return false
	}
	if existing.BusinessType != "order" || incoming.BusinessType != "order" {
		return false
	}
	if existing.BizID == "" || incoming.BizID == "" {
		return false
	}
	if existing.BizID != incoming.BizID {
		return false
	}
	if existing.UserID != incoming.UserID {
		return false
	}
	window := p.WindowMS
	if window <= 0 {
		window = 30000
	}
	nowMS := time.Now().UnixMilli()
	if nowMS-existing.CreatedAtMS > window {
		return false
	}
	return true
}

// Merge combines two order status messages, keeping the old seq but new payload.
func (p *OrderStatusMergePolicy) Merge(existing, incoming *OfflineMessage) *OfflineMessage {
	merged := *existing // copy
	merged.Payload = incoming.Payload
	merged.EventType = incoming.EventType
	merged.Priority = incoming.Priority
	merged.TTLSeconds = incoming.TTLSeconds
	merged.TraceID = incoming.TraceID
	merged.BizID = incoming.BizID
	merged.UpdatedAtMS = time.Now().UnixMilli()
	return &merged
}

// MergeChecker wraps the merge logic used by the dispatch engine when writing
// to the offline queue. It tries to find and merge incoming with an existing
// offline message, returning the msgID if merged or empty if newly queued.
type MergeChecker struct {
	policy MergePolicy
	// findExisting looks up an existing mergeable message by uid+bizType+bizID.
	// Returns nil if no mergeable message exists.
	findExisting func(userID string, bizType, bizID string) (*OfflineMessage, error)
	// updateExisting replaces an existing offline message's payload.
	updateExisting func(msgID string, merged *OfflineMessage) error
}

// NewMergeChecker creates a MergeChecker.
func NewMergeChecker(policy MergePolicy) *MergeChecker {
	return &MergeChecker{policy: policy}
}

// SetFindFunc sets the function used to look up existing mergeable messages.
func (m *MergeChecker) SetFindFunc(fn func(string, string, string) (*OfflineMessage, error)) {
	m.findExisting = fn
}

// SetUpdateFunc sets the function used to update a merged message.
func (m *MergeChecker) SetUpdateFunc(fn func(string, *OfflineMessage) error) {
	m.updateExisting = fn
}

// TryMerge checks whether incoming should be merged into an existing offline message.
// Returns the msgID of the existing message if merged, empty string if a new queue entry is needed.
func (m *MergeChecker) TryMerge(incoming *OfflineMessage) (merged bool, existingMsgID string) {
	if m.policy == nil || m.findExisting == nil || incoming == nil {
		return false, ""
	}
	existing, err := m.findExisting(incoming.UserID, incoming.BusinessType, incoming.BizID)
	if err != nil || existing == nil {
		return false, ""
	}
	if !m.policy.ShouldMerge(existing, incoming) {
		return false, ""
	}
	mergedMsg := m.policy.Merge(existing, incoming)
	if m.updateExisting != nil {
		if err := m.updateExisting(existing.MsgID, mergedMsg); err != nil {
			log.Warningf("merge update failed: existing=%s incoming=%s err=%v",
				existing.MsgID, incoming.MsgID, err)
			return false, ""
		}
	}
	log.Infof("offline message merged: existing=%s incoming=%s biz_type=%s biz_id=%s",
		existing.MsgID, incoming.MsgID, incoming.BusinessType, incoming.BizID)
	return true, existing.MsgID
}

// ParseOfflineEnvelope tries to parse an OfflineMessage from BizEnvelope JSON bytes.
func ParseOfflineEnvelope(data []byte, msgID, userID string, seq int64) *OfflineMessage {
	msg := &OfflineMessage{
		MsgID:       msgID,
		UserID:      userID,
		Seq:         seq,
		CreatedAtMS: time.Now().UnixMilli(),
	}
	// Try parsing as BizEnvelope
	var env struct {
		BizID        string `json:"biz_id"`
		BusinessType string `json:"business_type"`
		EventType    string `json:"event_type"`
		Priority     string `json:"priority"`
		TTLSeconds   int32  `json:"ttl_seconds"`
		TraceID      string `json:"trace_id"`
	}
	if err := json.Unmarshal(data, &env); err == nil {
		msg.BusinessType = env.BusinessType
		msg.BizID = env.BizID
		msg.EventType = env.EventType
		msg.Priority = env.Priority
		msg.TTLSeconds = env.TTLSeconds
		msg.TraceID = env.TraceID
	}
	msg.Payload = data
	return msg
}
