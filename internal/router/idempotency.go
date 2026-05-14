package router

import (
	"context"
	"hash/fnv"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
)

const (
	numShards    = 64
	maxShardSize = 2000 // per-shard cap, ~128k total
	entryTTL     = 5 * time.Minute
)

type idemShard struct {
	mu   sync.Mutex
	seen map[uint64]int64 // hash → expiry (unix nano)
}

// IdempotencyChecker prevents duplicate message delivery using a
// two-layer approach: in-memory sharded hash set with TTL (fast) + Redis lookup (exact).
type IdempotencyChecker struct {
	shards [numShards]idemShard
	msgDAO dao.MessageDAO
}

// NewIdempotencyChecker creates a new IdempotencyChecker.
func NewIdempotencyChecker(md dao.MessageDAO) *IdempotencyChecker {
	c := &IdempotencyChecker{msgDAO: md}
	for i := range c.shards {
		c.shards[i].seen = make(map[uint64]int64, maxShardSize/4)
	}
	return c
}

// IsDuplicate checks if a message ID has already been processed.
// Fast path: in-memory hash set. Slow path: Redis lookup.
func (c *IdempotencyChecker) IsDuplicate(ctx context.Context, msgID string) bool {
	h := hashMsgID(msgID)
	sh := &c.shards[h%numShards]

	sh.mu.Lock()
	expiry, ok := sh.seen[h]
	if ok && time.Now().UnixNano() < expiry {
		sh.mu.Unlock()
		return true
	}
	if ok {
		delete(sh.seen, h) // expired entry
	}
	sh.mu.Unlock()

	// Fall back to Redis for exact check
	status, err := c.msgDAO.GetMessageStatus(ctx, msgID)
	if err != nil || len(status) == 0 {
		return false
	}
	s := status["status"]
	return s == MsgStatusAcked || s == MsgStatusDelivered
}

// MarkSeen records a message ID as processed.
func (c *IdempotencyChecker) MarkSeen(msgID string) {
	h := hashMsgID(msgID)
	sh := &c.shards[h%numShards]
	now := time.Now().UnixNano()

	sh.mu.Lock()
	sh.seen[h] = now + int64(entryTTL)
	if len(sh.seen) > maxShardSize {
		c.evictShardLocked(sh, now)
	}
	sh.mu.Unlock()
}

// evictShardLocked removes expired entries and, if still over limit,
// clears the oldest half. Must be called with sh.mu held.
func (c *IdempotencyChecker) evictShardLocked(sh *idemShard, now int64) {
	// First pass: remove expired entries
	for k, exp := range sh.seen {
		if now > exp {
			delete(sh.seen, k)
		}
	}
	if len(sh.seen) <= maxShardSize {
		return
	}
	// Still over limit: collect all timestamps, find median, evict older half
	type entry struct {
		k   uint64
		exp int64
	}
	entries := make([]entry, 0, len(sh.seen))
	for k, exp := range sh.seen {
		entries = append(entries, entry{k, exp})
	}
	// Partial sort to find the oldest half (selection sort limited to half)
	mid := len(entries) / 2
	for i := 0; i < mid; i++ {
		minIdx := i
		for j := i + 1; j < len(entries); j++ {
			if entries[j].exp < entries[minIdx].exp {
				minIdx = j
			}
		}
		entries[i], entries[minIdx] = entries[minIdx], entries[i]
	}
	for i := 0; i < mid; i++ {
		delete(sh.seen, entries[i].k)
	}
}

func hashMsgID(msgID string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(msgID))
	return h.Sum64()
}
