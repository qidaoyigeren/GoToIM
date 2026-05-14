package comet

import (
	"sync"
	"sync/atomic"

	pb "github.com/Terry-Mao/goim/api/comet"
	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/comet/conf"
)

// Bucket 是连接分片管理器，负责管理一组 Channel（用户连接）和 Room（聊天室）。
// 整个 Comet 服务会创建多个 Bucket，每个 Bucket 独立管理一部分连接，
// 通过这种分片（sharding）机制减少锁竞争，提升并发性能。
//
// 工作流程：
//  1. 客户端连接到来时，按 key 哈希分配到某个 Bucket
//  2. Bucket 将该连接（Channel）存入 chs 映射表
//  3. 如果连接指定了房间号，则同时加入对应的 Room
//  4. 广播消息时，可向整个 Bucket 广播，也可向指定 Room 广播
//
// 字段说明：
//   - c:           配置信息，包含各 map 初始容量、广播协程数量等
//   - cLock:       读写锁，保护 chs、rooms、ipCnts 三个 map 的并发安全
//   - chs:         连接映射表，key 为用户子键（如用户ID+设备类型），value 为对应的 Channel
//     同一个 key 只能有一个连接（新连接会顶掉旧连接）
//   - rooms:       房间映射表，key 为房间ID，value 为 Room 对象
//   - routines:    广播协程的 channel 数组，用于房间消息广播的分发
//   - routinesNum: 原子计数器，用于轮询（round-robin）选择广播协程
//   - ipCnts:      IP 计数器，记录每个 IP 下有多少个连接，用于连接数限制
type Bucket struct {
	c     *conf.Bucket
	cLock sync.RWMutex        // protect the channels for chs
	chs   map[string]*Channel // map sub key to a channel
	// room
	rooms       map[string]*Room // bucket room channels
	routines    []chan *pb.BroadcastRoomReq
	routinesNum uint64

	ipCnts map[string]int32
}

func NewBucket(c *conf.Bucket) (b *Bucket) {
	b = new(Bucket)
	b.chs = make(map[string]*Channel, c.Channel)
	b.ipCnts = make(map[string]int32)
	b.c = c
	b.rooms = make(map[string]*Room, c.Room)
	b.routines = make([]chan *pb.BroadcastRoomReq, c.RoutineAmount)
	for i := uint64(0); i < c.RoutineAmount; i++ {
		c := make(chan *pb.BroadcastRoomReq, c.RoutineSize)
		b.routines[i] = c
		go b.roomproc(c)
	}
	return
}

// ChannelCount 返回当前 Bucket 中管理的连接（Channel）总数。
// 使用读锁保证并发安全。
func (b *Bucket) ChannelCount() int {
	b.cLock.RLock()
	n := len(b.chs)
	b.cLock.RUnlock()
	return n
}

// RoomCount 返回当前 Bucket 中房间（Room）总数。
// 注意：这个数字包含在线人数为 0 的房间（空房间不会自动清理，需调用 DelRoom）。
func (b *Bucket) RoomCount() int {
	b.cLock.RLock()
	n := len(b.rooms)
	b.cLock.RUnlock()
	return n
}

// RoomsCount 获取所有在线人数大于 0 的房间 ID 及其在线人数。
// 返回的 map 中，key 为房间ID，value 为该房间的在线人数。
// 这个信息通常用于监控、上报或者前端展示房间热度。
func (b *Bucket) RoomsCount() (res map[string]int32) {
	var (
		roomID string
		room   *Room
	)
	b.cLock.RLock()
	res = make(map[string]int32)
	for roomID, room = range b.rooms {
		if room.Online > 0 {
			res[roomID] = room.Online
		}
	}
	b.cLock.RUnlock()
	return
}

// ChangeRoom 将指定 Channel 从当前房间切换到新房间。
//
// 处理逻辑：
//  1. 如果 nrid（新房间ID）为空字符串，表示离开房间：
//     - 从旧房间中移除该 Channel
//     - 如果旧房间变空（无在线成员），则从 Bucket 中删除该房间
//     - 将 Channel 的 Room 引用置为 nil
//  2. 如果 nrid 不为空：
//     - 先从旧房间中移除 Channel（同上）
//     - 然后在 Bucket 的 rooms 中查找或创建新房间
//     - 将 Channel 加入新房间
//     - 更新 Channel 的 Room 引用
//
// 参数：
//   - nrid: 新房间ID
//   - ch:   需要切换房间的连接
func (b *Bucket) ChangeRoom(nrid string, ch *Channel) (err error) {
	var (
		nroom *Room
		ok    bool
		oroom = ch.Room // 记录当前所在房间
	)
	// change to no room
	if nrid == "" {
		if oroom != nil && oroom.Del(ch) {
			b.DelRoom(oroom)
		}
		ch.Room = nil
		return
	}
	b.cLock.Lock()
	if nroom, ok = b.rooms[nrid]; !ok {
		nroom = NewRoom(nrid)
		b.rooms[nrid] = nroom
	}
	b.cLock.Unlock()
	if oroom != nil && oroom.Del(ch) {
		b.DelRoom(oroom)
	}

	if err = nroom.Put(ch); err != nil {
		return
	}
	ch.Room = nroom
	return
}

// Put 将一个 Channel（连接）注册到 Bucket 中。
//
// 执行流程：
//  1. 加写锁，保证并发安全
//  2. 如果该 key 已存在旧连接，关闭旧连接（踢掉旧设备/旧会话）
//  3. 将新 Channel 存入 chs 映射表
//  4. 如果指定了房间ID（rid），将 Channel 加入对应房间
//  5. 更新 IP 计数（用于连接数限制）
//  6. 解锁，然后调用 room.Put 将 Channel 放入房间链表
//
// 参数：
//   - rid: 房间ID，为空则不加入任何房间
//   - ch:  要注册的连接
//
// 注意：同一个 key（用户子键）只能有一个活跃连接，新连接会关闭旧连接。
func (b *Bucket) Put(rid string, ch *Channel) (err error) {
	var (
		room *Room
		ok   bool
	)
	b.cLock.Lock()
	// close old channel
	if dch := b.chs[ch.Key]; dch != nil {
		dch.Close()
	}
	b.chs[ch.Key] = ch
	if rid != "" {
		if room, ok = b.rooms[rid]; !ok {
			room = NewRoom(rid)
			b.rooms[rid] = room
		}
		ch.Room = room
	}
	b.ipCnts[ch.IP]++
	b.cLock.Unlock()
	if room != nil {
		err = room.Put(ch)
	}
	return
}

// Del 从 Bucket 中删除指定的 Channel（连接断开时调用）。
//
// 处理流程：
//  1. 加写锁
//  2. 从 chs 映射表中删除该 Channel（仅当 key 对应的是同一个 Channel 对象时才删除，
//     防止误删已被新连接替换的旧对象）
//  3. 递减该 IP 的连接计数，计数归零时从 ipCnts 中移除该 IP
//  4. 解锁
//  5. 如果 Channel 在某个房间中，从房间中移除它；若房间变为空，则删除房间
//
// 参数：
//   - dch: 要删除的连接
func (b *Bucket) Del(dch *Channel) {
	room := dch.Room
	b.cLock.Lock()
	if ch, ok := b.chs[dch.Key]; ok {
		if ch == dch {
			delete(b.chs, ch.Key)
		}
		// ip counter
		if b.ipCnts[ch.IP] > 1 {
			b.ipCnts[ch.IP]--
		} else {
			delete(b.ipCnts, ch.IP)
		}
	}
	b.cLock.Unlock()
	if room != nil && room.Del(dch) {
		// if empty room, must delete from bucket
		b.DelRoom(room)
	}
}

// Channel 根据 key 获取对应的 Channel（连接）。
// 使用读锁保证并发安全。
//
// 参数：
//   - key: 连接的唯一标识（用户子键）
//
// 返回：
//   - ch: 找到的 Channel，未找到则返回 nil
func (b *Bucket) Channel(key string) (ch *Channel) {
	b.cLock.RLock()
	ch = b.chs[key]
	b.cLock.RUnlock()
	return
}

// Broadcast 向 Bucket 内所有连接广播消息。
// 遍历 chs 中所有 Channel，对每个 Channel：
//  1. 通过 NeedPush(op) 检查是否需要推送该操作类型的消息
//  2. 调用 Push 将消息写入 Channel 的发送缓冲区
//  3. 调用 Signal 通知该 Channel 有数据待发送
//
// 注意：遍历过程中忽略 Push 的错误（消息写入失败不影响其他连接）。
//
// 参数：
//   - p:  要广播的协议消息
//   - op: 操作类型，用于过滤不需要该操作的连接
func (b *Bucket) Broadcast(p *protocol.Proto, op int32) {
	var ch *Channel
	b.cLock.RLock()
	for _, ch = range b.chs {
		if !ch.NeedPush(op) {
			continue
		}
		_ = ch.Push(p)
		ch.Signal()
	}
	b.cLock.RUnlock()
}

// Room 根据房间ID查找房间。
//
// 参数：
//   - rid: 房间ID
//
// 返回：
//   - room: 找到的 Room，未找到则返回 nil
func (b *Bucket) Room(rid string) (room *Room) {
	b.cLock.RLock()
	room = b.rooms[rid]
	b.cLock.RUnlock()
	return
}

// DelRoom 从 Bucket 中删除指定房间。
//
// 执行流程：
//  1. 加写锁，从 rooms 映射表中删除该房间
//  2. 解锁
//  3. 调用 room.Close() 关闭房间（清理房间内链表资源）
//
// 该函数通常在房间的最后一个成员离开时自动调用，确保空房间被及时清理。
func (b *Bucket) DelRoom(room *Room) {
	b.cLock.Lock()
	delete(b.rooms, room.ID)
	b.cLock.Unlock()
	room.Close()
}

// BroadcastRoom 向指定房间广播消息。
//
// 采用轮询（round-robin）方式将广播请求分发到多个协程处理：
//  1. 原子递增 routinesNum 计数器
//  2. 对 RoutineAmount 取模，选出一个广播 channel
//  3. 将广播请求写入该 channel（非阻塞，因为有缓冲区）
//
// 被选中的 roomproc 协程会从 channel 读取请求，找到对应 Room 后调用 room.Push 完成广播。
// 多协程设计避免单个房间的广播阻塞其他房间的消息。
//
// 参数：
//   - arg: 房间广播请求，包含 RoomID 和要广播的协议消息
func (b *Bucket) BroadcastRoom(arg *pb.BroadcastRoomReq) {
	num := atomic.AddUint64(&b.routinesNum, 1) % b.c.RoutineAmount
	b.routines[num] <- arg
}

// Rooms 获取所有有在线成员的房间ID集合。
// 与 RoomsCount 类似，但只返回房间ID（不返回人数），用 map[string]struct{} 作为集合使用。
func (b *Bucket) Rooms() (res map[string]struct{}) {
	var (
		roomID string
		room   *Room
	)
	res = make(map[string]struct{})
	b.cLock.RLock()
	for roomID, room = range b.rooms {
		if room.Online > 0 {
			res[roomID] = struct{}{}
		}
	}
	b.cLock.RUnlock()
	return
}

// IPCount 获取当前 Bucket 中所有已连接客户端的 IP 地址集合。
// 返回 map[string]struct{} 作为 set 使用，可直接用 _, ok := res[ip] 判断 IP 是否存在。
func (b *Bucket) IPCount() (res map[string]struct{}) {
	var (
		ip string
	)
	b.cLock.RLock()
	res = make(map[string]struct{}, len(b.ipCnts))
	for ip = range b.ipCnts {
		res[ip] = struct{}{}
	}
	b.cLock.RUnlock()
	return
}

// UpRoomsCount 根据外部传入的 roomCountMap 更新所有房间的全服在线人数（AllOnline）。
//
// roomCountMap 由上层（如 job 模块）汇总所有 Bucket 各房间的在线人数后下发，
// 这样每个 Room 的 AllOnline 字段就能反映该房间在整个集群中的总在线人数，
// 而不仅仅是当前 Bucket 内的局部在线人数。
//
// 参数：
//   - roomCountMap: 房间ID → 全服在线人数的映射
func (b *Bucket) UpRoomsCount(roomCountMap map[string]int32) {
	var (
		roomID string
		room   *Room
	)
	b.cLock.RLock()
	for roomID, room = range b.rooms {
		room.AllOnline = roomCountMap[roomID]
	}
	b.cLock.RUnlock()
}

// Close 关闭 Bucket，停止所有广播协程。
// 通过 close 每个 routines 中的 channel，使对应的 roomproc 协程退出 range 循环并结束。
// 应在服务优雅关闭时调用。
func (b *Bucket) Close() {
	for _, c := range b.routines {
		close(c)
	}
}

// roomproc 房间广播处理协程。
// 每个协程独占一个 channel（在 NewBucket 中创建），
// 不断从 channel 读取 BroadcastRoomReq，找到对应 Room 后调用 room.Push 完成广播。
//
// 设计要点：
//   - 多个协程并行处理，避免一个房间的广播阻塞其他房间
//   - 协程数量由 Bucket.RoutineAmount 配置决定
func (b *Bucket) roomproc(c chan *pb.BroadcastRoomReq) {
	for arg := range c {
		if room := b.Room(arg.RoomID); room != nil {
			room.Push(arg.Proto)
		}
	}
}
