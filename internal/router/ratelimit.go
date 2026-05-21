package router

import (
	"sync"
	"time"
)

const rateLimitShardCount = 64

// RateLimiter enforces per-user and global rate limits at the Router entry.
type RateLimiter struct {
	globalMu sync.Mutex
	global   *tokenBucket
	shards   [rateLimitShardCount]rateLimitShard
}

type rateLimitShard struct {
	mu         sync.Mutex
	userLimits map[int64]*tokenBucket
}

type tokenBucket struct {
	rate     float64
	burst    float64
	tokens   float64
	lastTime int64
}

// NewRateLimiter creates a new RateLimiter.
func NewRateLimiter(globalRate, globalBurst float64) *RateLimiter {
	r := &RateLimiter{
		global: &tokenBucket{rate: globalRate, burst: globalBurst, tokens: globalBurst, lastTime: time.Now().UnixNano()},
	}
	for i := range r.shards {
		r.shards[i].userLimits = make(map[int64]*tokenBucket)
	}
	return r
}

// AllowUser checks if a user is within their rate limit.
func (r *RateLimiter) AllowUser(uid int64, rate, burst float64) bool {
	r.globalMu.Lock()
	if !r.global.allow() {
		r.globalMu.Unlock()
		return false
	}
	r.globalMu.Unlock()

	shard := &r.shards[rateLimitShardIndex(uid)]
	shard.mu.Lock()
	defer shard.mu.Unlock()
	tb, ok := shard.userLimits[uid]
	if !ok {
		tb = &tokenBucket{rate: rate, burst: burst, tokens: burst, lastTime: time.Now().UnixNano()}
		shard.userLimits[uid] = tb
	}
	return tb.allow()
}

func rateLimitShardIndex(uid int64) int64 {
	if uid < 0 {
		uid = -uid
	}
	return uid % rateLimitShardCount
}

func (tb *tokenBucket) allow() bool {
	now := time.Now().UnixNano()
	elapsed := float64(now-tb.lastTime) / 1e9
	tb.lastTime = now
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.burst {
		tb.tokens = tb.burst
	}
	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}
