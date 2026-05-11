package router

import (
	"sync"
	"time"
)

// RateLimiter enforces per-user and global rate limits at the Router entry.
type RateLimiter struct {
	mu         sync.Mutex
	userLimits map[int64]*tokenBucket
	global     *tokenBucket
}

type tokenBucket struct {
	rate     float64
	burst    float64
	tokens   float64
	lastTime int64
}

// NewRateLimiter creates a new RateLimiter.
func NewRateLimiter(globalRate, globalBurst float64) *RateLimiter {
	return &RateLimiter{
		userLimits: make(map[int64]*tokenBucket),
		global:     &tokenBucket{rate: globalRate, burst: globalBurst, tokens: globalBurst},
	}
}

// AllowUser checks if a user is within their rate limit.
func (r *RateLimiter) AllowUser(uid int64, rate, burst float64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.global.allow() {
		return false
	}

	tb, ok := r.userLimits[uid]
	if !ok {
		tb = &tokenBucket{rate: rate, burst: burst, tokens: burst}
		r.userLimits[uid] = tb
	}
	return tb.allow()
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
