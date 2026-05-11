package router

import (
	"context"
	"hash/fnv"
	"sync"

	"github.com/Terry-Mao/goim/internal/logic/dao"
)

// IdempotencyChecker prevents duplicate message delivery using a
// two-layer approach: in-memory hash set (fast) + Redis lookup (exact).
type IdempotencyChecker struct {
	mu     sync.RWMutex
	seen   map[uint64]struct{} // in-memory set of recently-seen msgIDs
	msgDAO dao.MessageDAO
}

// NewIdempotencyChecker creates a new IdempotencyChecker.
func NewIdempotencyChecker(md dao.MessageDAO) *IdempotencyChecker {
	return &IdempotencyChecker{
		seen:   make(map[uint64]struct{}, 10000),
		msgDAO: md,
	}
}

// IsDuplicate checks if a message ID has already been processed.
// Fast path: in-memory hash set. Slow path: Redis lookup.
func (c *IdempotencyChecker) IsDuplicate(ctx context.Context, msgID string) bool {
	h := hashMsgID(msgID)
	c.mu.RLock()
	_, ok := c.seen[h]
	c.mu.RUnlock()
	if ok {
		return true
	}

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
	c.mu.Lock()
	c.seen[h] = struct{}{}
	if len(c.seen) > 50000 {
		c.seen = make(map[uint64]struct{}, 10000)
	}
	c.mu.Unlock()
}

func hashMsgID(msgID string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(msgID))
	return h.Sum64()
}
