// logic 层 - 连接管理模块
// 负责处理客户端的连接（Connect）、断开（Disconnect）、心跳（Heartbeat）
// 以及 Comet 层上报的消息接收（Receive）。是 Logic 层对外的核心 RPC 接口。
package logic

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/model"
	"github.com/Terry-Mao/goim/internal/logic/service"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/google/uuid"
)

// Connect 处理客户端连接请求。
//
// Comet 层在收到客户端 WebSocket/TCP 连接后，会解析出客户端携带的 token（JSON 格式），
// 然后通过 RPC 调用此方法完成 Logic 层的认证与会话创建。
//
// 参数说明：
//   - ctx:        上下文，用于超时控制与链路追踪
//   - server:     Comet 服务器的唯一标识（如 "comet-1"），用于标识该连接所在的 Comet 节点
//   - cookie:     客户端连接的 cookie 值（当前未使用，预留字段）
//   - token:      客户端连接时携带的 JSON 认证令牌，包含用户 ID、房间 ID、设备信息等
//
// 返回值：
//   - mid:        用户唯一 ID（member id），用于标识登录用户
//   - key:        连接标识 key，用于唯一标识一个连接（如果客户端未提供，则自动生成 UUID）
//   - roomID:     房间 ID，客户端加入的房间标识
//   - accepts:    客户端接受的操作码列表（如 OpPushMsgAck、OpSyncReq 等）
//   - hb:         心跳超时时间（秒）= Heartbeat × HeartbeatMax，超过此时间无心跳视为掉线
//   - err:        错误信息
//
// 核心流程：
//  1. 解析 token JSON → 提取 mid、key、roomID、设备信息
//  2. 生成连接 key（若客户端未提供）
//  3. 创建 Session 记录（包含设备追踪字段）
//  4. 异步触发离线消息同步（不阻塞连接建立）
func (l *Logic) Connect(c context.Context, server, cookie string, token []byte) (mid int64, key, roomID string, accepts []int32, hb int64, err error) {
	// 步骤1: 定义 token 解析结构体
	// params 对应客户端在建立连接时携带的 JSON 认证信息
	var params struct {
		Mid      int64   `json:"mid"`       // 用户 ID
		Key      string  `json:"key"`       // 连接标识（客户端可不传，服务端自动生成）
		RoomID   string  `json:"room_id"`   // 目标房间 ID
		Platform string  `json:"platform"`  // 平台类型（如 "web"、"ios"、"android"）
		DeviceID string  `json:"device_id"` // 设备唯一标识（用于多设备管理）
		Accepts  []int32 `json:"accepts"`   // 客户端接受的操作码列表
		LastSeq  int64   `json:"last_seq"`  // 客户端本地最后的消息序号，用于增量同步离线消息
	}
	if err = json.Unmarshal(token, &params); err != nil {
		log.Errorf("json.Unmarshal(%s) error(%v)", token, err)
		return
	}

	// 步骤2: 提取并赋值返回值
	mid = params.Mid         // 用户 ID
	roomID = params.RoomID   // 房间 ID
	accepts = params.Accepts // 客户端接受的操作码列表

	// 心跳超时计算：Heartbeat（单次心跳间隔）× HeartbeatMax（最大允许丢失次数）
	// 例如：心跳间隔 30s，最大丢失 3 次 → 90s 内无心跳判定为掉线
	hb = int64(l.c.Node.Heartbeat) * int64(l.c.Node.HeartbeatMax)

	// 步骤3: 生成连接 key
	// key 用于在整个系统中唯一标识一个连接，Comet 用它来路由消息到具体的客户端
	if key = params.Key; key == "" {
		key = uuid.New().String() // 客户端未提供则自动生成 UUID
	}

	// 步骤4: 处理设备标识（DeviceID）
	// 如果客户端未明确传递 DeviceID，则使用 "平台:key" 组合作为兜底标识
	// 这样即使客户端不传，也能保证多设备登录时的 session 区分
	if params.DeviceID == "" {
		params.DeviceID = params.Platform + ":" + key
	}

	// 步骤5: 创建 Session 对象
	// Session 记录了连接的核心元数据，存储于 Redis/内存中
	// SID: 会话唯一 ID（每次连接生成新的，保证幂等）
	// UID: 用户 ID（mid）
	// Key: 连接 key（Comet 定位连接用）
	// DeviceID + Platform: 多设备管理的基础标识
	// Server: 记录该连接所在的 Comet 节点
	// CreatedAt / LastHBAt: 时间戳用于心跳超时判断
	sess := &service.Session{
		SID:       uuid.New().String(), // 每次连接生成全新的会话 ID
		UID:       mid,
		Key:       key,
		DeviceID:  params.DeviceID,
		Platform:  params.Platform,
		Server:    server, // 记录连接归属的 Comet 节点
		CreatedAt: time.Now(),
		LastHBAt:  time.Now(),
	}
	// 将 Session 持久化到存储层（Redis）
	if err = l.sessionMgr.Create(c, sess); err != nil {
		log.Errorf("session create(%d,%s,%s) error(%v)", mid, key, server, err)
		return
	}

	log.Infof("conn connected key:%s server:%s mid:%d device:%s platform:%s", key, server, mid, params.DeviceID, params.Platform)

	// 步骤6: 异步触发离线消息同步
	// 使用 goroutine + 独立 context（5s 超时）在后台执行，不阻塞 Connect 的返回
	// 这样客户端可以立即收到连接成功响应，离线消息通过后续的 Sync 操作下发
	go func() {
		// 使用 context.Background() 而非入参 ctx，因为入参 ctx 可能在 Connect 返回后被取消
		syncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// OnUserOnline 触发该用户的离线消息推送：将缓存中的离线消息投递到对应的 Comet 队列
		if err := l.syncSvc.OnUserOnline(syncCtx, mid, params.LastSeq); err != nil {
			log.Warningf("on user online sync failed: mid=%d err=%v", mid, err)
		}
	}()
	return
}

// Disconnect 处理客户端断开连接请求。
//
// Comet 层检测到客户端断开（主动关闭或心跳超时）后，通过 RPC 调用此方法。
//
// 参数说明：
//   - ctx:    上下文
//   - mid:    用户 ID
//   - key:    连接 key（用于精确定位要断开的连接）
//   - server: Comet 服务器标识
//
// 返回值：
//   - has: 是否成功断开（true 表示该 session 确实存在且已被清理）
//   - err: 错误信息
func (l *Logic) Disconnect(c context.Context, mid int64, key, server string) (has bool, err error) {
	// 调用 sessionMgr 执行断开逻辑：
	// 1. 清理 Redis 中的 session 记录
	// 2. 清理 Comet 节点上的连接映射
	// 3. 更新在线状态为离线
	if sessErr := l.sessionMgr.Disconnect(c, mid, key, server); sessErr != nil {
		log.Warningf("session disconnect(%d,%s,%s) error(%v)", mid, key, server, sessErr)
		err = sessErr
		return
	}
	has = true
	log.Infof("conn disconnected key:%s server:%s mid:%d", key, server, mid)
	return
}

// Heartbeat 处理客户端心跳请求。
//
// 客户端定期（由 Heartbeat 配置决定）发送心跳包，Comet 检测到有效心跳后
// 通过 RPC 调用此方法更新 session 的最后心跳时间。
//
// 参数说明：
//   - ctx:    上下文
//   - mid:    用户 ID
//   - key:    连接 key
//   - server: Comet 服务器标识
//
// 核心作用：防止 session 因心跳超时被清理，保持连接活跃状态。
func (l *Logic) Heartbeat(c context.Context, mid int64, key, server string) (err error) {
	// 更新 Redis 中该 session 的 LastHBAt 字段
	if err = l.sessionMgr.Heartbeat(c, key, mid); err != nil {
		log.Warningf("session heartbeat(%d,%s,%s) error(%v)", mid, key, server, err)
		return
	}
	log.Infof("conn heartbeat key:%s server:%s mid:%d", key, server, mid)
	return
}

// RenewOnline 续期 Comet 节点的在线状态。
//
// Comet 定期通过此接口上报自身存活状态和当前各房间的在线人数。
// Logic 层据此维护全局的"哪些 Comet 节点在线"和"各房间分布"信息，
// 供消息推送时做路由决策。
//
// 参数说明：
//   - ctx:       上下文
//   - server:    Comet 服务器标识
//   - roomCount: 该 Comet 节点各房间的在线连接数统计 (roomID → count)
//
// 返回值：
//   - map[string]int32: 全局各房间的在线人数汇总
//   - error:            错误信息
func (l *Logic) RenewOnline(c context.Context, server string, roomCount map[string]int32) (map[string]int32, error) {
	// 构建 Online 对象，记录该 Comet 的当前状态
	online := &model.Online{
		Server:    server,
		RoomCount: roomCount, // 各房间连接数分布
		Updated:   time.Now().Unix(),
	}
	// 将 Comet 在线信息写入 Redis（或数据库）
	if err := l.dao.AddServerOnline(context.Background(), server, online); err != nil {
		return nil, err
	}
	// 读取全局房间人数统计（读锁保护）
	l.nodesMu.RLock()
	rc := l.roomCount
	l.nodesMu.RUnlock()
	return rc, nil
}

// Receive 处理 Comet 层上报的客户端消息。
//
// 当 Comet 收到客户端发来的非心跳数据帧时，会通过 RPC 将消息转发到 Logic 层。
// Logic 根据消息的操作码（Op）分发处理：ACK 确认、Sync 同步请求等。
//
// 参数说明：
//   - ctx: 上下文
//   - mid: 用户 ID
//   - p:   协议消息体（Proto），包含操作码和消息体
//
// 核心流程（按操作码分发）：
//   - OpPushMsgAck → 处理客户端对推送消息的 ACK 确认
//   - OpSyncReq   → 处理客户端请求同步离线消息
//   - 其他        → 打印日志（不做处理）
//
// 返回值：
//   - err: 错误信息。注意：即使出错也不影响 Comet 的连接状态。
func (l *Logic) Receive(c context.Context, mid int64, p *protocol.Proto) (err error) {
	switch p.Op {
	case protocol.OpPushMsgAck:
		// --- ACK 确认消息 ---
		// 客户端收到推送消息后回发 ACK，确保消息已被消费。
		// 配合消息队列的 ACK 机制实现"可靠推送"：消息只有收到 ACK 后才从待确认队列中移除。
		ack, err := protocol.UnmarshalAckBody(p.Body)
		if err != nil {
			log.Errorf("unmarshal ack body error(%v)", err)
			return err
		}
		// 调用 Router 的 HandleACK：从待确认队列中移除对应的消息
		if err := l.router.HandleACK(c, mid, ack.MsgID); err != nil {
			log.Errorf("handle ack error(%v) mid:%d msg_id:%s", err, mid, ack.MsgID)
			return err
		}
		log.Infof("ack received mid:%d msg_id:%s seq:%d", mid, ack.MsgID, ack.Seq)

	case protocol.OpSyncReq:
		// --- 同步请求消息 ---
		// 客户端上线后发送 Sync 请求，拉取在离线期间产生的消息。
		// LastSeq 用于增量同步：服务端只返回 seq > LastSeq 的消息。
		syncReq, err := protocol.UnmarshalSyncReq(p.Body)
		if err != nil {
			log.Errorf("unmarshal sync req error(%v)", err)
			return err
		}
		// 从消息存储中拉取 LastSeq 之后的消息，limit 控制单次拉取数量
		reply, err := l.syncSvc.GetOfflineMessages(c, mid, syncReq.LastSeq, syncReq.Limit)
		if err != nil {
			log.Errorf("sync offline error(%v) mid:%d last_seq:%d", err, mid, syncReq.LastSeq)
			return err
		}
		// 关键设计：直接修改入参 Proto 的操作码和 Body
		// Logic 将 SyncReply 写入 p.Body，Comet 收到 RPC 返回后
		// 直接将新 Body 写回客户端连接，无需额外处理
		replyBytes, err := protocol.MarshalSyncReply(reply)
		if err != nil {
			log.Errorf("marshal sync reply error(%v)", err)
			return err
		}
		p.Op = protocol.OpSyncReply // 将请求 Op 改为响应 Op
		p.Body = replyBytes         // 替换 Body 为同步结果
		log.Infof("sync request mid:%d last_seq:%d limit:%d msgs=%d", mid, syncReq.LastSeq, syncReq.Limit, len(reply.Messages))

	default:
		// 未识别的操作码，仅记录日志
		log.Infof("receive mid:%d message:%+v", mid, p)
	}
	return
}
