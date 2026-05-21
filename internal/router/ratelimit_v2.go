// 多维度限流器 —— Phase 2 升级，支持 user_id / business_type / event_type / priority 维度。
//
// 在原有 per-user token bucket 基础上增加：
//   - business_type 维度限流（order / marketing / system）
//   - event_type 维度限流（order_status_changed / flash_sale_campaign）
//   - priority 维度限流（critical / high / normal / low）
//
// 策略：
//   - high / critical 被限流时走降级（Kafka fallback）而非直接拒绝
//   - normal / low 被限流时直接返回 ErrRateLimited
//   - 所有配置通过 RouteByUser 入口读取 BizEnvelope 后逐层检查
//   - user 维度采用 64-shard 分片锁，与 V1 RateLimiter 一致

package router

import (
	"errors"
	"sync"
	"time"
)

// ErrRateLimited is returned when a message is rejected by rate limiting.
var ErrRateLimited = errors.New("rate limited")

// DimConfig holds token-bucket parameters for one dimension key.
type DimConfig struct {
	Rate  float64 `json:"rate"`
	Burst float64 `json:"burst"`
}

// MultiDimConfig holds rate-limit configuration for all dimensions.
type MultiDimConfig struct {
	User         DimConfig            `json:"user"`
	BusinessType map[string]DimConfig `json:"business_type"`
	EventType    map[string]DimConfig `json:"event_type"`
	Priority     map[string]DimConfig `json:"priority"`
}

// DefaultMultiDimConfig returns a sensible default configuration.
func DefaultMultiDimConfig() MultiDimConfig {
	return MultiDimConfig{
		User: DimConfig{Rate: 100, Burst: 200},
		BusinessType: map[string]DimConfig{
			"order":     {Rate: 1000, Burst: 2000},
			"marketing": {Rate: 200, Burst: 500},
		},
		EventType: map[string]DimConfig{
			"order_status_changed": {Rate: 800, Burst: 1000},
			"flash_sale_campaign":  {Rate: 100, Burst: 200},
		},
		Priority: map[string]DimConfig{
			"critical": {Rate: 2000, Burst: 3000},
			"high":     {Rate: 1000, Burst: 2000},
			"normal":   {Rate: 500, Burst: 1000},
			"low":      {Rate: 100, Burst: 200},
		},
	}
}

const v2ShardCount = 64

type v2UserShard struct {
	mu      sync.Mutex
	buckets map[int64]*tokenBucket
}

// MultiDimRateLimiter enforces rate limits across multiple dimensions.
// User dimension uses 64-shard locking (matching V1 RateLimiter pattern).
// Business-type / event-type / priority use independent mutexes (low cardinality).
type MultiDimRateLimiter struct {
	cfg      MultiDimConfig
	shards   [v2ShardCount]v2UserShard
	bizMu    sync.Mutex
	bizTypes map[string]*tokenBucket
	evtMu    sync.Mutex
	evtTypes map[string]*tokenBucket
	prioMu   sync.Mutex
	prios    map[string]*tokenBucket
}

// NewMultiDimRateLimiter creates a multi-dimensional rate limiter.
func NewMultiDimRateLimiter(cfg MultiDimConfig) *MultiDimRateLimiter {
	if cfg.BusinessType == nil {
		cfg.BusinessType = make(map[string]DimConfig)
	}
	if cfg.EventType == nil {
		cfg.EventType = make(map[string]DimConfig)
	}
	if cfg.Priority == nil {
		cfg.Priority = make(map[string]DimConfig)
	}
	m := &MultiDimRateLimiter{
		cfg:      cfg,
		bizTypes: make(map[string]*tokenBucket),
		evtTypes: make(map[string]*tokenBucket),
		prios:    make(map[string]*tokenBucket),
	}
	for i := range m.shards {
		m.shards[i].buckets = make(map[int64]*tokenBucket)
	}
	return m
}

// AllowUser checks whether the user has remaining capacity.
func (m *MultiDimRateLimiter) AllowUser(uid int64) bool {
	s := &m.shards[v2ShardIndex(uid)]
	s.mu.Lock()
	defer s.mu.Unlock()
	tb, ok := s.buckets[uid]
	if !ok {
		tb = newBucket(m.cfg.User)
		s.buckets[uid] = tb
	}
	return tb.allow()
}

// AllowBusinessType checks whether the business type has remaining capacity.
func (m *MultiDimRateLimiter) AllowBusinessType(bizType string) bool {
	if bizType == "" {
		return true
	}
	cfg, ok := m.cfg.BusinessType[bizType]
	if !ok {
		return true
	}
	m.bizMu.Lock()
	defer m.bizMu.Unlock()
	tb, ok := m.bizTypes[bizType]
	if !ok {
		tb = newBucket(cfg)
		m.bizTypes[bizType] = tb
	}
	return tb.allow()
}

// AllowEventType checks whether the event type has remaining capacity.
func (m *MultiDimRateLimiter) AllowEventType(evtType string) bool {
	if evtType == "" {
		return true
	}
	cfg, ok := m.cfg.EventType[evtType]
	if !ok {
		return true
	}
	m.evtMu.Lock()
	defer m.evtMu.Unlock()
	tb, ok := m.evtTypes[evtType]
	if !ok {
		tb = newBucket(cfg)
		m.evtTypes[evtType] = tb
	}
	return tb.allow()
}

// AllowPriority checks whether the priority level has remaining capacity.
func (m *MultiDimRateLimiter) AllowPriority(priority string) bool {
	if priority == "" {
		priority = "normal"
	}
	cfg, ok := m.cfg.Priority[priority]
	if !ok {
		cfg = m.cfg.Priority["normal"]
	}
	m.prioMu.Lock()
	defer m.prioMu.Unlock()
	tb, ok := m.prios[priority]
	if !ok {
		tb = newBucket(cfg)
		m.prios[priority] = tb
	}
	return tb.allow()
}

// IsDegradable returns true if a priority can be downgraded (Kafka fallback) instead of rejected.
func IsDegradable(priority string) bool {
	return priority == "critical" || priority == "high"
}

// RateLimitResult describes which dimension blocked a request.
type RateLimitResult struct {
	Allowed   bool   `json:"allowed"`
	Dimension string `json:"dimension,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// CheckAll runs all dimension checks. Returns the first blocking result.
// Uses independent locks per dimension; no deadlock because locks are never nested.
func (m *MultiDimRateLimiter) CheckAll(uid int64, bizType, evtType, priority string) RateLimitResult {
	if !m.AllowUser(uid) {
		return RateLimitResult{Allowed: false, Dimension: "user", Reason: "user rate limit exceeded"}
	}
	if !m.AllowBusinessType(bizType) {
		return RateLimitResult{Allowed: false, Dimension: "business_type", Reason: bizType + " rate limit exceeded"}
	}
	if !m.AllowEventType(evtType) {
		return RateLimitResult{Allowed: false, Dimension: "event_type", Reason: evtType + " rate limit exceeded"}
	}
	if !m.AllowPriority(priority) {
		return RateLimitResult{Allowed: false, Dimension: "priority", Reason: priority + " rate limit exceeded"}
	}
	return RateLimitResult{Allowed: true}
}

func v2ShardIndex(uid int64) int64 {
	if uid < 0 {
		uid = -uid
	}
	return uid % v2ShardCount
}

func newBucket(cfg DimConfig) *tokenBucket {
	return &tokenBucket{rate: cfg.Rate, burst: cfg.Burst, tokens: cfg.Burst, lastTime: time.Now().UnixNano()}
}
