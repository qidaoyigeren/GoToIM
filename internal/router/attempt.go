package router

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/gomodule/redigo/redis"
)

// DeliveryAttempt records one delivery try for a message through a specific channel.
type DeliveryAttempt struct {
	MsgID     string    `json:"msg_id"`
	AttemptNo int       `json:"attempt_no"`
	Channel   string    `json:"channel"` // grpc_direct / kafka_fallback / offline_stored
	Status    string    `json:"status"`  // success / failed
	LatencyMS int64     `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
	Server    string    `json:"server,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Redis key prefixes for attempt tracking.
const (
	_prefixAttemptSeq = "attempt_seq:%s" // msg_id -> monotonic attempt number (INCR)
	_prefixAttempt    = "attempt:%s:%d"  // msg_id:attempt_no -> attempt JSON
	_attemptTTL       = 7 * 24 * 3600    // 7 days
)

func keyAttemptSeq(msgID string) string {
	return fmt.Sprintf(_prefixAttemptSeq, msgID)
}

func keyAttempt(msgID string, attemptNo int) string {
	return fmt.Sprintf(_prefixAttempt, msgID, attemptNo)
}

// AttemptRecorder writes delivery attempt records to Redis without
// affecting the main dispatch flow (failures are logged only).
type AttemptRecorder struct {
	pool *redis.Pool
}

// NewAttemptRecorder creates an AttemptRecorder backed by the given Redis pool.
// Pass nil to create a no-op recorder (useful in tests).
func NewAttemptRecorder(pool *redis.Pool) *AttemptRecorder {
	return &AttemptRecorder{pool: pool}
}

// nextAttemptNo atomically increments and returns the next attempt number for a msgID.
func (r *AttemptRecorder) nextAttemptNo(ctx context.Context, msgID string) (int, error) {
	if r.pool == nil {
		return 1, nil
	}
	conn := r.pool.Get()
	defer conn.Close()
	n, err := redis.Int(conn.Do("INCR", keyAttemptSeq(msgID)))
	if err != nil {
		return 0, err
	}
	// Set TTL on the sequence key (best-effort)
	if _, err := conn.Do("EXPIRE", keyAttemptSeq(msgID), _attemptTTL); err != nil {
		log.Warningf("attempt seq EXPIRE failed for msg_id=%s: %v", msgID, err)
	}
	return n, nil
}

// Record writes a delivery attempt to Redis.
// Returns silently on failure — attempt recording must not block dispatching.
func (r *AttemptRecorder) Record(ctx context.Context, msgID, channel, status string, latencyMs int64, errStr, server string) {
	if r.pool == nil || msgID == "" {
		return
	}
	attemptNo, noErr := r.nextAttemptNo(ctx, msgID)
	if noErr != nil {
		log.Warningf("attempt seq failed for msg_id=%s: %v", msgID, noErr)
		return
	}
	attempt := DeliveryAttempt{
		MsgID:     msgID,
		AttemptNo: attemptNo,
		Channel:   channel,
		Status:    status,
		LatencyMS: latencyMs,
		Error:     errStr,
		Server:    server,
		CreatedAt: time.Now(),
	}
	data, marshalErr := json.Marshal(attempt)
	if marshalErr != nil {
		log.Warningf("attempt marshal failed for msg_id=%s: %v", msgID, marshalErr)
		return
	}
	conn := r.pool.Get()
	defer conn.Close()
	key := keyAttempt(msgID, attemptNo)
	if _, err := conn.Do("SET", key, data, "EX", _attemptTTL); err != nil {
		log.Warningf("attempt record failed for %s: %v", key, err)
	}
}
