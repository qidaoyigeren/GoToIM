// ============================================================================
// 文件：idempotency.go
// 职责：消息幂等性检查 —— 防止同一条消息被重复投递/处理。
// ============================================================================
//
// 一、为什么需要幂等性？
//
//   IM 系统中消息投递遵循"至少一次"语义：因网络重试、服务重启、Kafka 重平衡等原因，
//   同一条消息可能被投递多次。如果业务层不做去重，用户就会收到重复消息。
//   解决方案："至少一次投递 + 业务层去重 = 精确一次"，本文件就是这个"去重"环节。
//
// 二、两层防护架构
//
//   ┌─────────────────────────────────────────────────────────┐
//   │                   IsDuplicate(msgID)                     │
//   ├─────────────────────────────────────────────────────────┤
//   │  第一层（快速路径）：内存分片哈希集合 + TTL               │
//   │    · 命中 → 直接返回 true（重复，跳过）                  │
//   │    · 未命中 → 进入第二层                                  │
//   │    · 命中但已过期 → 惰性删除，进入第二层                  │
//   ├─────────────────────────────────────────────────────────┤
//   │  第二层（慢速路径）：Redis 消息状态查询                   │
//   │    · 查询到 status=Acked/Delivered → 返回 true           │
//   │    · 查询失败 / 无状态 → 保守返回 false（当新消息处理）   │
//   └─────────────────────────────────────────────────────────┘
//
//   为什么需要两层？
//    - 内存层（5 分钟 TTL）提供极致速度，覆盖短时间内重复投递的绝大多数场景。
//    - Redis 层兜底内存过期 / 服务重启后的重复投递，消息状态保留时间远长于 5 分钟。
//
// 三、分片设计（减少锁竞争）
//
//   64 个独立分片，每个分片有独立的 sync.Mutex。
//   消息 ID 经 FNV-64a 哈希后取模路由到某个分片。
//   查询/写入只锁目标分片，其余 63 个分片不受影响 → 并发吞吐量提升 64 倍（理想情况）。
//
// 四、内存淘汰策略
//
//   每个分片容量上限 2000 条（总计 ~128000 条），TTL 为 5 分钟。超限时两阶段淘汰：
//     阶段 1：删除所有已过期条目（大多数情况到此为止）。
//     阶段 2：若仍超限，用部分选择排序找出过期时间最早的一半并删除。
//
// 五、数据流
//
//   消息到达 → IsDuplicate(msgID)
//                ├─ true  → 丢弃（重复消息）
//                └─ false → 正常处理 → 处理成功 → MarkSeen(msgID) 写入内存缓存
//
// 六、关键权衡
//
//   · 内存缓存是尽力而为的（best-effort），允许丢失（服务重启 / TTL 过期），
//     因此 MarkSeen 写失败不影响正确性，只是下次需要走 Redis 慢路径。
//   · IsDuplicate 查询 Redis 失败时保守返回 false，宁可重复投递也不丢消息。
//   · FNV-64a 非加密哈希有碰撞概率，但最多导致误判重复（漏投一条），
//     不会导致安全漏洞，且 Redis 二次校验进一步降低误判概率。

package router

import (
	"context"
	"hash/fnv"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
)

const (
	// numShards 分片数量。将消息 ID 的哈希值分散到 64 个独立的分片上，
	// 每个分片有自己的锁，这样不同分片上的操作互不阻塞，减少锁竞争。
	numShards = 64

	// maxShardSize 每个分片最多缓存的条目数（2000 条）。
	// 64 个分片 × 2000 ≈ 128000 条总内存缓存上限。
	// 超过上限时，会触发淘汰（先清理过期条目，不够则驱逐较旧的一半）。
	maxShardSize = 2000

	// entryTTL 每条缓存在内存中的存活时间（5 分钟）。
	// 超过 5 分钟的条目即使没被主动淘汰，也会在 IsDuplicate 查询时被判为过期并删除。
	entryTTL = 5 * time.Minute
)

// idemShard 单个分片，包含一把互斥锁和一个哈希集合。
type idemShard struct {
	mu sync.Mutex
	// seen 存储 消息ID哈希值 → 过期时间戳（Unix 纳秒）
	// key 是 uint64 哈希而非原始字符串，节省内存且比较更快。
	seen map[uint64]int64
}

// IdempotencyChecker 消息幂等性检查器。
//
// 设计目标：防止同一条消息被重复投递/处理，即"至少一次投递 + 业务层去重 = 精确一次"。
//
// 采用两层防护：
//  1. 快速路径（内存）：分片哈希集合 + TTL，命中率最高，O(1) 查重，无需网络 IO。
//  2. 慢速路径（Redis）：当内存未命中时，查询 Redis 中该消息的状态。
//     用于兜底——处理内存缓存过期 / 服务重启后缓存丢失的场景。
//
// 为什么不在内存命中时也查 Redis？
//
//	内存 TTL（5分钟）远小于消息状态在 Redis 中的保留时间。
//	如果在内存中找到了，说明 5 分钟内已处理过，不可能重复，无需再查 Redis。
//	只有在内存未命中时才可能有两种情况：a) 确实新消息  b) 旧消息但内存缓存已过期，
//	此时查 Redis 来区分这两种情况。
type IdempotencyChecker struct {
	// shards 固定大小的分片数组（非切片），64 个分片在创建时一次性初始化，
	// 避免运行时的动态分配和 map 扩容带来的锁持有时间不可控。
	shards [numShards]idemShard

	// msgDAO 数据访问层，用于查询 Redis 中持久化的消息状态。
	msgDAO dao.MessageDAO
}

// NewIdempotencyChecker 创建一个幂等性检查器实例。
//
// 初始化时预分配每个分片的 map，初始容量为 maxShardSize/4（500），
// 这样在大多数场景下无需 map 自动扩容，减少 GC 压力和 rehash 开销。
func NewIdempotencyChecker(md dao.MessageDAO) *IdempotencyChecker {
	c := &IdempotencyChecker{msgDAO: md}
	for i := range c.shards {
		c.shards[i].seen = make(map[uint64]int64, maxShardSize/4)
	}
	return c
}

// IsDuplicate 判断指定消息 ID 是否已经被处理过。
//
// 执行流程：
//  1. 对 msgID 做 FNV-64a 哈希，得到 uint64 的哈希值。
//  2. h % numShards 确定落在哪个分片，只锁该分片（其他分片不受影响）。
//  3. 查分片的 seen map：
//     - 如果找到且未过期 → 返回 true（重复消息，直接丢弃）。
//     - 如果找到但已过期 → 删除该条目（惰性清理），继续下一步。
//     - 如果未找到 → 继续下一步。
//  4. 降级到 Redis 查询：调用 GetMessageStatus 获取消息的持久化状态。
//     - 如果查询失败或状态为空 → 保守处理，返回 false（当作新消息处理，宁可重复投递也不丢消息）。
//     - 如果状态是已确认（Acked）或已送达（Delivered） → 返回 true（确实处理过了）。
//
// 返回值：
//
//	true  → 消息已处理过，调用方应跳过本次处理。
//	false → 消息未处理（或 Redis 查询异常时保守返回 false），调用方应正常处理。
func (c *IdempotencyChecker) IsDuplicate(ctx context.Context, msgID string) bool {
	// 计算消息 ID 的哈希值
	h := hashMsgID(msgID)

	// 定位到对应的分片（取模路由）
	sh := &c.shards[h%numShards]

	// 查询内存缓存
	sh.mu.Lock()
	expiry, ok := sh.seen[h]
	if ok && time.Now().UnixNano() < expiry {
		// 命中且未过期 → 重复消息
		sh.mu.Unlock()
		return true
	}
	if ok {
		// 命中但已过期 → 惰性删除，避免 map 无限膨胀
		delete(sh.seen, h)
	}
	sh.mu.Unlock()

	// 内存未命中，降级到 Redis 做精确判断
	status, err := c.msgDAO.GetMessageStatus(ctx, msgID)
	if err != nil || len(status) == 0 {
		// 查询失败或无状态记录 → 保守返回 false（当作新消息）
		return false
	}
	s := status["status"]
	return s == MsgStatusAcked || s == MsgStatusDelivered
}

// MarkSeen 将消息 ID 标记为"已处理"，写入内存缓存。
//
// 调用时机：消息投递/处理成功后调用，这样后续的重复投递能被 IsDuplicate 拦截。
//
// 执行流程：
//  1. 哈希 + 取模定位分片。
//  2. 写入 seen map，过期时间 = 当前时间 + entryTTL（5分钟）。
//  3. 如果写入后分片超过 maxShardSize，触发淘汰流程。
func (c *IdempotencyChecker) MarkSeen(msgID string) {
	h := hashMsgID(msgID)
	sh := &c.shards[h%numShards]
	now := time.Now().UnixNano()

	sh.mu.Lock()
	// 写入条目，过期时间 = 当前纳秒时间戳 + 5分钟（纳秒）
	sh.seen[h] = now + int64(entryTTL)
	// 容量控制：超过分片上限则淘汰
	if len(sh.seen) > maxShardSize {
		c.evictShardLocked(sh, now)
	}
	sh.mu.Unlock()
}

// evictShardLocked 淘汰分片中的旧条目，以控制内存使用。
//
// 两阶段淘汰策略：
//
//	阶段 1 — 清理过期条目：遍历删除所有已过期的条目。
//	          如果清理后已低于上限，直接返回（大多数情况到此为止）。
//	阶段 2 — 驱逐较旧的一半：如果清理过期条目后仍然超限，
//	          说明有大量未过期条目，需要做"部分选择排序"找出过期时间最早的一半并删除。
//
// 为什么用部分选择排序而非完全排序？
//
//	只需要淘汰一半，不需要全排序。部分选择排序 O(n × n/2) 在 n=2000 时约 200 万次比较，
//	对于不频繁触发的淘汰场景是可接受的。完全 sort.Slice 也是 O(n log n)，
//	但选择排序避免了额外的内存分配（原地交换）。
//
// 调用前提：调用方必须持有 sh.mu 互斥锁。
func (c *IdempotencyChecker) evictShardLocked(sh *idemShard, now int64) {
	// 阶段 1：删除所有已过期条目
	for k, exp := range sh.seen {
		if now > exp {
			delete(sh.seen, k)
		}
	}
	// 清理后如果已经在限制内，直接返回
	if len(sh.seen) <= maxShardSize {
		return
	}

	// 阶段 2：仍然超限，找出过期时间最早的一半条目并删除
	// 将所有条目收集到切片中（结构体值类型，栈分配，无 GC 压力）
	type entry struct {
		k   uint64 // 消息哈希值
		exp int64  // 过期时间（Unix 纳秒）
	}
	entries := make([]entry, 0, len(sh.seen))
	for k, exp := range sh.seen {
		entries = append(entries, entry{k, exp})
	}

	// 部分选择排序：只把最小的 mid 个元素排到切片前半部分
	// 例如 2000 条中找出最早的 1000 条
	mid := len(entries) / 2
	for i := 0; i < mid; i++ {
		minIdx := i
		for j := i + 1; j < len(entries); j++ {
			if entries[j].exp < entries[minIdx].exp {
				minIdx = j
			}
		}
		// 将第 i 小的元素交换到位置 i
		entries[i], entries[minIdx] = entries[minIdx], entries[i]
	}

	// 从 map 中删除最早的一半条目
	for i := 0; i < mid; i++ {
		delete(sh.seen, entries[i].k)
	}
}

// hashMsgID 使用 FNV-64a 算法对消息 ID 做哈希。
//
// 选择 FNV-64a 的原因：
//   - 速度快：FNV 只需要乘法和异或运算，比加密哈希（SHA/MD5）快一个数量级。
//   - 分布均匀：对于短字符串（消息 ID 通常在 32-64 字符以内）分布均匀，碰撞率极低。
//   - 确定性：相同输入永远产生相同输出，无随机性，适合用作 map key。
//
// 注意：FNV 不是加密哈希，存在哈希碰撞的理论可能。
// 但这不影响正确性——即使两个不同的 msgID 哈希到同一个 uint64，
// 最多导致误判为重复（漏投一条消息），不会导致安全漏洞。
// 且 Redis 层的二次校验进一步降低了误判概率。
func hashMsgID(msgID string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(msgID))
	return h.Sum64()
}
