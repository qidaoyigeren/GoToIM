// server_tcp.go 实现了 Comet 服务的 TCP 长连接层。
//
// 整体架构：
//
//	InitTCP        —— 监听多个 TCP 地址，为每个地址启动 N 个 accept goroutine
//	acceptTCP      —— 在 accept 循环中接收新连接，配置 socket 参数后派发给 serveTCP
//	serveTCP       —— 每个连接一个 goroutine，负责：握手认证 → 循环读取客户端消息 → 交给 dispatch 协程写回
//	dispatchTCP    —— 每个连接一个写协程，从 Channel 的就绪队列中取出协议帧，写入 TCP 连接
//	authTCP        —— 完成握手阶段的协议读写，调用 Server.Connect 注册会话
//
// 数据流：
//
//	客户端 ──TCP──▶ acceptTCP ──▶ serveTCP（读协程）
//	                                 │  读到的 Proto 放入 ch.CliProto 环形缓冲区
//	                                 │  调用 ch.Signal() 通知 dispatch 协程
//	                                 ▼
//	                           dispatchTCP（写协程）
//	                                 │  从 ch.Ready() 获取待发送的 Proto
//	                                 │  写入 TCP 连接
//	                                 ▼
//	                           客户端 ◀──TCP──
//
// 服务端推送路径：
//
//	Logic/Kafka ──▶ Server.Push() ──▶ Channel.Push() ──▶ ch.Signal()
//	                                                       ──▶ dispatchTCP 感知后写给客户端
package comet

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/comet/conf"
	"github.com/Terry-Mao/goim/pkg/bufio"
	"github.com/Terry-Mao/goim/pkg/bytes"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/metrics"
	"github.com/Terry-Mao/goim/pkg/ratelimit"
	xtime "github.com/Terry-Mao/goim/pkg/time"
)

// isClosedError 判断错误是否为"连接已关闭"。
// 在连接断开后的清理阶段，对端可能已经 close，此时读写会返回 net.ErrClosed，
// 这类错误属于正常的连接生命周期结束，不应记为异常日志。
func isClosedError(err error) bool {
	return errors.Is(err, net.ErrClosed)
}

const (
	// maxInt 是连接计数器 r 的上限。r 在 acceptTCP 中单调递增用于 round-robin 分配
	// Timer/Reader/Writer 池，到达 maxInt 后归零避免溢出。
	maxInt = 1<<31 - 1
)

// InitTCP 初始化 TCP 服务。
// 遍历配置中的所有绑定地址（addrs），对每个地址创建 TCP 监听器，
// 然后启动 accept 个 acceptTCP goroutine 并行 accept 连接。
//
// 参数说明：
//
//	server —— 全局 Server 实例，持有配置、连接桶、路由等共享资源
//	addrs  —— TCP 监听地址列表，如 [":3101", ":3102"]，支持多网卡多端口
//	accept —— 每个监听地址启动的 accept goroutine 数量，
//	          多个 accept 可以减少在高并发场景下 accept 调用的排队延迟
func InitTCP(server *Server, addrs []string, accept int) (err error) {
	var (
		bind     string
		listener *net.TCPListener
		addr     *net.TCPAddr
	)
	for _, bind = range addrs {
		if addr, err = net.ResolveTCPAddr("tcp", bind); err != nil {
			log.Errorf("net.ResolveTCPAddr(tcp, %s) error(%v)", bind, err)
			return
		}
		if listener, err = net.ListenTCP("tcp", addr); err != nil {
			log.Errorf("net.ListenTCP(tcp, %s) error(%v)", bind, err)
			return
		}
		log.Infof("start tcp listen: %s", bind)
		// split N core accept
		for i := 0; i < accept; i++ {
			go acceptTCP(server, listener)
		}
	}
	return
}

// acceptTCP 是单个 accept goroutine 的主循环。
// 持续从 TCP 监听器上 accept 新连接，配置 TCP keepalive 和读写缓冲区大小后，
// 将连接交给 serveTCP 处理。
//
// 参数 r 用于 round-robin 地将连接分配到不同的 Timer/Reader/Writer 池，
// 使得不同连接的定时器和缓冲区对象分散在多个池中，降低单个池的竞争。
func acceptTCP(server *Server, lis *net.TCPListener) {
	var (
		conn *net.TCPConn
		err  error
		r    int
	)
	for {
		if conn, err = lis.AcceptTCP(); err != nil {
			// if listener close then return
			log.Errorf("listener.Accept(\"%s\") error(%v)", lis.Addr().String(), err)
			return
		}
		// TCP KeepAlive：探测对端是否存活，避免半开连接占用资源
		if err = conn.SetKeepAlive(server.c.TCP.KeepAlive); err != nil {
			log.Errorf("conn.SetKeepAlive() error(%v)", err)
			conn.Close()
			continue
		}
		// 设置内核接收缓冲区大小（SO_RCVBUF），影响 TCP 窗口和吞吐
		if err = conn.SetReadBuffer(server.c.TCP.Rcvbuf); err != nil {
			log.Errorf("conn.SetReadBuffer() error(%v)", err)
			conn.Close()
			continue
		}
		// 设置内核发送缓冲区大小（SO_SNDBUF），影响写入吞吐
		if err = conn.SetWriteBuffer(server.c.TCP.Sndbuf); err != nil {
			log.Errorf("conn.SetWriteBuffer() error(%v)", err)
			conn.Close()
			continue
		}
		// 为每个连接启动独立的 serveTCP goroutine
		// r 用于 round-robin 分配资源池
		go serveTCP(server, conn, r)
		if r++; r == maxInt {
			r = 0
		}
	}
}

// serveTCP 是每个 TCP 连接的入口 goroutine。
// 根据 round-robin 索引 r 从 Round（资源池轮）中获取对应的：
//
//	tr —— 定时器池（用于握手超时、心跳超时检测）
//	rp —— 读缓冲区对象池（减少频繁的 []byte 分配）
//	wp —— 写缓冲区对象池
//
// 然后将连接移交给 ServeTCP 执行完整的连接生命周期。
func serveTCP(s *Server, conn *net.TCPConn, r int) {
	var (
		// timer
		tr = s.round.Timer(r)
		rp = s.round.Reader(r)
		wp = s.round.Writer(r)
		// ip addr
		lAddr = conn.LocalAddr().String()
		rAddr = conn.RemoteAddr().String()
	)
	if conf.Conf.Debug {
		log.Infof("start tcp serve \"%s\" with \"%s\"", lAddr, rAddr)
	}
	s.ServeTCP(conn, rp, wp, tr)
}

// ServeTCP 是单个 TCP 连接的完整生命周期管理函数。
//
// 生命周期分为三个阶段：
//  1. 握手认证（Handshake）—— 读取客户端的 OpAuth 请求，调用 authTCP 完成认证，
//     获取用户 ID (mid)、连接唯一标识 (key)、房间 ID (rid)、订阅事件列表 (accepts)、
//     心跳间隔 (hb)。认证成功后将 Channel 注册到对应的连接桶 (Bucket)。
//  2. 消息循环（Message Loop）—— 持续从 TCP 连接读取客户端协议帧，
//     心跳帧就地回复；业务帧交给 Server.Operate 处理（如加入房间、同步消息等）。
//     读到的帧放入 Channel 的 CliProto 环形缓冲区，然后调用 ch.Signal() 唤醒 dispatch 写协程。
//  3. 清理退出（Cleanup）—— 连接异常或客户端断开后，从 Bucket 中摘除 Channel，
//     释放缓冲区对象回对象池，关闭 TCP 连接，并通知 Logic 层 Disconnect。
//
// 参数说明：
//
//	conn —— 原始 TCP 连接
//	rp/wp —— 读/写缓冲区对象池（Round 中按 r 索引的池）
//	tr  —— 定时器池，用于握手超时和心跳超时的管理
func (s *Server) ServeTCP(conn *net.TCPConn, rp, wp *bytes.Pool, tr *xtime.Timer) {
	var (
		err     error
		rid     string
		accepts []int32
		hb      time.Duration
		white   bool
		p       *protocol.Proto
		b       *Bucket
		trd     *xtime.TimerData
		lastHb  = time.Now()
		rb      = rp.Get()                                                 // 从读对象池获取一个 []byte 缓冲区
		wb      = wp.Get()                                                 // 从写对象池获取一个 []byte 缓冲区
		ch      = NewChannel(s.c.Protocol.CliProto, s.c.Protocol.SvrProto) // 创建 Channel，分配客户端/服务端协议环形缓冲区
		rr      = &ch.Reader                                               // Channel 的带缓冲 Reader，包装了 TCP 连接
		wr      = &ch.Writer                                               // Channel 的带缓冲 Writer，包装了 TCP 连接
	)
	// 初始化令牌桶限流器：RateLimit 是每秒允许的请求数，RateBurst 是突发容量
	if s.c.Protocol.RateLimit > 0 {
		burst := s.c.Protocol.RateBurst
		if burst <= 0 {
			burst = 10
		}
		ch.SetRateLimiter(ratelimit.NewTokenBucket(s.c.Protocol.RateLimit, burst))
	}
	// 将对象池中的缓冲区绑定到 Channel 的 Reader/Writer，
	// 实现零拷贝的带缓冲读写（ReadTCP/WriteTCP 直接操作这块内存）
	ch.Reader.ResetBuffer(conn, rb.Bytes())
	ch.Writer.ResetBuffer(conn, wb.Bytes())
	// 创建可取消的 context，用于在连接断开时通知所有关联的异步操作（如 Operate 中的 RPC 调用）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ========== 阶段一：握手认证 ==========
	// step 变量用于超时回调中定位握手进行到哪一步，便于排查问题
	step := 0
	// 注册握手超时定时器：如果在 HandshakeTimeout 内未完成认证，强制关闭连接
	trd = tr.Add(time.Duration(s.c.Protocol.HandshakeTimeout), func() {
		conn.Close()
		log.Errorf("key: %s remoteIP: %s step: %d tcp handshake timeout", ch.Key, conn.RemoteAddr().String(), step)
	})
	// 提取客户端 IP 地址（去掉端口号），用于后续白名单判断和日志记录
	ch.IP, _, _ = net.SplitHostPort(conn.RemoteAddr().String())
	// must not setadv, only used in auth
	// 从客户端协议环形缓冲区中申请一个空槽位，用于存放认证请求帧
	step = 1
	if p, err = ch.CliProto.Set(); err == nil {
		// authTCP 读取客户端发送的 OpAuth 请求，调用 Server.Connect 向 Logic 层注册会话
		// 返回：mid=用户ID, key=连接唯一标识, rid=房间ID, accepts=订阅的事件类型, hb=心跳间隔
		if ch.Mid, ch.Key, rid, accepts, hb, err = s.authTCP(ctx, rr, wr, p); err == nil {
			// 订阅客户端感兴趣的事件类型（如弹幕、礼物等），用于后续消息过滤
			ch.Watch(accepts...)
			// 根据 key 的一致性哈希值选择一个连接桶，将 Channel 注册进去
			// Bucket 是 Comet 层管理连接的核心数据结构，支持按 key 快速查找
			b = s.Bucket(ch.Key)
			err = b.Put(rid, ch)
			if conf.Conf.Debug {
				log.Infof("tcp connnected key:%s mid:%d proto:%+v", ch.Key, ch.Mid, p)
			}
			metrics.ConnectionsActive.Inc()
		}
	}
	// ========== 阶段一（续）：握手结果处理 ==========
	step = 2
	if err != nil {
		// 握手失败：释放所有资源并关闭连接
		conn.Close()
		rp.Put(rb) // 缓冲区归还对象池
		wp.Put(wb)
		tr.Del(trd) // 移除超时定时器
		log.Errorf("key: %s handshake failed error(%v)", ch.Key, err)
		return
	}
	// 握手成功：将定时器的 key 绑定到 Channel 的唯一标识，后续通过 key 管理定时器
	trd.Key = ch.Key
	// 将握手超时定时器重置为心跳超时定时器，复用同一个 trd 对象
	// 如果客户端在 hb 时间内没有任何消息（包括心跳），连接将被超时关闭
	tr.Set(trd, hb)
	// 白名单检查：白名单用户的连接会打印详细的调试日志
	white = whitelist.Contains(ch.Mid)
	if white {
		whitelist.Printf("key: %s[%s] auth\n", ch.Key, rid)
	}

	// ========== 阶段二：消息循环 ==========
	step = 3
	// hanshake ok start dispatch goroutine
	// 启动独立的写协程（dispatchTCP），与当前读协程形成"一读一写"的双 goroutine 模型
	// 读协程负责从 TCP 读取客户端消息，写协程负责将服务端响应和推送消息写回 TCP
	// 两者通过 Channel 的 CliProto 环形缓冲区和 Signal/Ready 机制协调
	go s.dispatchTCP(conn, wr, wp, wb, ch)
	// 服务端心跳间隔：每隔一段时间主动向 Logic 层续期连接，防止 Logic 认为连接已死
	// 使用随机值避免大量连接同时续期造成惊群效应
	serverHeartbeat := s.RandServerHearbeat()
	for {
		// 从客户端协议环形缓冲区申请一个空槽位，用于存放即将读取的协议帧
		// 如果环形缓冲区已满（消费端来不及处理），此处会返回错误，连接将被关闭
		if p, err = ch.CliProto.Set(); err != nil {
			break
		}
		if white {
			whitelist.Printf("key: %s start read proto\n", ch.Key)
		}
		// 从 TCP 连接中读取一个完整的协议帧（Header + Body）
		// ReadTCP 内部使用带缓冲读取，减少系统调用次数
		if err = p.ReadTCP(rr); err != nil {
			break
		}
		if white {
			whitelist.Printf("key: %s read proto:%v\n", ch.Key, p)
		}
		// 令牌桶限流：对非心跳消息进行限流检查
		// 心跳消息不受限流约束（否则限流会导致连接被超时断开）
		if p.Op != protocol.OpHeartbeat && !ch.AllowMessage() {
			metrics.RateLimitedTotal.Inc()
			log.Warningf("key: %s rate limited", ch.Key)
			ch.CliProto.SetAdv() // 跳过当前槽位，不放入就绪队列
			continue
		}
		if p.Op == protocol.OpHeartbeat {
			// === 心跳处理 ===
			// 收到心跳后重置心跳超时定时器，确保在 hb 时间内没有新消息时能检测到连接异常
			tr.Set(trd, hb)
			// 将操作码改为心跳回复，清空 Body（心跳回复不需要 Payload）
			p.Op = protocol.OpHeartbeatReply
			p.Body = nil
			// 服务端主动续期：每隔 serverHeartbeat 时间向 Logic 层发送一次心跳续期请求
			// Logic 层通过 Redis 记录连接的存活状态，用于推送时判断连接是否在线
			if now := time.Now(); now.Sub(lastHb) > serverHeartbeat {
				if err1 := s.Heartbeat(ctx, ch.Mid, ch.Key); err1 == nil {
					lastHb = now
				}
			}
			if conf.Conf.Debug {
				log.Infof("tcp heartbeat receive key:%s, mid:%d", ch.Key, ch.Mid)
			}
			step++
		} else if p.Op == protocol.OpPushMsgAck {
			// ACK is a one-way client→server notification; forward to Logic
			// but skip echo-back — the client does not expect a reply.
			if err = s.Operate(ctx, p, ch, b); err != nil {
				break
			}
			continue
		} else {
			// === 业务消息处理 ===
			// Operate 根据操作码（Op）分发到不同的处理逻辑：
			//   OpAuth      —— 认证（正常不会走到这里，因为已在握手阶段处理）
			//   OpRoomJoin  —— 加入房间
			//   OpRoomLeave —— 离开房间
			//   OpRoomSend  —— 发送房间消息（如弹幕）
			//   OpBroadcast —— 全局广播
			if err = s.Operate(ctx, p, ch, b); err != nil {
				break
			}
		}
		if white {
			whitelist.Printf("key: %s process proto:%v\n", ch.Key, p)
		}
		// 将当前槽位标记为"已填充"（推进写指针），使 dispatch 写协程能读到这个帧
		ch.CliProto.SetAdv()
		// 通知 dispatch 写协程：有新的协议帧待发送
		// Signal 内部会将帧放入就绪队列，并通过 channel 唤醒 dispatchTCP
		ch.Signal()
		if white {
			whitelist.Printf("key: %s signal\n", ch.Key)
		}
	}
	// ========== 阶段三：连接清理 ==========
	if white {
		whitelist.Printf("key: %s server tcp error(%v)\n", ch.Key, err)
	}
	// io.EOF 表示客户端主动关闭连接，net.ErrClosed 表示连接已被关闭，这两种属于正常断开
	// 其他错误（如协议解析失败）才记录为异常日志
	if err != nil && err != io.EOF && !isClosedError(err) {
		log.Errorf("key: %s server tcp failed error(%v)", ch.Key, err)
	}
	b.Del(ch)    // 从连接桶中摘除 Channel，后续推送将不再能找到此连接
	tr.Del(trd)  // 移除心跳超时定时器
	rp.Put(rb)   // 读缓冲区归还对象池，供后续连接复用
	conn.Close() // 关闭 TCP 连接，dispatchTCP 写协程检测到后也会退出
	ch.Close()   // 关闭 Channel，清理内部资源
	// 通知 Logic 层该连接已断开，Logic 会从 Redis 中删除连接路由信息
	// 后续推送到该用户时不会再尝试投递到此 Comet 节点
	if err = s.Disconnect(ctx, ch.Mid, ch.Key); err != nil {
		log.Errorf("key: %s mid: %d operator do disconnect error(%v)", ch.Key, ch.Mid, err)
	}
	if white {
		whitelist.Printf("key: %s mid: %d disconnect error(%v)\n", ch.Key, ch.Mid, err)
	}
	if conf.Conf.Debug {
		log.Infof("tcp disconnected key: %s mid: %d", ch.Key, ch.Mid)
	}
	metrics.ConnectionsActive.Dec()
}

// dispatchTCP 是每个 TCP 连接的写协程（dispatch goroutine）。
// 与 ServeTCP 中的读协程形成"一读一写"配对：
//   - 读协程：从 TCP 读取客户端消息 → 放入 CliProto 环形缓冲区 → Signal() 通知
//   - 写协程：从 ch.Ready() 阻塞等待就绪事件 → 取出协议帧 → 写入 TCP
//
// 就绪事件有三种来源：
//  1. protocol.ProtoReady  —— 读协程读到了客户端消息（如心跳回复、业务操作结果），从 CliProto 取出写回
//  2. *protocol.Proto      —— 服务端推送消息（来自 Channel.Push()），直接写入 TCP
//  3. protocol.ProtoFinish —— 连接关闭信号，写协程退出
//
// 参数说明：
//
//	conn —— TCP 连接，仅用于出错时 Close()
//	wr   —— 带缓冲的 Writer（共享自 Channel）
//	wp   —— 写缓冲区对象池，用于归还 wb
//	wb   —— 写缓冲区对象，函数退出时归还对象池
//	ch   —— Channel 实例，读写协程共享的核心数据结构
func (s *Server) dispatchTCP(conn *net.TCPConn, wr *bufio.Writer, wp *bytes.Pool, wb *bytes.Buffer, ch *Channel) {
	var (
		err    error
		finish bool
		online int32
		white  = whitelist.Contains(ch.Mid)
	)
	if conf.Conf.Debug {
		log.Infof("key: %s start dispatch tcp goroutine", ch.Key)
	}
	for {
		if white {
			whitelist.Printf("key: %s wait proto ready\n", ch.Key)
		}
		// ch.Ready() 会阻塞，直到读协程调用 ch.Signal() 或 Channel.Push()
		// 返回值 p 的类型决定了事件来源（见函数头部注释）
		var p = ch.Ready()
		if white {
			whitelist.Printf("key: %s proto ready\n", ch.Key)
		}
		if conf.Conf.Debug {
			log.Infof("key:%s dispatch msg:%s", ch.Key, p.String())
		}
		switch p {
		case protocol.ProtoFinish:
			// 连接关闭信号：读协程已退出或 ch.Close() 被调用
			if white {
				whitelist.Printf("key: %s receive proto finish\n", ch.Key)
			}
			if conf.Conf.Debug {
				log.Infof("key: %s wakeup exit dispatch goroutine", ch.Key)
			}
			finish = true
			goto failed
		case protocol.ProtoReady:
			// 读协程读到了客户端消息，从 CliProto 环形缓冲区中逐个取出并写回 TCP
			// 典型场景：心跳回复、业务操作的响应（如加入房间成功）
			for {
				// Get() 从环形缓冲区取出一个已填充的协议帧
				if p, err = ch.CliProto.Get(); err != nil {
					break // 环形缓冲区已空，退出内层循环
				}
				if white {
					whitelist.Printf("key: %s start write client proto%v\n", ch.Key, p)
				}
				if p.Op == protocol.OpHeartbeatReply {
					// 心跳回复：附带当前房间在线人数
					if ch.Room != nil {
						online = ch.Room.OnlineNum()
					}
					if err = p.WriteTCPHeart(wr, online); err != nil {
						goto failed
					}
				} else {
					// 普通业务响应：序列化并写入带缓冲 Writer
					if err = p.WriteTCP(wr); err != nil {
						goto failed
					}
				}
				if white {
					whitelist.Printf("key: %s write client proto%v\n", ch.Key, p)
				}
				p.Body = nil // avoid memory leak
				// 推进读指针，释放当前槽位供读协程复用
				ch.CliProto.GetAdv()
			}
		default:
			if white {
				whitelist.Printf("key: %s start write server proto%v\n", ch.Key, p)
			}
			// 服务端推送消息（来自 Channel.Push()）：直接写入 TCP
			// 推送路径：Logic → Kafka → Comet.Push() → Channel.Push() → ch.Signal() →
			//此处
			if err = p.WriteTCP(wr); err != nil {
				goto failed
			}
			if white {
				whitelist.Printf("key: %s write server proto%v\n", ch.Key, p)
			}
			if conf.Conf.Debug {
				log.Infof("tcp sent a message key:%s mid:%d proto:%+v", ch.Key, ch.Mid, p)
			}
		}
		if white {
			whitelist.Printf("key: %s start flush \n", ch.Key)
		}
		// only hungry flush response
		// 每处理完一个就绪事件后立即 flush，将缓冲区中的数据刷入 TCP 连接
		// 这种"饥饿刷新"策略确保消息能及时送达客户端，而非攒满缓冲区再发
		if err = wr.Flush(); err != nil {
			break
		}
		if white {
			whitelist.Printf("key: %s flush\n", ch.Key)
		}
	}
failed:
	if white {
		whitelist.Printf("key: %s dispatch tcp error(%v)\n", ch.Key, err)
	}
	if err != nil {
		log.Errorf("key: %s dispatch tcp error(%v)", ch.Key, err)
	}
	conn.Close() // 关闭 TCP 连接，读协程的 ReadTCP 会因此返回错误并退出
	wp.Put(wb)   // 写缓冲区归还对象池
	// must ensure all channel message discard, for reader won't blocking Signal
	// 等待读协程退出（发送 ProtoFinish 信号），确保所有 Channel 消息被消费完毕
	// 如果不等待，读协程可能在 ch.Signal() 上阻塞（因为 dispatch 已退出无人接收）
	for !finish {
		finish = (ch.Ready() == protocol.ProtoFinish)
	}
	if conf.Conf.Debug {
		log.Infof("key: %s dispatch goroutine exit", ch.Key)
	}
}

// authTCP 完成 TCP 连接的握手认证流程。
//
// 协议交互过程：
//  1. 循环读取客户端发送的协议帧，直到收到 OpAuth 操作码（跳过非认证帧）
//  2. 调用 Server.Connect 向 Logic 层注册连接，Logic 会：
//     - 验证 token 合法性（调用业务方的验证接口）
//     - 在 Redis 中建立 key → Comet 节点的路由映射
//     - 返回 mid（用户ID）、key（连接唯一标识）、rid（房间ID）、accepts（订阅事件）、hb（心跳间隔）
//  3. 向客户端回复 OpAuthReply 确认认证成功
//
// 参数：
//
//	ctx  —— 上下文，连接断开时会被 cancel
//	rr/wr —— Channel 的带缓冲 Reader/Writer
//	p    —— 用于存放认证请求帧的协议对象（已从 CliProto 环形缓冲区申请）
//
// 返回值：
//
//	mid     —— 用户 ID（消息标识 ID）
//	key     —— 连接唯一标识（由 Logic 层生成，用于后续路由和推送）
//	rid     —— 房间 ID（认证时可选加入初始房间）
//	accepts —— 客户端订阅的事件类型列表（如弹幕、礼物、系统通知等）
//	hb      —— 心跳间隔，客户端应在此时间内至少发送一次心跳
//	err     —— 错误信息，nil 表示认证成功
func (s *Server) authTCP(ctx context.Context, rr *bufio.Reader, wr *bufio.Writer, p *protocol.Proto) (mid int64, key, rid string, accepts []int32, hb time.Duration, err error) {
	for {
		// 从 TCP 读取一个协议帧
		if err = p.ReadTCP(rr); err != nil {
			return
		}
		if p.Op == protocol.OpAuth {
			break // 收到认证请求，跳出循环
		} else {
			// 客户端在认证前发送了非认证帧，记录错误日志但继续等待
			log.Errorf("tcp request operation(%d) not auth", p.Op)
		}
	}
	// 调用 Server.Connect 完成认证和会话注册
	// Connect 内部会调用 Operator.Connect（由业务方实现）验证 token
	if mid, key, rid, accepts, hb, err = s.Connect(ctx, p, ""); err != nil {
		log.Errorf("authTCP.Connect(key:%v).err(%v)", key, err)
		return
	}
	// 构造认证回复帧：操作码改为 OpAuthReply，清空 Body
	p.Op = protocol.OpAuthReply
	p.Body = nil
	// 将认证回复写入 TCP 连接
	if err = p.WriteTCP(wr); err != nil {
		log.Errorf("authTCP.WriteTCP(key:%v).err(%v)", key, err)
		return
	}
	// flush 确保认证回复立即发送给客户端，而非留在缓冲区中
	err = wr.Flush()
	return
}
