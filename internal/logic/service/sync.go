// Package service 提供 IM 业务逻辑层的核心服务实现。
package service

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/dao"
	log "github.com/Terry-Mao/goim/pkg/log"
)

// DirectPusher 定义直接推送接口，负责通过 gRPC 将消息推送到指定会话所在的 comet 节点。
// 这是 SyncService 对底层推送能力的抽象，便于测试时替换为 mock 实现。
type DirectPusher interface {
	// DirectPush 向指定会话列表推送消息。
	// sessions: 目标会话列表（通常属于同一用户的不同设备）
	// op: 操作码，标识消息类型（如 OpSyncReply 表示同步回复）
	// body: 已序列化的协议体字节数组
	DirectPush(ctx context.Context, sessions []*Session, op int32, body []byte) ([]*Session, error)
}

// SyncService 负责离线消息同步和多设备消息同步。
//
// 核心职责：
//  1. 用户上线时，从 Redis 离线队列拉取遗漏的消息并推送到其所有在线设备
//  2. 提供主动拉取离线消息列表的接口（分页查询）
//
// 工作流程：
//
//	当消息被推送到用户但某些设备不在线时，消息 ID 会被写入该用户的 Redis 离线队列。
//	用户下次上线时，OnUserOnline 会消费这个队列，逐条取出消息内容并推送。
//	每条消息的状态（如已 ACK）都会被检查，已确认的消息会从队列中清理。
type SyncService struct {
	dao     dao.MessageDAO  // 消息数据访问层，负责 Redis 离线队列和消息状态的读写
	sessMgr *SessionManager // 会话管理器，用于查询用户当前在线的所有设备会话
	pusher  DirectPusher    // 直接推送器，将同步消息发送到指定会话所在的 comet 节点
}

// NewSyncService 创建一个 SyncService 实例。
// d: 消息 DAO 实现
// sessMgr: 会话管理器
// pusher: 直接推送器实现
func NewSyncService(d dao.MessageDAO, sessMgr *SessionManager, pusher DirectPusher) *SyncService {
	return &SyncService{
		dao:     d,
		sessMgr: sessMgr,
		pusher:  pusher,
	}
}

// OnUserOnline 用户上线时的回调，负责检测并推送离线期间遗漏的消息。
//
// 参数：
//   - uid: 上线用户的 ID
//   - lastSeq: 用户客户端上报的最后一条消息序号（seq），表示客户端当前已接收到的位置。
//     seq 是递增的消息序列号，用于标记消息在用户维度下的顺序，seq > lastSeq 的就是缺失消息。
//
// 执行流程：
//  1. 从 Redis 离线队列查询 lastSeq 之后的消息 ID 列表（最多 100 条）
//  2. 如果没有离线消息，直接返回
//  3. 查询用户当前所有在线设备的会话
//  4. 逐条取出消息内容，跳过已 ACK（已确认）的消息
//  5. 为每条消息重新构建协议体并通过 gRPC 推送到所有在线设备
//
// 注意：
//   - 已 ACK 的消息不会重复推送，但会从离线队列中移除
//   - 消息 body 在 Redis 中以 base64 编码存储，取出时需要解码
//   - 如果用户没有任何在线设备，消息不会丢失——它们仍保留在离线队列中，下次上线再推送
func (s *SyncService) OnUserOnline(ctx context.Context, uid int64, lastSeq int64) error {
	// 第一步：从 Redis 离线队列获取 lastSeq 之后的消息 ID 列表
	// float64(lastSeq) 是 Redis ZSet 的 score，用于范围查询
	// 100 是单次拉取上限
	msgIDs, err := s.dao.GetOfflineQueue(ctx, uid, float64(lastSeq), 100)
	if err != nil {
		return fmt.Errorf("get offline queue: %w", err)
	}

	// 没有离线消息，无需同步
	if len(msgIDs) == 0 {
		return nil
	}

	log.Infof("syncing offline messages: uid=%d count=%d last_seq=%d", uid, len(msgIDs), lastSeq)

	// 第二步：获取用户当前所有在线设备的会话
	// 注意：互踢仅限"同一用户+同一设备"（uid + device_id），不同设备（如手机、iPad、PC）
	// 可以同时在线，因此这里可能返回多个 session，需要推送到每一台设备
	// 如果用户没有任何在线设备，消息保留在队列中等待下次上线
	sessions, err := s.sessMgr.GetSessions(ctx, uid)
	if err != nil || len(sessions) == 0 {
		return nil
	}

	// 第三步：逐条处理离线消息
	// seq 从 lastSeq 开始递增，为每条消息分配新的序列号
	seq := lastSeq
	for _, msgID := range msgIDs {
		// 获取消息的完整元数据（状态、发送方、接收方、时间戳、消息体）
		msgData, err := s.dao.GetMessageStatus(ctx, msgID)
		if err != nil || len(msgData) == 0 {
			continue
		}

		// 如果消息已被确认（ACK），说明其他设备已经处理过，直接清理队列
		status := msgData["status"]
		if status == MsgStatusAcked {
			s.dao.RemoveFromOfflineQueue(ctx, uid, msgID)
			continue
		}

		// 递增序号，构建同步消息体
		seq++
		mb := &protocol.MsgBody{
			MsgID: msgID,
			Seq:   seq,
		}

		// 从 Redis 返回的字符串字段中解析消息元数据
		// msgData 中的值都是字符串格式（Redis hash 存储），需要解析为数值
		if v, ok := msgData["from_uid"]; ok {
			fmt.Sscanf(v, "%d", &mb.FromUID)
		}
		if v, ok := msgData["to_uid"]; ok {
			fmt.Sscanf(v, "%d", &mb.ToUID)
		}
		if v, ok := msgData["created_at"]; ok {
			fmt.Sscanf(v, "%d", &mb.Timestamp)
		}

		// 消息体以 base64 编码存储，需要解码还原原始字节
		if v, ok := msgData["body"]; ok && v != "" {
			if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
				mb.Content = decoded
			}
		}

		// 构建同步回复包
		// CurrentSeq: 当前消息的序号
		// HasMore: false 表示这批已处理完（线上推送逐条发送，不是批量返回）
		syncBody := &protocol.SyncReplyBody{
			CurrentSeq: seq,
			HasMore:    false,
			Messages:   []*protocol.MsgBody{mb},
		}

		// 序列化为协议字节数组
		syncBytes, err := protocol.MarshalSyncReply(syncBody)
		if err != nil {
			log.Warningf("marshal sync reply failed: %v", err)
			continue
		}

		// 推送到该用户的所有在线设备（手机、PC、Web 等）
		for _, sess := range sessions {
			if sess == nil {
				continue
			}
			_, _ = s.pusher.DirectPush(ctx, []*Session{sess}, protocol.OpSyncReply, syncBytes)
		}
	}

	return nil
}

// GetOfflineMessages 主动拉取离线消息列表，返回指定范围的离线消息详情。
//
// 与 OnUserOnline 的区别：
//   - OnUserOnline 是服务端主动推送——上线时自动触发，逐条推送到所有设备
//   - GetOfflineMessages 是客户端主动拉取——客户端可以分页请求，一次返回一批消息
//
// 参数：
//   - uid: 用户 ID
//   - lastSeq: 起始序号，只拉取 seq > lastSeq 的消息
//   - limit: 每次拉取的数量上限（1-200，超出范围自动修正为 100）
//
// 返回值：
//   - SyncReplyBody: 包含消息列表、当前序号、是否还有更多消息
//   - error: 查询失败时的错误信息
func (s *SyncService) GetOfflineMessages(ctx context.Context, uid int64, lastSeq int64, limit int32) (*protocol.SyncReplyBody, error) {
	// 参数校验：limit 必须在有效范围内
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	// 从 Redis 离线队列获取消息 ID 列表
	msgIDs, err := s.dao.GetOfflineQueue(ctx, uid, float64(lastSeq), int(limit))
	if err != nil {
		return nil, fmt.Errorf("get offline queue: %w", err)
	}

	// 构建回复体
	// HasMore: 如果返回的消息数量达到 limit，说明可能还有更多消息
	reply := &protocol.SyncReplyBody{
		CurrentSeq: lastSeq,
		HasMore:    len(msgIDs) >= int(limit),
	}

	// 逐条获取消息详情并填充到回复列表
	seq := lastSeq
	for _, msgID := range msgIDs {
		msgData, err := s.dao.GetMessageStatus(ctx, msgID)
		if err != nil || len(msgData) == 0 {
			continue
		}

		// 跳过已确认的消息并清理队列
		status := msgData["status"]
		if status == MsgStatusAcked {
			s.dao.RemoveFromOfflineQueue(ctx, uid, msgID)
			continue
		}

		seq++
		mb := &protocol.MsgBody{
			MsgID: msgID,
			Seq:   seq,
		}
		// 从 Redis hash 字段解析消息元数据（Redis 存储均为字符串，需转换）
		if v, ok := msgData["from_uid"]; ok {
			fmt.Sscanf(v, "%d", &mb.FromUID)
		}
		if v, ok := msgData["to_uid"]; ok {
			fmt.Sscanf(v, "%d", &mb.ToUID)
		}
		if v, ok := msgData["created_at"]; ok {
			fmt.Sscanf(v, "%d", &mb.Timestamp)
		}
		// 解码 base64 编码的消息体
		if v, ok := msgData["body"]; ok && v != "" {
			if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
				mb.Content = decoded
			}
		}

		// 追加到消息列表，并更新当前序号
		reply.Messages = append(reply.Messages, mb)
		reply.CurrentSeq = seq
	}

	return reply, nil
}
