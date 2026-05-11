package main

import (
	"flag"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Terry-Mao/goim/internal/job"
	"github.com/Terry-Mao/goim/internal/job/conf"
	"github.com/Terry-Mao/goim/internal/worker"
	"github.com/bilibili/discovery/naming"

	log "github.com/Terry-Mao/goim/pkg/log"
	resolver "github.com/bilibili/discovery/naming/grpc"
)

var (
	ver       = "3.0.0"
	useWorker = flag.Bool("worker", true, "use DeliveryWorker instead of legacy Job")
)

func main() {
	flag.Parse()
	if err := conf.Init(); err != nil {
		panic(err)
	}
	log.Init(false)
	log.Infof("goim-job [version: %s env: %+v] start", ver, conf.Conf.Env)

	// grpc register naming
	dis := naming.New(conf.Conf.Discovery)
	resolver.Register(dis)

	if *useWorker {
		runWorker()
	} else {
		runLegacyJob()
	}
}

func runWorker() {
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

func runLegacyJob() {
	j := job.New(conf.Conf)
	go j.Consume()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		s := <-c
		log.Infof("goim-job get a signal %s", s.String())
		switch s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			j.Close()
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
			log.Error("watchComets init timeout")
		}

		ins, ok := resolver.Fetch()
		if !ok {
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
		log.Infof("watchComets updated %d comet servers", len(addrs))
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
