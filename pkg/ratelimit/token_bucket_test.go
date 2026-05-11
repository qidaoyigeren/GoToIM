package ratelimit

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTokenBucket_BasicAllow(t *testing.T) {
	tb := NewTokenBucket(10, 5) // 10/sec, capacity 5
	for i := 0; i < 5; i++ {
		assert.True(t, tb.Allow(), "should allow up to capacity")
	}
	assert.False(t, tb.Allow(), "should deny when exhausted")
}

func TestTokenBucket_Refill(t *testing.T) {
	tb := NewTokenBucket(100, 1) // 100/sec, capacity 1
	assert.True(t, tb.Allow())
	assert.False(t, tb.Allow())

	time.Sleep(15 * time.Millisecond) // ~1.5 tokens refilled at 100/sec
	assert.True(t, tb.Allow())
}

func TestTokenBucket_AllowN(t *testing.T) {
	tb := NewTokenBucket(10, 10)
	assert.True(t, tb.AllowN(5))
	assert.True(t, tb.AllowN(5))
	assert.False(t, tb.AllowN(1)) // exhausted

	time.Sleep(110 * time.Millisecond) // ~1 token refilled
	assert.True(t, tb.AllowN(1))
}

func TestTokenBucket_CapacityCapped(t *testing.T) {
	tb := NewTokenBucket(1000, 3) // high rate, low capacity
	time.Sleep(10 * time.Millisecond)
	// Even with high rate, tokens should not exceed capacity
	tb.mu.Lock()
	tb.refill()
	assert.LessOrEqual(t, tb.tokens, float64(3))
	tb.mu.Unlock()
}

func TestTokenBucket_ConcurrentSafety(t *testing.T) {
	tb := NewTokenBucket(1000, 1000) // generous limits
	var wg sync.WaitGroup
	allowed := make(chan bool, 200)

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed <- tb.Allow()
		}()
	}
	wg.Wait()
	close(allowed)

	trueCount := 0
	for v := range allowed {
		if v {
			trueCount++
		}
	}
	// With 1000 capacity and 200 concurrent, all should succeed
	assert.Equal(t, 200, trueCount)
}

func TestTokenBucket_ZeroRate(t *testing.T) {
	tb := NewTokenBucket(0, 5) // no refill
	for i := 0; i < 5; i++ {
		assert.True(t, tb.Allow())
	}
	assert.False(t, tb.Allow())
	// Even after waiting, no refill
	time.Sleep(10 * time.Millisecond)
	assert.False(t, tb.Allow())
}
