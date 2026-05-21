package main

import (
	"context"
	"flag"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	"github.com/Terry-Mao/goim/internal/mq"
	mqkafka "github.com/Terry-Mao/goim/internal/mq/kafka"
	"github.com/Terry-Mao/goim/internal/worker"
	"github.com/Terry-Mao/goim/internal/worker/conf"
	"github.com/bilibili/discovery/naming"
	"github.com/gomodule/redigo/redis"

	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/tracing"
	resolver "github.com/bilibili/discovery/naming/grpc"
)

const ver = "3.0.0"

func main() {
	flag.Parse()
	if err := conf.Init(); err != nil {
		panic(err)
	}
	log.Init(false)

	// Initialize distributed tracing (OTEL_EXPORTER_OTLP_ENDPOINT env var)
	tp, err := tracing.InitTracer(context.Background(), "goim-job", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		log.Errorf("tracing init error(%v)", err)
	}
	defer func() {
		if tp != nil {
			_ = tp.Shutdown(context.Background())
		}
	}()

	log.Infof("goim-job [version: %s env: %+v] start", ver, conf.Conf.Env)

	// grpc register naming
	dis := naming.New(conf.Conf.Discovery)
	resolver.Register(dis)

	cfg := conf.Conf

	// Create DLQ producer when a DLQ topic is configured
	var dlq mq.DLQProducer
	if cfg.Kafka.DLQTopic != "" {
		var dlqErr error
		dlq, dlqErr = mqkafka.NewDLQ(cfg.Kafka.Brokers, cfg.Kafka.DLQTopic)
		if dlqErr != nil {
			log.Fatalf("NewDLQ error(%v)", dlqErr)
		}
		log.Infof("dead-letter queue enabled: topic=%s", cfg.Kafka.DLQTopic)
	}

	// Create Redis-backed retry counter when Redis is configured
	var retryCounter mq.RetryCounter
	if cfg.Redis != nil && cfg.Redis.Addr != "" {
		pool := newRedisPool(cfg.Redis)
		retryCounter = dao.NewRedisRetryCounter(pool)
		log.Infof("retry counter enabled (redis: %s)", cfg.Redis.Addr)
	}

	var (
		workers       []*worker.DeliveryWorker
		retryProducer mq.Producer
	)
	onlineTopic := cfg.Kafka.OnlinePushTopic
	offlineTopic := cfg.Kafka.OfflinePushTopic
	if onlineTopic != "" && offlineTopic != "" && onlineTopic != offlineTopic {
		var prodErr error
		retryProducer, prodErr = mqkafka.NewProducer(cfg.Kafka.Brokers, cfg.Kafka.PushTopic, cfg.Kafka.RoomTopic, cfg.Kafka.AllTopic, cfg.Kafka.ACKTopic, cfg.Kafka.Topic)
		if prodErr != nil {
			log.Fatalf("retry producer error(%v)", prodErr)
		}
		onlineWorker, err := worker.New(worker.Config{
			Brokers:       cfg.Kafka.Brokers,
			ConsumerGroup: groupWithSuffix(cfg.Kafka.Group, "online"),
			Topics:        []string{onlineTopic},
			RoutineChan:   cfg.Comet.RoutineChan,
			RoutineSize:   cfg.Comet.RoutineSize,
			RoomBatch:     cfg.Room.Batch,
			RetryCounter:  retryCounter,
			RetryProducer: retryProducer,
			RetryToTopic:  offlineTopic,
		})
		if err != nil {
			log.Fatalf("online worker.New error(%v)", err)
		}
		offlineWorker, err := worker.New(worker.Config{
			Brokers:       cfg.Kafka.Brokers,
			ConsumerGroup: groupWithSuffix(cfg.Kafka.Group, "offline"),
			Topics:        collectNonEmptyTopics(offlineTopic, cfg.Kafka.RoomTopic, cfg.Kafka.AllTopic, cfg.Kafka.ACKTopic),
			RoutineChan:   cfg.Comet.RoutineChan,
			RoutineSize:   cfg.Comet.RoutineSize,
			RoomBatch:     cfg.Room.Batch,
			DLQ:           dlq,
			RetryCounter:  retryCounter,
		})
		if err != nil {
			log.Fatalf("offline worker.New error(%v)", err)
		}
		workers = append(workers, onlineWorker, offlineWorker)
		log.Infof("online/offline push topics enabled: online=%s offline=%s", onlineTopic, offlineTopic)
	} else {
		w, err := worker.New(worker.Config{
			Brokers:       cfg.Kafka.Brokers,
			ConsumerGroup: cfg.Kafka.Group,
			Topics:        collectTopics(cfg.Kafka),
			RoutineChan:   cfg.Comet.RoutineChan,
			RoutineSize:   cfg.Comet.RoutineSize,
			RoomBatch:     cfg.Room.Batch,
			DLQ:           dlq,
			RetryCounter:  retryCounter,
		})
		if err != nil {
			log.Fatalf("worker.New error(%v)", err)
		}
		workers = append(workers, w)
	}

	// Watch comet servers and feed to worker
	go watchComets(workers...)

	// Start consume-dispatch loop
	for _, w := range workers {
		w := w
		go func() {
			if err := w.Start(); err != nil {
				log.Errorf("worker.Start error(%v)", err)
			}
		}()
	}

	// Signal handling
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		s := <-c
		log.Infof("goim-job get a signal %s", s.String())
		switch s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			for _, w := range workers {
				w.Close()
			}
			if retryProducer != nil {
				retryProducer.Close()
			}
			log.Infof("goim-job [version: %s] exit", ver)
			log.Sync()
			return
		case syscall.SIGHUP:
		default:
			return
		}
	}
}

// watchComets watches the discovery service and updates workers' comet pools.
func watchComets(workers ...*worker.DeliveryWorker) {
	dis := naming.New(conf.Conf.Discovery)
	resolver := dis.Build("goim.comet")
	event := resolver.Watch()

	for {
		select {
		case _, ok := <-event:
			if !ok {
				return
			}
		case <-time.After(10 * time.Second):
			log.Warningf("watchComets: no event from discovery, retrying fetch...")
		}

		ins, ok := resolver.Fetch()
		if !ok {
			log.Warningf("watchComets: fetch returned no data")
			continue
		}

		addrs := make(map[string]string)
		for _, zone := range ins.Instances {
			for _, in := range zone {
				for _, addr := range in.Addrs {
					u, err := url.Parse(addr)
					if err == nil && u.Scheme == "grpc" {
						addrs[in.Hostname] = u.Host
					}
				}
			}
		}
		for _, w := range workers {
			if w != nil {
				w.UpdateComets(addrs)
			}
		}
		if len(addrs) == 0 {
			log.Warningf("watchComets: discovery returned instances but no grpc addresses found")
		} else {
			log.Infof("watchComets updated %d comet servers: %v", len(addrs), addrs)
		}
	}
}

func newRedisPool(c *conf.Redis) *redis.Pool {
	return &redis.Pool{
		MaxIdle:     c.Idle,
		MaxActive:   c.Active,
		IdleTimeout: time.Duration(c.IdleTimeout),
		Dial: func() (redis.Conn, error) {
			return redis.Dial(c.Network, c.Addr,
				redis.DialConnectTimeout(time.Duration(c.DialTimeout)),
				redis.DialReadTimeout(time.Duration(c.ReadTimeout)),
				redis.DialWriteTimeout(time.Duration(c.WriteTimeout)),
				redis.DialPassword(c.Auth),
			)
		},
	}
}

func collectTopics(c *conf.Kafka) []string {
	topics := collectNonEmptyTopics(c.Topic, c.PushTopic, c.OnlinePushTopic, c.OfflinePushTopic, c.RoomTopic, c.AllTopic, c.ACKTopic)
	if len(topics) == 0 && c.Topic != "" {
		topics = []string{c.Topic}
	}
	return topics
}

func collectNonEmptyTopics(candidates ...string) []string {
	seen := make(map[string]struct{})
	var topics []string
	for _, t := range candidates {
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		topics = append(topics, t)
	}
	return topics
}

func groupWithSuffix(group, suffix string) string {
	if group == "" {
		return "goim-job-" + suffix
	}
	return group + "-" + suffix
}
