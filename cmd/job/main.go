package main

import (
	"context"
	"flag"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Terry-Mao/goim/internal/worker"
	"github.com/Terry-Mao/goim/internal/worker/conf"
	"github.com/bilibili/discovery/naming"

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
	topics := collectTopics(cfg.Kafka)
	w, err := worker.New(worker.Config{
		Brokers:       cfg.Kafka.Brokers,
		ConsumerGroup: cfg.Kafka.Group,
		Topics:        topics,
		RoutineChan:   cfg.Comet.RoutineChan,
		RoutineSize:   cfg.Comet.RoutineSize,
		RoomBatch:     cfg.Room.Batch,
	})
	if err != nil {
		log.Fatalf("worker.New error(%v)", err)
	}

	// Watch comet servers and feed to worker
	go watchComets(w)

	// Start consume-dispatch loop
	go func() {
		if err := w.Start(); err != nil {
			log.Errorf("worker.Start error(%v)", err)
		}
	}()

	// Signal handling
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		s := <-c
		log.Infof("goim-job get a signal %s", s.String())
		switch s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			w.Close()
			log.Infof("goim-job [version: %s] exit", ver)
			log.Sync()
			return
		case syscall.SIGHUP:
		default:
			return
		}
	}
}

// watchComets watches the discovery service and updates the worker's comet pool.
func watchComets(w *worker.DeliveryWorker) {
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
		w.UpdateComets(addrs)
		if len(addrs) == 0 {
			log.Warningf("watchComets: discovery returned instances but no grpc addresses found")
		} else {
			log.Infof("watchComets updated %d comet servers: %v", len(addrs), addrs)
		}
	}
}

func collectTopics(c *conf.Kafka) []string {
	seen := make(map[string]struct{})
	var topics []string
	for _, t := range []string{c.Topic, c.PushTopic, c.RoomTopic, c.AllTopic, c.ACKTopic} {
		if t != "" {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				topics = append(topics, t)
			}
		}
	}
	if len(topics) == 0 && c.Topic != "" {
		topics = []string{c.Topic}
	}
	return topics
}
