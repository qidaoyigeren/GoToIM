package dao

import (
	"context"
	"fmt"

	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/gomodule/redigo/redis"
)

const _prefixRetryCnt = "retry_cnt:%s:%d:%d" // topic:partition:offset -> retry count

var retryCounterIncrScript = redis.NewScript(1, `
local n = redis.call("INCR", KEYS[1])
redis.call("EXPIRE", KEYS[1], ARGV[1])
return n
`)

// Ensure RedisRetryCounter implements mq.RetryCounter at compile time.
var _ mq.RetryCounter = (*RedisRetryCounter)(nil)

// RedisRetryCounter implements mq.RetryCounter backed by a Redis pool.
// The counter keys auto-expire after 1 hour.
type RedisRetryCounter struct {
	pool *redis.Pool
}

// NewRedisRetryCounter creates a retry counter backed by the given Redis pool.
func NewRedisRetryCounter(pool *redis.Pool) *RedisRetryCounter {
	return &RedisRetryCounter{pool: pool}
}

// Incr increments and returns the retry count for a Kafka message identified
// by its topic, partition, and offset.
func (r *RedisRetryCounter) Incr(ctx context.Context, topic string, partition int32, offset int64) (int64, error) {
	conn := r.pool.Get()
	defer conn.Close()
	key := fmt.Sprintf(_prefixRetryCnt, topic, partition, offset)
	return redis.Int64(retryCounterIncrScript.Do(conn, key, 3600))
}
