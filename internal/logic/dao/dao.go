package dao

import (
	"context"
	"time"

	sarama "github.com/IBM/sarama"
	"github.com/Terry-Mao/goim/internal/logic/conf"
	"github.com/Terry-Mao/goim/internal/mq"
	mqkafka "github.com/Terry-Mao/goim/internal/mq/kafka"
	"github.com/gomodule/redigo/redis"
)

// Dao dao.
type Dao struct {
	c           *conf.Config
	kafkaPub    sarama.SyncProducer
	mqProducer  mq.Producer // optional: MQ abstraction (nil when not configured)
	redis       *redis.Pool
	redisExpire int32
}

// New new a dao and return.
func New(c *conf.Config) *Dao {
	d := &Dao{
		c:           c,
		kafkaPub:    newKafkaPub(c.Kafka),
		redis:       newRedis(c.Redis),
		redisExpire: int32(time.Duration(c.Redis.Expire) / time.Second),
	}
	if c.MQ != nil {
		pub, err := mqkafka.NewProducer(c.MQ.Brokers, c.MQ.PushTopic, c.MQ.RoomTopic, c.MQ.AllTopic, c.MQ.ACKTopic, c.Kafka.Topic)
		if err != nil {
			panic(err)
		}
		d.mqProducer = pub
	}
	return d
}

func newKafkaPub(c *conf.Kafka) sarama.SyncProducer {
	kc := sarama.NewConfig()
	kc.Producer.RequiredAcks = sarama.WaitForAll // Wait for all in-sync replicas to ack the message
	kc.Producer.Retry.Max = 10                   // Retry up to 10 times to produce the message
	kc.Producer.Return.Successes = true
	pub, err := sarama.NewSyncProducer(c.Brokers, kc)
	if err != nil {
		panic(err)
	}
	return pub
}

func newRedis(c *conf.Redis) *redis.Pool {
	return &redis.Pool{
		MaxIdle:     c.Idle,
		MaxActive:   c.Active,
		IdleTimeout: time.Duration(c.IdleTimeout),
		Dial: func() (redis.Conn, error) {
			conn, err := redis.Dial(c.Network, c.Addr,
				redis.DialConnectTimeout(time.Duration(c.DialTimeout)),
				redis.DialReadTimeout(time.Duration(c.ReadTimeout)),
				redis.DialWriteTimeout(time.Duration(c.WriteTimeout)),
				redis.DialPassword(c.Auth),
			)
			if err != nil {
				return nil, err
			}
			return conn, nil
		},
	}
}

// Close close the resource.
func (d *Dao) Close() error {
	if d.kafkaPub != nil {
		d.kafkaPub.Close()
	}
	if d.mqProducer != nil {
		d.mqProducer.Close()
	}
	return d.redis.Close()
}

// Ping dao ping.
func (d *Dao) Ping(c context.Context) error {
	return d.pingRedis(c)
}

// MQProducer returns the MQ abstraction producer, or nil if not configured.
func (d *Dao) MQProducer() mq.Producer {
	return d.mqProducer
}
