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

// MultiDimRateLimiter enforces rate limits across multiple dimensions.
type MultiDimRateLimiter struct {
	mu       sync.Mutex
	cfg      MultiDimConfig
	users    map[int64]*tokenBucket
	bizTypes map[string]*tokenBucket
	evtTypes map[string]*tokenBucket
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
	return &MultiDimRateLimiter{
		cfg:      cfg,
		users:    make(map[int64]*tokenBucket),
		bizTypes: make(map[string]*tokenBucket),
		evtTypes: make(map[string]*tokenBucket),
		prios:    make(map[string]*tokenBucket),
	}
}

// AllowUser checks whether the user has remaining capacity.
func (m *MultiDimRateLimiter) AllowUser(uid int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allowOrCreate(&m.users, uid, m.cfg.User)
}

// AllowBusinessType checks whether the business type has remaining capacity.
func (m *MultiDimRateLimiter) AllowBusinessType(bizType string) bool {
	if bizType == "" {
		return true
	}
	cfg, ok := m.cfg.BusinessType[bizType]
	if !ok {
		return true // no specific config, allow
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allowOrCreateStr(&m.bizTypes, bizType, cfg)
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allowOrCreateStr(&m.evtTypes, evtType, cfg)
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allowOrCreateStr(&m.prios, priority, cfg)
}

// IsDegradable returns true if a priority can be downgraded (Kafka fallback) instead of rejected.
func IsDegradable(priority string) bool {
	return priority == "critical" || priority == "high"
}

// allowOrCreate is a generic typed token-bucket checker.
func (m *MultiDimRateLimiter) allowOrCreate(buckets *map[int64]*tokenBucket, key int64, cfg DimConfig) bool {
	tb := (*buckets)[key]
	if tb == nil {
		tb = &tokenBucket{rate: cfg.Rate, burst: cfg.Burst, tokens: cfg.Burst, lastTime: time.Now().UnixNano()}
		(*buckets)[key] = tb
	}
	return tb.allow()
}

func (m *MultiDimRateLimiter) allowOrCreateStr(buckets *map[string]*tokenBucket, key string, cfg DimConfig) bool {
	tb := (*buckets)[key]
	if tb == nil {
		tb = &tokenBucket{rate: cfg.Rate, burst: cfg.Burst, tokens: cfg.Burst, lastTime: time.Now().UnixNano()}
		(*buckets)[key] = tb
	}
	return tb.allow()
}

// RateLimitResult describes which dimension blocked a request.
type RateLimitResult struct {
	Allowed   bool   `json:"allowed"`
	Dimension string `json:"dimension,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// CheckAll runs all dimension checks. Returns the first blocking result.
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
