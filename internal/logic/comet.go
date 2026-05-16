// ============================================================================
// 文件：comet.go
// 职责：Comet 服务连接池 —— 管理与所有 Comet 实例的 gRPC 长连接，提供消息推送能力。
// ============================================================================
//
// 一、什么是 Comet？
//
//   Comet 是接入层服务，负责维护与客户端的 WebSocket/TCP 长连接。
//   Logic 层（本模块）作为消息路由层，通过 gRPC 调用 Comet 来完成实际的
//   消息下发、房间广播、全服广播和踢人操作。
//
// 二、架构定位
//
//   ┌──────────┐    gRPC     ┌──────────┐   WebSocket/TCP   ┌──────────┐
//   │  Logic   │ ──────────→ │  Comet   │ ←──────────────→ │  Client  │
//   │ (本文件)  │             │ (多实例)  │                   │ (用户设备) │
//   └──────────┘             └──────────┘                   └──────────┘
//
//   Logic 不直接面对客户端，而是通过 Comet 间接推送。一个 Logic 实例连接所有
//   Comet 实例，维护一张 hostname → gRPC client 的映射表。
//
// 三、连接生命周期
//
//   NewCometPusher()        → 创建空的连接池
//   UpdateNodes(nodes)      → 根据服务发现刷新连接（新增/移除 Comet 实例）
//   PushMsg / BroadcastRoom  → 使用连接发送消息
//   Close()                 → 关闭所有连接，清理资源
//
// 四、Copy-on-Write 模式（UpdateNodes）
//
//   连接变更不频繁（仅在 Comet 扩缩容时触发），但消息推送极其频繁。
//   因此 UpdateNodes 采用"写时复制"策略：
//     1. 构建全新的 clients + conns map（不阻塞读）。
//     2. 为新增节点建立 gRPC 连接（耗时操作在锁外完成）。
//     3. 原子替换 map 指针（持写锁时间极短）。
//     4. 关闭旧连接（在锁外执行，避免阻塞）。
//   这样 PushMsg 始终持有读锁，不会被连接变更阻塞。
//
// 五、协议编码说明
//
//   PushMsg / BroadcastRoom / BroadcastAll 在发送前都会做同样的操作：
//   先将原始 body 用 protocol.Proto 包装并序列化（header + data），
//   然后将序列化后的完整帧作为 OpRaw 的 body 发送。
//   这是为了匹配 Comet 端的处理逻辑：Comet.WriteWebsocket 期望 OpRaw
//   的 body 是一个完整的序列化帧，它会从内层 header 中提取真实的 op 和 body
//   再发送给客户端。

package logic

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/api/comet"
	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/pkg/bytes"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/bilibili/discovery/naming"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// CometPusher 管理到所有 Comet 实例的 gRPC 连接。
//
// 内部维护两张 map：
//
//	clients — hostname → gRPC 客户端桩（用于调用 RPC 方法）
//	conns   — hostname → gRPC 底层连接（用于资源回收和连接管理）
//
// 并发安全：读写锁保护。PushMsg 等高频操作持读锁，UpdateNodes 持写锁。
type CometPusher struct {
	mu      sync.RWMutex
	clients map[string]comet.CometClient // Comet 实例主机名 → gRPC 客户端代理
	conns   map[string]*grpc.ClientConn  // Comet 实例主机名 → gRPC 底层连接
}

// NewCometPusher 创建空的 CometPusher。
// 初始时没有任何连接，需要通过 UpdateNodes 注入 Comet 实例列表。
func NewCometPusher() *CometPusher {
	return &CometPusher{
		clients: make(map[string]comet.CometClient),
		conns:   make(map[string]*grpc.ClientConn),
	}
}

// PushMsg 向指定 Comet 实例上的指定连接（keys）推送消息。
//
// 协议编码流程：
//  1. 用原始 op + body 构造 Proto，序列化为完整帧（header + body）。
//  2. 将序列化后数据作为新的 body，op 改为 OpRaw。
//  3. Comet 收到 OpRaw 后，解析内层 Proto header，提取真实 op 和 body 发给客户端。
//     这样做的目的是复用 Comet 已有的 OpRaw 处理路径，与 Job/Worker 行为一致。
func (p *CometPusher) PushMsg(ctx context.Context, server string, keys []string, op int32, body []byte) error {
	p.mu.RLock()
	client, ok := p.clients[server]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("comet server %s not found", server)
	}

	// 预编码：将 body 包装为完整的协议帧
	buf := bytes.NewWriterSize(len(body) + 64) // 64 字节留给 header，避免扩容
	pb := &protocol.Proto{
		Ver:  1,
		Op:   op,
		Body: body,
	}
	pb.WriteTo(buf)        // 序列化 header + body
	pb.Body = buf.Buffer() // 用序列化后的完整帧替换 body
	pb.Op = protocol.OpRaw // op 改为 OpRaw，Comet 端会解析内层 header

	_, err := client.PushMsg(ctx, &comet.PushMsgReq{
		Keys:    keys,
		ProtoOp: op, // 原始 op 透传，Comet 可用于日志/统计
		Proto:   pb,
	})
	return err
}

// UpdateNodes 根据服务发现的结果刷新 Comet 连接池。
//
// 采用"写时复制"策略，最小化锁持有时间：
//  1. 构建全新的 clients + conns map。
//  2. 复用已有连接（节点未变 → 保留原连接，无需重新拨号）。
//  3. 为新节点建立 gRPC 连接（拨号在锁外完成，耗时不阻塞推送）。
//  4. 原子替换 map 指针（写锁持有时间仅一次 map 赋值）。
//  5. 关闭已移除节点的旧连接（在锁外执行）。
//
// 参数：
//
//	nodes — 来自服务发现（如 Bilibili Discovery）的 Comet 实例列表。
//	        每个实例包含 Hostname（服务名+端口）和 Addrs（各类地址）。
func (p *CometPusher) UpdateNodes(nodes []*naming.Instance) {
	if len(nodes) == 0 {
		log.Errorf("comet pusher: UpdateNodes called with 0 nodes")
		return
	}

	// 构建新 map，预分配容量以减少扩容
	newClients := make(map[string]comet.CometClient, len(nodes))
	newConns := make(map[string]*grpc.ClientConn, len(nodes))

	// 第一阶段：复用仍在列表中的已有连接（持读锁，不阻塞推送）
	p.mu.RLock()
	for _, node := range nodes {
		if client, ok := p.clients[node.Hostname]; ok {
			newClients[node.Hostname] = client
			newConns[node.Hostname] = p.conns[node.Hostname]
		}
	}
	p.mu.RUnlock()

	// 第二阶段：为新节点建立连接（无锁，耗时操作不阻塞业务）
	for _, node := range nodes {
		if _, ok := newClients[node.Hostname]; ok {
			continue // 已有连接，跳过
		}
		grpcAddr := grpcAddress(node)
		if grpcAddr == "" {
			log.Errorf("comet node %s has no grpc address: %v", node.Hostname, node.Addrs)
			continue
		}
		conn, client, err := dialCometClient(grpcAddr)
		if err != nil {
			log.Errorf("dial comet %s(%s) error(%v)", node.Hostname, grpcAddr, err)
			continue // 单个节点连接失败不影响整体
		}
		newClients[node.Hostname] = client
		newConns[node.Hostname] = conn
		log.Infof("comet pusher: connected to %s (%s)", node.Hostname, grpcAddr)
	}

	// 第三阶段：原子替换（持写锁，极短时间）
	p.mu.Lock()
	oldConns := p.conns
	p.clients = newClients
	p.conns = newConns
	p.mu.Unlock()

	// 第四阶段：关闭已移除的旧连接（无锁，不阻塞业务）
	for hostname, conn := range oldConns {
		if _, ok := newClients[hostname]; !ok {
			conn.Close()
			log.Infof("comet pusher: disconnected from %s", hostname)
		}
	}
}

// KickConnection 踢出指定连接。
//
// 向 Comet 发送 OpKickConnection 指令，Comet 收到后关闭该客户端的长连接。
// 典型场景：用户被封禁、强制下线、多端登录踢旧设备。
func (p *CometPusher) KickConnection(ctx context.Context, server, key string) error {
	p.mu.RLock()
	client, ok := p.clients[server]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("comet server %s not found", server)
	}
	_, err := client.PushMsg(ctx, &comet.PushMsgReq{
		Keys:    []string{key},
		ProtoOp: protocol.OpKickConnection,
		Proto: &protocol.Proto{
			Ver: 1,
			Op:  protocol.OpKickConnection,
		},
	})
	return err
}

// BroadcastRoom 向指定房间广播消息（跨所有 Comet 实例）。
//
// 房间内的用户可能分布在不同的 Comet 实例上，因此需要对所有 Comet 逐一广播。
// 每个 Comet 收到请求后，查找自己持有的连接中哪些用户在目标房间，然后投递。
//
// 失败策略：只要有一个 Comet 成功就算整体成功。
// 这是因为服务发现可能包含已下线但尚未摘除的 Comet 实例，
// 个别失败属正常现象，不应阻塞整体流程。
func (p *CometPusher) BroadcastRoom(ctx context.Context, op int32, roomKey string, body []byte) error {
	// 协议编码（与 PushMsg 相同的处理方式）
	buf := bytes.NewWriterSize(len(body) + 64)
	pb := &protocol.Proto{
		Ver:  1,
		Op:   op,
		Body: body,
	}
	pb.WriteTo(buf)
	pb.Body = buf.Buffer()
	pb.Op = protocol.OpRaw

	p.mu.RLock()
	defer p.mu.RUnlock()

	var lastErr error
	okCount := 0
	for server, client := range p.clients {
		if _, err := client.BroadcastRoom(ctx, &comet.BroadcastRoomReq{
			RoomID: roomKey,
			Proto:  pb,
		}); err != nil {
			lastErr = err
			log.Warningf("broadcast room to %s failed: %v", server, err)
		} else {
			okCount++
		}
	}
	// 全部失败才返回错误
	if okCount == 0 && lastErr != nil {
		return fmt.Errorf("all comet broadcast room failed: %w", lastErr)
	}
	return nil
}

// BroadcastAll 向所有 Comet 实例的所有在线用户广播消息（全服广播）。
//
// speed 参数控制广播速率（消息/秒），会按 Comet 实例数量均分：
//
//	speed_per_comet = speed / comet_count
//
// 例如 speed=100 且有 4 个 Comet，则每个 Comet 以 25 msg/s 的速率广播。
// 这是为了避免瞬间全量推送打垮下游。
func (p *CometPusher) BroadcastAll(ctx context.Context, op, speed int32, body []byte) error {
	// 协议编码（与 PushMsg 相同的处理方式）
	buf := bytes.NewWriterSize(len(body) + 64)
	pb := &protocol.Proto{
		Ver:  1,
		Op:   op,
		Body: body,
	}
	pb.WriteTo(buf)
	pb.Body = buf.Buffer()
	pb.Op = protocol.OpRaw

	// 按 Comet 实例数均分速率
	p.mu.RLock()
	n := len(p.clients)
	p.mu.RUnlock()
	if n > 0 {
		speed /= int32(n)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	var lastErr error
	okCount := 0
	for server, client := range p.clients {
		if _, err := client.Broadcast(ctx, &comet.BroadcastReq{
			ProtoOp: op,
			Proto:   pb,
			Speed:   speed,
		}); err != nil {
			lastErr = err
			log.Warningf("broadcast to %s failed: %v", server, err)
		} else {
			okCount++
		}
	}
	if okCount == 0 && lastErr != nil {
		return fmt.Errorf("all comet broadcast failed: %w", lastErr)
	}
	return nil
}

// Close 关闭所有 gRPC 连接，释放资源。
//
// 通常在服务优雅关闭时调用。
// 关闭后 CometPusher 不可再用，除非重新调用 UpdateNodes。
func (p *CometPusher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for hostname, conn := range p.conns {
		conn.Close()
		delete(p.clients, hostname) // 同步清理 client 引用
	}
	p.conns = make(map[string]*grpc.ClientConn)
}

// grpcAddress 从服务发现返回的实例地址列表中提取 gRPC 地址。
//
// 服务发现可能为一个实例注册多种协议的地址（http、grpc 等）。
// 此函数遍历 Addrs 列表，找到 scheme="grpc" 的地址并返回其 Host 部分。
//
// 示例：
//
//	Addrs = ["http://10.0.0.1:8080", "grpc://10.0.0.1:9000"]
//	返回 "10.0.0.1:9000"
func grpcAddress(in *naming.Instance) string {
	for _, addr := range in.Addrs {
		u, err := url.Parse(addr)
		if err == nil && u.Scheme == "grpc" {
			return u.Host
		}
	}
	return ""
}

// dialCometClient 建立到 Comet 实例的 gRPC 连接。
//
// 连接参数说明：
//
//	WithInitialWindowSize(16MB)      — HTTP/2 流控窗口大小，大窗口支持高吞吐推送
//	WithInitialConnWindowSize(16MB)  — 连接级流控窗口
//	MaxCallRecvMsgSize(16MB)         — 单条消息最大接收尺寸
//	MaxCallSendMsgSize(16MB)         — 单条消息最大发送尺寸
//	WithBackoffMaxDelay(3s)          — 重连退避最大延迟（连接断开后的重试间隔上限）
//
// Keepalive 参数说明：
//
//	Time:    10s — 每 10 秒发送一次 keepalive ping
//	Timeout: 3s  — ping 超时 3 秒未收到 ack 则认为连接已死
//	PermitWithoutStream: true — 即使没有活跃的 RPC 流也发送 keepalive，
//	                           确保空闲连接也能被及时检测到异常
//
// 注意：使用 insecure 传输（非 TLS），适用于内网环境。
func dialCometClient(addr string) (*grpc.ClientConn, comet.CometClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),    // 内网明文传输
		grpc.WithInitialWindowSize(1<<24),                           // 16MB 流控窗口
		grpc.WithInitialConnWindowSize(1<<24),                       // 16MB 连接窗口
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1<<24)), // 16MB 接收上限
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(1<<24)), // 16MB 发送上限
		grpc.WithBackoffMaxDelay(3*time.Second),                     // 重连退避上限
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second, // ping 间隔
			Timeout:             3 * time.Second,  // ping 超时
			PermitWithoutStream: true,             // 空闲时也发 ping
		}),
	)
	if err != nil {
		return nil, nil, err
	}
	return conn, comet.NewCometClient(conn), nil
}
