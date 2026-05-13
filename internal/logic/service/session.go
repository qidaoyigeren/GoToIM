package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	log "github.com/Terry-Mao/goim/pkg/log"
)

// 编译期接口实现检查：确保 *dao.Dao 实现了 dao.SessionDAO 接口。
// 如果 dao 包中修改了 SessionDAO 的签名而忘了同步实现，这行会在编译时报错。
// 这是 Go 里常用的"编译期契约验证"惯用法。
var _ dao.SessionDAO = (*dao.Dao)(nil)

// CometKicker 定义了"通知 Comet 服务器踢掉指定连接"的接口。
//
// 为什么需要这个接口？
// Session 存储在 Redis 中（由 Logic 层管理），而真实的 TCP/WebSocket 连接
// 在 Comet 服务器上。当 Logic 层判定需要踢人（如同一设备重复登录），
// 它必须通知 Comet 层关闭对应的底层连接。CometKicker 就是 Logic → Comet 的
// 反向通知通道。
//
// 此接口只有一个方法：
//   - server: 目标 Comet 节点标识（如 "comet-1"）
//   - key:    连接的唯一标识 key（Comet 通过 key 定位到具体的连接对象）
type CometKicker interface {
	KickConnection(ctx context.Context, server, key string) error
}

// Session 代表一个活跃的用户连接会话。
//
// 在整个 goim 架构中，连接由 Comet 层持有（TCP/WebSocket 长连接），
// 而会话元数据（Session）由 Logic 层管理，持久化在 Redis 中。
// 这种"连接与会话分离"的设计使得 Logic 层可以独立地对会话进行 CRUD，
// 而不需要关心底层协议是 TCP 还是 WebSocket。
//
// 字段说明：
//   - SID:       会话唯一 ID，每次连接时新生成（UUID），用于会话的精确操作
//   - UID:       用户 ID（mid, member id），关联到具体的用户
//   - Key:       连接 key，Comet 层用 key 来定位一个具体的 Channel（连接）
//     key 在整个系统中必须唯一；如果客户端不提供，服务端自动生成 UUID
//   - DeviceID:  设备唯一标识，用于实现"同设备互踢"逻辑（同一设备只保留最新连接）
//   - Platform:  平台类型（"android" / "ios" / "web"），用于多端在线管理
//   - Server:    Comet 服务器标识（如 "comet-1"），记录该连接分配到了哪台 Comet
//   - CreatedAt: 会话创建时间（毫秒精度）
//   - LastHBAt:  最后心跳时间（毫秒精度），用于心跳超时判活
//   - cachedAt:  本地内存缓存的写入时间（不持久化），用于缓存 TTL 管理
type Session struct {
	SID       string
	UID       int64
	Key       string
	DeviceID  string
	Platform  string
	Server    string
	CreatedAt time.Time
	LastHBAt  time.Time
	cachedAt  time.Time // 本地内存缓存的时间戳（小写 = 包外不可见）
}

// SessionManager 管理用户会话，提供"Redis 持久化 + 本地内存缓存"双层存储。
//
// 设计要点：
//
//  1. Redis 作为真实数据源（source of truth）
//     所有 session 写入都直接落到 Redis，保证 Logic 层多实例间的数据一致。
//     即使某台 Logic 实例挂了，session 数据也不会丢失。
//
//  2. 本地 sync.Map 作为读缓存
//     高频读取场景（如 IsOnline 检查）每次都查 Redis 代价太高，
//     因此以 uid 为 key 在本地内存中缓存该用户的 session 列表。
//     缓存过期时间 1 分钟（硬编码在 GetSessions 中），写操作会主动删除缓存。
//
//  3. CometKicker 的可选注入
//     SessionManager 专注于 session 数据的 CRUD，而"踢连接"是和 Comet 的交互。
//     通过 SetKicker 注入，实现了关注点分离：没有 kicker 时 session 照样读写正常。
type SessionManager struct {
	dao    dao.SessionDAO // Redis 持久化层接口
	local  sync.Map       // uid(int64) → []*Session（本地读缓存）
	ttl    time.Duration  // session 的 TTL 时长（Redis EXPIRE 时间）
	kicker CometKicker    // Comet 连接踢出器（可选注入，用于同设备互踢通知）
}

// NewSessionManager 创建一个 SessionManager 实例。
//
// ttl 参数来自配置文件的 Redis.Expire 字段，决定了 session 在 Redis 中的存活时间。
// session 的 TTL 由心跳（Heartbeat）定期刷新，所以只要客户端保持心跳，TTL 不会过期。
func NewSessionManager(d dao.SessionDAO, ttl time.Duration) *SessionManager {
	return &SessionManager{
		dao: d,
		ttl: ttl,
	}
}

// SetKicker 注入 CometKicker 实现。
//
// 为什么不放在 NewSessionManager 的构造函数参数中？
// CometPusher（kicker 的实现者）需要先建立到所有 Comet 节点的 gRPC 连接，
// 这发生在 Logic 初始化流程中 NewSessionManager 之后。因此采用两步初始化：
//  1. NewSessionManager(d, ttl)    — 创建 session 管理器
//  2. manager.SetKicker(cometPusher) — 注入踢连接能力
func (m *SessionManager) SetKicker(k CometKicker) {
	m.kicker = k
}

// Create 创建一个新 session。
//
// 调用时机：客户端 Connect 成功后，由 Logic.Connect() 调用。
//
// 流程：
//  1. 检查同一设备是否已有 session（同一用户 + 同一 device_id）
//  2. 如果旧 session 存在且 SID 不同 → 执行 Kick 踢掉旧连接（单设备登录语义）
//  3. 通过 DAO 将 session 写入 Redis
//  4. 删除本地缓存（因为该用户的 session 列表已变更）
//
// 关键设计：同设备互踢（Same-Device Kick）
// 这是 IM 系统的标准需求：用户在 iPhone 上登录后，之前在 iPhone 上的旧连接
// 应该被强制断开（因为那个连接已经不可用了）。但 iPad 上的连接不受影响
// （因为 DeviceID 不同）。
func (m *SessionManager) Create(ctx context.Context, sess *Session) error {
	// 步骤1: 检查同一设备是否已有 session
	// GetDeviceSession 查询 Redis key: device_session:{uid}:{device_id} → sid
	oldSID, _ := m.dao.GetDeviceSession(ctx, sess.UID, sess.DeviceID)
	if oldSID != "" && oldSID != sess.SID {
		// 存在旧 session 且 SID 不同 → 执行互踢
		log.Errorf("session kick: uid=%d device=%s old_sid=%s new_sid=%s",
			sess.UID, sess.DeviceID, oldSID, sess.SID)
		m.Kick(ctx, oldSID, sess.UID, sess.DeviceID)
	}

	// 步骤2: 将 session 写入 Redis（原子性由 Pipeline 保证）
	if err := m.dao.AddSession(ctx, sess.SID, sess.UID, sess.Key, sess.DeviceID, sess.Platform, sess.Server); err != nil {
		return fmt.Errorf("add session: %w", err)
	}

	// 步骤3: 删除本地缓存，使下次 GetSessions 重新从 Redis 拉取
	m.local.Delete(sess.UID)
	return nil
}

// GetSessions 获取某用户的所有活跃 session 列表。
//
// 调用场景：
//   - IsOnline：判断用户是否在线
//   - PushToUser：消息推送前获取用户的连接列表，确定要投递到哪些 key
//
// 缓存策略（Read-Aside 模式）：
//  1. 先查本地 sync.Map
//  2. 缓存命中且未过期（< 1 分钟）→ 直接返回
//  3. 缓存未命中或已过期 → 从 Redis 拉取 → 更新本地缓存 → 返回
//
// 为什么选择 1 分钟缓存？
// 在 GoIM 场景中，session 列表用于消息推送路由。如果每次推送都查 Redis，
// 高并发下会对 Redis 造成较大压力。1 分钟缓存意味着某用户的推送路由信息
// 可能过时最多 1 分钟，这个延迟在实际场景中是可以接受的。
func (m *SessionManager) GetSessions(ctx context.Context, uid int64) ([]*Session, error) {
	// 步骤1: 检查本地缓存
	if cached, ok := m.local.Load(uid); ok {
		sessions := cached.([]*Session)
		// 缓存有效条件：非空 且 缓存时间在 1 分钟内
		if len(sessions) > 0 && time.Since(sessions[0].cachedAt) < time.Minute {
			return sessions, nil
		}
	}

	// 步骤2: 从 Redis 获取该用户的所有 sid
	// GetUserSessions 返回 map[sid]string，其中 value 是 "device_id:platform"
	sidMap, err := m.dao.GetUserSessions(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("get user sessions: %w", err)
	}

	// 步骤3: 逐个查询 session 详情（Batch 模式，但这里是一次一次查）
	// 注意：这里存在一个经典的 N+1 问题 —— 先获取 sid 列表，再逐个查详情。
	// 对于单个用户通常只有 2-5 个设备，所以 N+1 影响很小。
	// 如果扩展到每个用户可能几十个 session，应该用 Pipeline 批量查询。
	var sessions []*Session
	for sid := range sidMap {
		sess, err := m.GetSession(ctx, sid)
		if err != nil || sess == nil {
			// 跳过已失效的 session（可能 TTL 刚好过期）
			continue
		}
		sessions = append(sessions, sess)
	}

	// 步骤4: 更新本地缓存
	// 所有 session 共用同一个 cachedAt（取当前时间）
	for _, s := range sessions {
		s.cachedAt = time.Now()
	}
	m.local.Store(uid, sessions)
	return sessions, nil
}

// GetSession 根据 SID 获取单个 session 的详细信息。
//
// Redis 中 session 以 Hash 结构存储（HSET）。
// key 格式: session:{sid}
// fields: uid, key, device_id, platform, server, created_at, last_hb
//
// 返回值 nil, nil 表示 session 不存在（已过期或已删除），
// 这是正常情况（如 Kick 时先 Get 再 Del，之间可能刚好 TTL 过期）。
func (m *SessionManager) GetSession(ctx context.Context, sid string) (*Session, error) {
	// 从 Redis 读取 session 的全部 field
	data, err := m.dao.GetSession(ctx, sid)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		// Redis 返回空 map → session 不存在或已过期
		return nil, nil
	}

	// 构建 Session 结构体，从 map[string]string 逐字段解析
	sess := &Session{
		SID:      sid,
		Key:      data["key"],
		DeviceID: data["device_id"],
		Platform: data["platform"],
		Server:   data["server"],
	}
	// uid 是 int64，Redis 中以字符串形式存储，需要用 fmt.Sscanf 转换
	if v, ok := data["uid"]; ok {
		fmt.Sscanf(v, "%d", &sess.UID)
	}
	// created_at 是毫秒级 Unix 时间戳字符串 → time.Time
	if v, ok := data["created_at"]; ok {
		var ts int64
		fmt.Sscanf(v, "%d", &ts)
		sess.CreatedAt = time.UnixMilli(ts)
	}
	// last_hb 同上，记录最后一次心跳时间
	if v, ok := data["last_hb"]; ok {
		var ts int64
		fmt.Sscanf(v, "%d", &ts)
		sess.LastHBAt = time.UnixMilli(ts)
	}
	return sess, nil
}

// Heartbeat 刷新 session 的心跳时间。
//
// 调用时机：Comet 检测到客户端心跳后，通过 RPC 调用 Logic.Heartbeat()，
// 最终到达此方法。
//
// 参数 key 是连接 key（不是 sid），因为 Comet 只知道 key 不知道 sid。
// 需要通过"key → sid"的反向索引先查出 sid。
//
// 流程：
//  1. key → sid（通过 Redis key_sid:{key} 反向索引）
//  2. 对 session:{sid} 和 user_sessions:{uid} 执行 EXPIRE 续期
//
// 为什么心跳要刷新 TTL？
// Session 在 Redis 中有 TTL（如 30 分钟）。如果客户端一直活跃但不发心跳，
// session 会在 30 分钟后被自动清理——此时 Comet 上连接还在，但 Logic 找不到
// session 了。心跳的作用就是告诉 Logic："这个连接还活着，别清我的 session"
func (m *SessionManager) Heartbeat(ctx context.Context, key string, uid int64) error {
	sid, err := m.dao.GetSessionByKey(ctx, key)
	if err != nil {
		return err
	}
	if sid == "" {
		// key 对应的 session 不存在，可能是已经过期被清理了
		return nil
	}
	return m.dao.ExpireSession(ctx, sid, uid)
}

// Kick 强制踢掉一个 session，并通知 Comet 关闭底层连接。
//
// 完整的踢人流程（三步）：
//  1. GetSession：从 Redis 获取 session 信息（需要 server + key 通知 Comet）
//  2. DelSession：从 Redis 删除 session 的所有关联 key
//  3. 通知 Comet：通过 gRPC 发送 OpKickConnection 给 Comet，关闭 TCP/WebSocket 连接
//
// 为什么需要先 Get 再 Del？
// 删除 session 后我们就丢失了 server 和 key 信息，无法通知 Comet。
// 所以必须先读出来（记录 server 和 key），再执行删除。
//
// 容错设计：
//   - Step1 GetSession 失败 → 忽略（key 为空，后续跳过 Comet 通知）
//   - Step2 DelSession 失败 → 返回错误（Redis 操作失败）
//   - Step3 Comet 通知失败 → 仅记录警告日志，不返回错误（因为 Redis 数据已清理完毕，
//     Comet 通知失败意味着连接可能还会存活一段时间，直到 Comet 端的心跳超时自动断开）
func (m *SessionManager) Kick(ctx context.Context, sid string, uid int64, deviceID string) error {
	// 步骤1: 在执行删除前获取 session 信息（需要 server + key 用于通知 Comet）
	sess, _ := m.GetSession(ctx, sid)

	key := ""
	if sess != nil {
		key = sess.Key
	}
	// 步骤2: 从 Redis 删除 session（包含 4 个 key 的原子删除）
	if err := m.dao.DelSession(ctx, sid, uid, deviceID, key); err != nil {
		return fmt.Errorf("del session: %w", err)
	}
	// 删除后立即清本地缓存
	m.local.Delete(uid)

	// 步骤3: 通知 Comet 关闭实际的 TCP/WebSocket 连接
	// 只有 kicker 已注入 且 拿到了有效的 server 和 key 才执行通知
	if m.kicker != nil && sess != nil && sess.Server != "" && sess.Key != "" {
		if err := m.kicker.KickConnection(ctx, sess.Server, sess.Key); err != nil {
			log.Warningf("kick comet connection failed: server=%s key=%s err=%v", sess.Server, sess.Key, err)
		}
	}
	return nil
}

// Disconnect 处理客户端主动断开连接。
//
// 与 Kick 的区别：
//   - Kick：服务端主动踢人（如同设备互踢）
//   - Disconnect：客户端主动断开（用户关闭 App、网络切换等）
//
// Disconnect 通过 key 和 server 定位 session：
//  1. key → sid（反向索引查询）
//  2. 获取 session 详情拿 deviceID
//  3. 调用 Kick 执行完整清理流程
//
// 为什么 Disconnect 最终调用 Kick 而不是自己实现清理？
// Kick 已经包含了完整的"删 Redis + 通知 Comet"逻辑，Disconnect 只是多了
// "通过 key 查找 sid"这一步。复用 Kick 避免了逻辑重复。
func (m *SessionManager) Disconnect(ctx context.Context, uid int64, key, server string) error {
	// step1: key → sid 反向查找
	sid, err := m.dao.GetSessionByKey(ctx, key)
	if err != nil {
		return err
	}
	if sid == "" {
		// session 不存在（可能已被清理），无需处理
		return nil
	}
	// step2: 获取 session 详情，取出 deviceID（Kick 需要 deviceID 来清理 device_session key）
	sess, _ := m.GetSession(ctx, sid)
	deviceID := ""
	if sess != nil {
		deviceID = sess.DeviceID
	}
	// step3: 复用 Kick 执行完整清理
	return m.Kick(ctx, sid, uid, deviceID)
}

// IsOnline 判断用户是否在线，并返回其在线 session 列表。
//
// 在线判定逻辑：Redis 中是否存在该用户的活跃 session。
// 由于心跳会定期刷新 TTL，如果用户掉线（心跳停止），session 会在 TTL 到期后
// 自动从 Redis 中消失，IsOnline 就会返回 false。
//
// 返回的 session 列表可用于：
//   - 获取用户的连接 key，用于消息推送
//   - 获取用户的 device/platform 信息，用于多端消息控制
func (m *SessionManager) IsOnline(ctx context.Context, uid int64) (bool, []*Session) {
	sessions, err := m.GetSessions(ctx, uid)
	if err != nil || len(sessions) == 0 {
		return false, nil
	}
	return true, sessions
}
