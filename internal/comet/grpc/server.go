// ============================================================================
// 文件：server.go
// 职责：Comet 的 gRPC 服务端 —— 接收 Logic 层的推送指令，路由到目标连接完成消息下发。
// ============================================================================
//
// 一、在整条链路中的位置
//
//   这是消息推送链路中"最后一公里"的入口。整条链路如下：
//
//   ┌──────────┐   gRPC call    ┌──────────────────┐   Channel.Push   ┌──────────────┐  WebSocket/TCP  ┌──────────┐
//   │  Logic   │ ─────────────→ │ Comet gRPC Server │ ──────────────→ │ Bucket/Channel │ ──────────────→│  Client  │
//   │          │                │   (本文件)         │                 │  (连接管理)      │                │ (用户设备) │
//   └──────────┘                └──────────────────┘                 └──────────────┘                 └──────────┘
//
//      调用方                          接收方                              数据结构                          最终目标
//   CometPusher                   server.PushMsg                  Server → Bucket → Channel          浏览器/App
//   (logic/comet.go)                                               (cityhash 分片路由)
//
//   具体来说：
//     Logic 的 CometPusher.PushMsg 发起 gRPC 调用
//       → 本文件的 server.PushMsg 接收请求
//         → Server.Bucket(key) 用 cityhash 定位到某个 Bucket（分片）
//           → Bucket.Channel(key) 在 Bucket 内查找对应的 Channel（客户端连接）
//             → channel.Push(proto) 将消息写入 Channel 的优先级队列
//               → channel.Signal() 唤醒分发协程
//                 → 分发协程从队列取出消息，通过 WebSocket/TCP 写入客户端
//
// 二、为什么 Comet 要暴露 gRPC 接口？
//
//   Comet 维护着所有客户端的长连接（WebSocket/TCP），但消息路由逻辑在 Logic 层。
//   Logic 需要通过某种方式告诉 Comet"把这条消息发给某某用户"。
//   gRPC 是高效的选择：二进制序列化、HTTP/2 多路复用、内置流控、长连接复用。
//   Logic 与 Comet 之间保持 gRPC 长连接，每次推送只需一次 RPC 调用。
//
// 三、数据结构层次
//
//   Server                          —— 顶层，持有多个 Bucket
//     └─ Bucket[0..N]               —— 分片，每个 Bucket 管理一部分连接（cityhash 路由）
//          ├─ chs: map[key]*Channel  —— 连接映射表，key → 客户端连接
//          ├─ rooms: map[roomID]*Room —— 房间映射表，roomID → 房间内所有 Channel
//          └─ ipCnts: map[ip]int32   —— IP 计数，用于连接数限制
//               └─ Channel           —— 单个客户端连接，包含读写缓冲区、优先级队列
//
// 四、各 RPC 方法概览
//
//   PushMsg        — 向指定 key 的客户端推送消息（点对点 / 少量推送）
//   Broadcast      — 向所有连接的客户端广播（全服广播，异步 goroutine）
//   BroadcastRoom  — 向指定房间的所有成员广播（房间消息）
//   Rooms          — 获取当前 Comet 实例上所有活跃的房间 ID 列表
//
// 五、关键设计细节
//
//   1. proto.Clone（Broadcast 方法）：全服广播使用 goroutine 异步执行，
//      goroutine 的生命周期可能超过 RPC handler 的返回时间。
//      gRPC 框架在 handler 返回后可能复用 request message，
//      因此必须深拷贝 proto，否则 goroutine 会读到被覆盖的数据。
//
//   2. Speed 限速（Broadcast 方法）：每处理完一个 Bucket，按配置计算等待时间：
//        t = channel_count / speed （秒）
//      避免瞬间全量推送把下游网络打满。
//
//   3. OpKickConnection（PushMsg 方法）：踢人指令不走正常消息推送流程，
//      直接调用 channel.Close() 关闭连接，效率最高。
//
//   4. NeedPush 检查（PushMsg 方法）：客户端可以声明自己关心的 op 类型（Watch），
//      不匹配的 op 直接跳过不推送，避免无效数据占用带宽。

package grpc

import (
	"context"
	"net"
	"time"

	pb "github.com/Terry-Mao/goim/api/comet"
	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/comet"
	"github.com/Terry-Mao/goim/internal/comet/conf"
	"github.com/Terry-Mao/goim/internal/comet/errors"
	"github.com/Terry-Mao/goim/internal/grpcx"
	log "github.com/Terry-Mao/goim/pkg/log"
	"google.golang.org/protobuf/proto"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// New 创建并启动 Comet gRPC 服务器。
//
// 参数：
//
//	c — gRPC 服务配置（监听地址、keepalive 参数等）
//	s — Comet 核心 Server 实例，持有所有 Bucket 和连接
//
// 服务端 Keepalive 参数说明：
//
//	MaxConnectionIdle     — 空闲连接最大存活时间，超时无 RPC 则关闭
//	MaxConnectionAge      — 连接最大生命周期，到期强制关闭（强制轮换）
//	MaxConnectionAgeGrace  — 连接到期后的宽限期，给正在执行的 RPC 一个完成时间
//	Time                  — 空闲时发送 keepalive ping 的间隔
//	Timeout               — keepalive ping 等待 ack 的超时时间
//
// 额外配置：
//
//	otelgrpc.NewServerHandler()  — OpenTelemetry 集成，自动生成 gRPC 调用的 trace span
//	UnaryInterceptorChain()      — 一元 RPC 拦截器链（recovery、日志、trace 等）
//	StreamInterceptorChain()     — 流式 RPC 拦截器链
func New(c *conf.RPCServer, s *comet.Server) *grpc.Server {
	keepParams := grpc.KeepaliveParams(keepalive.ServerParameters{
		MaxConnectionIdle:     time.Duration(c.IdleTimeout),
		MaxConnectionAgeGrace: time.Duration(c.ForceCloseWait),
		Time:                  time.Duration(c.KeepAliveInterval),
		Timeout:               time.Duration(c.KeepAliveTimeout),
		MaxConnectionAge:      time.Duration(c.MaxLifeTime),
	})
	srv := grpc.NewServer(keepParams,
		grpc.StatsHandler(otelgrpc.NewServerHandler()),         // 分布式链路追踪
		grpc.UnaryInterceptor(grpcx.UnaryInterceptorChain()),   // 一元 RPC 拦截器
		grpc.StreamInterceptor(grpcx.StreamInterceptorChain()), // 流式 RPC 拦截器
	)
	pb.RegisterCometServer(srv, &server{srv: s})

	// 启动 TCP 监听
	lis, err := net.Listen(c.Network, c.Addr)
	if err != nil {
		log.Fatalf("comet grpc net.Listen(%s, %s) error(%v)", c.Network, c.Addr, err)
	}
	// 异步 serve，不阻塞调用方（通常在主 goroutine 启动后还要做其他初始化）
	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("comet grpc srv.Serve error(%v)", err)
		}
	}()
	return srv
}

// server 是 pb.CometServer 的具体实现。
// 嵌入 UnimplementedCometServer 保证向前兼容（proto 新增方法时编译不会报错）。
type server struct {
	pb.UnimplementedCometServer
	srv *comet.Server // Comet 核心实例，持有 Bucket → Channel 的完整数据
}

// 编译期检查：确保 server 实现了 pb.CometServer 接口
var _ pb.CometServer = &server{}

// PushMsg 向指定 key 的客户端推送消息（点对点推送）。
//
// 这是 Comet 端最核心、调用频率最高的 RPC 方法。
// Logic 层确定消息的目标用户后，通过此方法把消息投递到 Comet，
// Comet 再将消息写入客户端的长连接。
//
// 处理流程（单条消息的完整旅途）：
//
//  1. 参数校验 — 检查 keys 和 proto 非空。
//
//  2. 遍历 keys — 一个请求可以包含多个 key（批量推送）。
//
//  3. Server.Bucket(key) — 用 cityhash 对 key 取哈希，定位到某个 Bucket。
//     所有 Comet 共享相同的 cityhash 算法，保证同一个 key 始终路由到同一个 Bucket。
//
//  4. Bucket.Channel(key) — 在 Bucket 内部查找该 key 对应的 Channel。
//     Channel 代表一个客户端的 WebSocket/TCP 长连接。
//     如果 Channel 不存在（nil），说明该客户端已断开，跳过本条。
//
//  5. 特殊处理：OpKickConnection
//     如果是踢人指令，不走消息队列，直接调用 channel.Close() 关闭连接。
//
//  6. NeedPush 检查 — 客户端通过 Watch(op) 声明自己关心的操作类型。
//     例如，客户端 A 订阅了"私聊消息"op，那么"群聊消息"op 就不会推给它。
//     这是一种客户端级别的消息过滤，节省带宽和 CPU。
//
//  7. channel.Push(proto) — 将协议帧写入 Channel 的优先级队列。
//     注意：这里只是"入队"，不是"写入网络"。
//
//  8. channel.Signal() — 发送 ProtoReady 信号，唤醒分发协程（dispatch goroutine）。
//     分发协程被唤醒后从队列 Pop 消息，编码为 WebSocket/TCP 帧写入网络。
//
// 为什么 Push + Signal 要分两步？
//
//	Channel 内部有一个优先级队列（高优先级 + 普通优先级）和一个分发协程。
//	Push 只负责入队，Signal 负责通知分发协程"有数据了，来取"。
//	分离入队和通知可以支持批量写入——分发协程一次 Pop 可以取多条消息合并写入。
func (s *server) PushMsg(ctx context.Context, req *pb.PushMsgReq) (reply *pb.PushMsgReply, err error) {
	if len(req.Keys) == 0 || req.Proto == nil {
		return nil, errors.ErrPushMsgArg
	}
	log.Infof("PushMsg: keys=%v protoOp=%d bodyLen=%d", req.Keys, req.ProtoOp, len(req.Proto.Body))

	for _, key := range req.Keys {
		// 步骤 1：cityhash 分片路由，定位 Bucket
		bucket := s.srv.Bucket(key)
		if bucket == nil {
			log.Warningf("PushMsg: bucket nil for key=%s", key)
			continue
		}

		// 步骤 2：在 Bucket 内查找 Channel
		if channel := bucket.Channel(key); channel != nil {
			// 步骤 3：特殊处理——踢人指令直接关闭连接
			if req.ProtoOp == protocol.OpKickConnection {
				channel.Close()
				continue
			}

			// 步骤 4：客户端过滤——检查该连接是否接受此类 op
			if !channel.NeedPush(req.ProtoOp) {
				log.Warningf("PushMsg: NeedPush=false key=%s op=%d (client did not accept this op)", key, req.ProtoOp)
				continue
			}

			// 步骤 5：消息入队
			if err = channel.Push(req.Proto); err != nil {
				log.Warningf("PushMsg: Push failed key=%s err=%v", key, err)
				return
			}

			// 步骤 6：唤醒分发协程，将消息写入网络
			channel.Signal()
			log.Infof("PushMsg: delivered key=%s op=%d", key, req.ProtoOp)
		} else {
			log.Warningf("PushMsg: channel nil for key=%s", key)
		}
	}
	return &pb.PushMsgReply{}, nil
}

// Broadcast 向所有在线用户广播消息（全服广播）。
//
// 与 PushMsg 的点对点推送不同，Broadcast 面向所有连接，不分 key。
// 典型场景：全服公告、服务器维护通知。
//
// 异步执行：
//
//	广播涉及大量连接，耗时可能较长，因此在 goroutine 中异步执行，
//	RPC 方法立即返回，不阻塞调用方。
//
// 关键：proto.Clone
//
//	goroutine 会在 RPC handler 返回后继续执行，但 gRPC 框架会在 handler
//	返回后复用 request message 对象以节省内存。如果不做深拷贝，
//	goroutine 读到的 proto 可能已被后续请求覆盖 → 导致数据错乱。
//	因此必须先 proto.Clone(req.Proto) 再传入 goroutine。
//
// Speed 限速机制：
//
//	每处理完一个 Bucket，按公式等待一段时间：
//	  wait_seconds = channel_count_in_bucket / speed
//	例如 speed=100，某 Bucket 有 500 个连接，则该 Bucket 处理后等待 5 秒。
//	作用：避免瞬间向所有连接写入数据导致网络拥塞或 CPU 尖峰。
//	同时监听 Server 的 context，服务关闭时可提前退出。
func (s *server) Broadcast(ctx context.Context, req *pb.BroadcastReq) (*pb.BroadcastReply, error) {
	if req.Proto == nil {
		return nil, errors.ErrBroadCastArg
	}

	// 深拷贝：goroutine 生命周期超过 RPC handler，必须拷贝
	p := proto.Clone(req.Proto).(*protocol.Proto)
	op := req.ProtoOp
	speed := req.Speed

	// TODO: 后续可改为专用的广播队列，而非直接启 goroutine
	go func() {
		srvCtx := s.srv.Context() // Server 的 context，服务关闭时 Done
		for _, bucket := range s.srv.Buckets() {
			bucket.Broadcast(p, op) // 向该 Bucket 内所有 Channel 广播

			// 速率控制
			if speed > 0 {
				count := bucket.ChannelCount()
				if count > 0 {
					t := count / int(speed) // 等待秒数
					if t > 0 {
						select {
						case <-time.After(time.Duration(t) * time.Second):
						case <-srvCtx.Done():
							return // 服务关闭，立即退出
						}
					}
				}
			}
		}
	}()
	return &pb.BroadcastReply{}, nil
}

// BroadcastRoom 向指定房间的所有成员广播消息（房间广播）。
//
// 与 Broadcast（全服）不同，BroadcastRoom 只推送给房间内的连接。
// 逻辑：遍历所有 Bucket，每个 Bucket 内部查找该房间的成员并推送。
//
// 为什么是同步的？
//
//	与 Broadcast 不同，房间广播通常涉及的用户数有限（几十到几千），
//	同步执行即可，不需要启 goroutine 异步。
func (s *server) BroadcastRoom(ctx context.Context, req *pb.BroadcastRoomReq) (*pb.BroadcastRoomReply, error) {
	if req.Proto == nil || req.RoomID == "" {
		return nil, errors.ErrBroadCastRoomArg
	}
	for _, bucket := range s.srv.Buckets() {
		bucket.BroadcastRoom(req)
	}
	return &pb.BroadcastRoomReply{}, nil
}

// Rooms 获取当前 Comet 实例上所有活跃的房间 ID 列表。
//
// 用途：供运维/管理后台查询"当前有哪些房间在使用中"。
// 遍历所有 Bucket 的 rooms map，去重后返回。
func (s *server) Rooms(ctx context.Context, req *pb.RoomsReq) (*pb.RoomsReply, error) {
	var (
		roomIds = make(map[string]bool)
	)
	for _, bucket := range s.srv.Buckets() {
		for roomID := range bucket.Rooms() {
			roomIds[roomID] = true
		}
	}
	return &pb.RoomsReply{Rooms: roomIds}, nil
}
