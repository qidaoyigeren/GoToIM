// ============================================================================
// 文件：ack.go
// 职责：消息 ACK（确认）处理 —— 客户端收到消息后回执确认，驱动消息状态流转。
// ============================================================================
//
// 一、什么是 ACK？
//
//   客户端收到推送消息后，向服务端发送 ACK 回执，告知"我已收到这条消息"。
//   这是 IM 系统可靠投递的关键环节：服务端只有收到 ACK 才会认为消息投递成功，
//   否则会通过离线队列等方式重试投递。
//
// 二、消息生命周期（状态机）
//
//   Pending ──推送成功──→ Delivered ──客户端ACK──→ Acked
//      │                                                    │
//      └──────────── 推送失败 ──→ Failed                    │
//                                                           │
//   所有终态（Acked / Failed）都会写入 idCache，供 IdempotencyChecker 快速去重。
//
// 三、ACKHandler 与 IdempotencyChecker 的关系
//
//   · ACKHandler 负责"写"：处理 ACK 请求，更新消息状态，写入幂等缓存。
//   · IdempotencyChecker 负责"读"：判断消息是否已处理过（查重）。
//   · 二者共享同一个 IdempotencyChecker 实例（idCache 字段），
//     确保 MarkSeen 写入后 IsDuplicate 能立即命中。
//
// 四、TrackMessage 的原子性保证
//
//   使用 Redis HSETNX（Hash Set If Not Exists）原子操作抢占消息。
//   多协程并发投递同一条消息时，只有第一个执行 HSETNX 的协程成功，
//   其余协程收到"已存在"错误后跳过，避免同一条消息被重复追踪。

package router

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// 消息状态常量。
// 与 service/ack.go 保持一致（此处独立定义是为了避免循环引用）。
const (
	MsgStatusPending   = "pending"   // 待投递：消息已写入 Redis，等待推送到客户端
	MsgStatusDelivered = "delivered" // 已送达：消息已推送到客户端，等待客户端 ACK 确认
	MsgStatusAcked     = "acked"     // 已确认：客户端已发送 ACK，投递完成（终态）
	MsgStatusFailed    = "failed"    // 投递失败：多次重试后仍无法送达（终态）
)

// ACKHandler 处理客户端的 ACK 回执。
//
// 核心职责：
//  1. 接收客户端 ACK，将消息状态从 Delivered 推进到 Acked。
//  2. 从离线队列中移除该消息（已确认，无需再推送）。
//  3. 发布 ACK 事件到 Kafka，供下游（如推送方）感知投递结果。
//  4. 将终态消息写入幂等缓存，加速后续重复消息的去重判定。
//  5. 提供消息追踪（TrackMessage）、标记送达/失败等辅助方法。
type ACKHandler struct {
	dao     dao.MessageDAO      // 消息状态持久化（Redis）
	pushDAO dao.PushDAO         // 推送事件发布（Kafka），可为 nil（不启用推送回调时）
	idCache *IdempotencyChecker // 终态消息内存缓存，服务于 IsDuplicate 快速路径
}

// NewACKHandler 创建 ACKHandler 实例。
//
// 参数：
//
//	d  - 消息 DAO（必填），用于读写 Redis 中的消息状态。
//	pd - 推送 DAO（可选），传 nil 表示不发布 ACK 事件到 Kafka。
func NewACKHandler(d dao.MessageDAO, pd dao.PushDAO) *ACKHandler {
	return &ACKHandler{
		dao:     d,
		pushDAO: pd,
		idCache: NewIdempotencyChecker(d), // 内部共享同一个检查器实例
	}
}

// HandleACK 处理客户端发来的 ACK 回执。
//
// 这是 ACKHandler 的核心方法，一次典型的 ACK 处理包含以下步骤：
//
//  1. 更新消息状态为 Acked（Redis）。
//     这一步是 ACK 的本质——将消息从"已送达未确认"推进到"已确认"终态。
//
//  2. 从离线队列移除该消息。
//     消息已被确认，不再需要离线重推。失败仅告警不中断，
//     因为离线队列有自身的 TTL 兜底，残留条目不会造成严重后果。
//
//  3. 发布 ACK 事件到 Kafka（如果 pushDAO 非 nil）。
//     通知消息发送方"投递成功"，发送方可据此更新 UI 状态（如"已读"标记）。
//     此步骤同样是 best-effort：失败不影响核心流程。
//
//  4. 写入幂等缓存（idCache.MarkSeen）。
//     将这条消息标记为"已处理"，后续重复投递时 IsDuplicate 可直接命中内存缓存。
//
// 错误处理策略：
//
//	只有步骤 1（更新状态）失败才算整体失败，因为状态更新是 ACK 的核心语义。
//	步骤 2、3 失败只记录告警日志——它们属于"尽力而为"的附加操作。
func (a *ACKHandler) HandleACK(ctx context.Context, uid int64, msgID string) error {
	// 步骤 1：更新消息状态为已确认（核心操作，失败则整体返回错误）
	if err := a.dao.UpdateMessageStatus(ctx, msgID, MsgStatusAcked); err != nil {
		return fmt.Errorf("update msg status: %w", err)
	}

	// 步骤 2：从离线队列移除（best-effort，失败不中断）
	if err := a.dao.RemoveFromOfflineQueue(ctx, uid, msgID); err != nil {
		log.Warningf("remove from offline queue: uid=%d msg_id=%s err=%v", uid, msgID, err)
	}

	// 步骤 3：发布 ACK 事件到 Kafka（best-effort，pushDAO 为 nil 时跳过）
	if a.pushDAO != nil {
		if err := a.pushDAO.PublishACK(ctx, msgID, uid, MsgStatusAcked); err != nil {
			log.Warningf("publish ack event: uid=%d msg_id=%s err=%v", uid, msgID, err)
		}
	}

	// 步骤 4：写入幂等缓存，加速后续去重判定
	a.idCache.MarkSeen(msgID)

	metrics.MsgAckTotal.Inc()
	log.Infof("ack processed: uid=%d msg_id=%s", uid, msgID)
	return nil
}

// TrackMessage 将一条消息写入追踪（首次记录，幂等操作）。
//
// 在消息初次投递前调用，用于在 Redis 中建立消息的状态记录。
// 返回 nil 表示当前协程成功"抢占"了这条消息的追踪权。
// 返回 error 表示该消息已被其他协程抢占（重复调用），调用方应跳过后续投递。
//
// 原子性保证：
//
//	使用 Redis HSETNX 原子操作设置 status 字段。
//	HSETNX 语义：仅当字段不存在时才写入，存在则返回 false。
//	多协程并发调用时，只有一个能成功写入 Pending 状态，
//	其余协程收到"already tracked"错误后跳过，避免重复追踪。
//
// 写入字段：
//
//	from_uid, to_uid, op  — 消息元信息（发送方、接收方、操作类型）
//	body                   — 消息体（Base64 编码，避免二进制数据在 Redis 中的存储问题）
//	retry_cnt: 0           — 重试计数，初始为 0
//	created_at, updated_at — 时间戳（Unix 毫秒）
func (a *ACKHandler) TrackMessage(ctx context.Context, msgID string, fromUID, toUID int64, op int32, body []byte) error {
	// 原子抢占：只有第一个执行的协程能成功写入 Pending 状态
	added, err := a.dao.SetMessageStatusNX(ctx, msgID, "status", MsgStatusPending)
	if err != nil {
		return fmt.Errorf("track message: %w", err)
	}
	if !added {
		// 已被其他协程抢占，调用方应跳过本次投递
		return fmt.Errorf("message already tracked: %s", msgID)
	}

	// 抢占成功，写入剩余字段
	fields := map[string]interface{}{
		"from_uid":   fromUID,
		"to_uid":     toUID,
		"op":         op,
		"body":       base64.StdEncoding.EncodeToString(body), // 二进制 → 文本，安全存入 Redis
		"retry_cnt":  0,
		"created_at": time.Now().UnixMilli(),
		"updated_at": time.Now().UnixMilli(),
	}
	return a.dao.SetMessageStatus(ctx, msgID, fields)
}

// MarkDelivered 将消息标记为"已送达"。
//
// 调用时机：消息成功推送到客户端后调用。
// 此时消息从 Pending 进入 Delivered 状态，等待客户端的 ACK 回执。
// 同时写入幂等缓存，防止短期内重复推送同一条消息。
func (a *ACKHandler) MarkDelivered(ctx context.Context, msgID string) error {
	if err := a.dao.UpdateMessageStatus(ctx, msgID, MsgStatusDelivered); err != nil {
		return err
	}
	a.idCache.MarkSeen(msgID)
	return nil
}

// MarkFailed 将消息标记为"投递失败"。
//
// 调用时机：多次重试后仍无法送达时调用。
// 进入 Failed 终态后不再重试，可配合告警机制通知运维排查。
// 注意：Failed 状态不写入幂等缓存，因为失败的消息后续可能被重新投递。
func (a *ACKHandler) MarkFailed(ctx context.Context, msgID string) error {
	return a.dao.UpdateMessageStatus(ctx, msgID, MsgStatusFailed)
}

// GetMessageStatus 查询消息的当前状态。
//
// 返回值：
//
//	正常情况返回状态字符串（pending / delivered / acked / failed）。
//	消息不存在时返回空字符串（不会报错）。
//	仅 Redis 查询本身出错时才返回 error。
func (a *ACKHandler) GetMessageStatus(ctx context.Context, msgID string) (string, error) {
	data, err := a.dao.GetMessageStatus(ctx, msgID)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}
	return data["status"], nil
}
