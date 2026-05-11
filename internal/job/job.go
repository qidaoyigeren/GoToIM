package job

import (
	"context"
	"fmt"
	"sync"
	"time"

	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/job/conf"
	"github.com/bilibili/discovery/naming"
	"github.com/golang/protobuf/proto"

	log "github.com/Terry-Mao/goim/pkg/log"
	cluster "github.com/bsm/sarama-cluster"
)

// Job is push job.
type Job struct {
	c            *conf.Config
	consumer     *cluster.Consumer
	cometServers map[string]*Comet
	cometsMutex  sync.RWMutex

	rooms      map[string]*Room
	roomsMutex sync.RWMutex
}

// New new a push job.
func New(c *conf.Config) *Job {
	j := &Job{
		c:        c,
		consumer: newKafkaSub(c.Kafka),
		rooms:    make(map[string]*Room),
	}
	j.watchComet(c.Discovery)
	return j
}

func newKafkaSub(c *conf.Kafka) *cluster.Consumer {
	topics := collectTopics(c)
	config := cluster.NewConfig()
	config.Consumer.Return.Errors = true
	config.Group.Return.Notifications = true
	consumer, err := cluster.NewConsumer(c.Brokers, c.Group, topics, config)
	if err != nil {
		log.Fatalf("newKafkaSub failed: %v", err)
	}
	return consumer
}

// collectTopics gathers all configured topics with deduplication.
func collectTopics(c *conf.Kafka) []string {
	seen := make(map[string]struct{})
	var topics []string
	addTopic := func(t string) {
		if t == "" {
			return
		}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		topics = append(topics, t)
	}
	addTopic(c.Topic)
	addTopic(c.PushTopic)
	addTopic(c.RoomTopic)
	addTopic(c.AllTopic)
	addTopic(c.ACKTopic)
	if len(topics) == 0 {
		topics = []string{c.Topic}
	}
	return topics
}

// Close close resounces.
func (j *Job) Close() error {
	j.cometsMutex.Lock()
	for _, c := range j.cometServers {
		c.Close()
	}
	j.cometServers = nil
	j.cometsMutex.Unlock()

	// Collect rooms under lock, then close channels outside lock
	// to avoid deadlock: pushproc → delRoom tries to acquire roomsMutex.
	j.roomsMutex.Lock()
	rooms := make([]*Room, 0, len(j.rooms))
	for _, r := range j.rooms {
		rooms = append(rooms, r)
	}
	j.rooms = nil
	j.roomsMutex.Unlock()

	for _, r := range rooms {
		r.CloseProto()
	}

	if j.consumer != nil {
		return j.consumer.Close()
	}
	return nil
}

// Consume messages, watch signals
func (j *Job) Consume() {
	for {
		select {
		case err := <-j.consumer.Errors():
			log.Errorf("consumer error(%v)", err)
		case n := <-j.consumer.Notifications():
			log.Infof("consumer rebalanced(%v)", n)
		case msg, ok := <-j.consumer.Messages():
			if !ok {
				return
			}
			// process push message first, then mark offset
			// This ensures at-least-once delivery: if crash after process but before mark,
			// the message will be re-delivered (idempotent push handles duplicates)
			pushMsg := new(pb.PushMsg)
			if err := proto.Unmarshal(msg.Value, pushMsg); err != nil {
				log.Errorf("proto.Unmarshal(%v) error(%v)", msg, err)
				j.consumer.MarkOffset(msg, "")
				continue
			}
			pushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if pushErr := j.push(pushCtx, pushMsg); pushErr != nil {
				cancel()
				log.Errorf("j.push(%v) error(%v)", pushMsg, pushErr)
				// Don't mark offset on failure - message will be re-consumed
				continue
			}
			cancel()
			// Mark offset only after successful processing
			j.consumer.MarkOffset(msg, "")
			log.Infof("consume: %s/%d/%d\t%s\t%+v", msg.Topic, msg.Partition, msg.Offset, msg.Key, pushMsg)
		}
	}
}

func (j *Job) watchComet(c *naming.Config) {
	dis := naming.New(c)
	resolver := dis.Build("goim.comet")
	event := resolver.Watch()
	select {
	case _, ok := <-event:
		if !ok {
			log.Fatal("watchComet init failed")
		}
		if ins, ok := resolver.Fetch(); ok {
			if err := j.newAddress(ins.Instances); err != nil {
				log.Fatalf("watchComet init newAddress error(%v)", err)
			}
			log.Infof("watchComet init newAddress:%+v", ins)
		}
	case <-time.After(10 * time.Second):
		log.Error("watchComet init instances timeout")
	}
	go func() {
		for {
			if _, ok := <-event; !ok {
				log.Info("watchComet exit")
				return
			}
			ins, ok := resolver.Fetch()
			if ok {
				if err := j.newAddress(ins.Instances); err != nil {
					log.Errorf("watchComet newAddress(%+v) error(%+v)", ins, err)
					continue
				}
				log.Infof("watchComet change newAddress:%+v", ins)
			}
		}
	}()
}

func (j *Job) newAddress(insMap map[string][]*naming.Instance) error {
	ins := insMap[j.c.Env.Zone]
	if len(ins) == 0 {
		return fmt.Errorf("watchComet instance is empty")
	}
	j.cometsMutex.RLock()
	oldServers := j.cometServers
	j.cometsMutex.RUnlock()
	comets := map[string]*Comet{}
	for _, in := range ins {
		if old, ok := oldServers[in.Hostname]; ok {
			comets[in.Hostname] = old
			continue
		}
		c, err := NewComet(in, j.c.Comet)
		if err != nil {
			log.Errorf("watchComet NewComet(%+v) error(%v)", in, err)
			return err
		}
		comets[in.Hostname] = c
		log.Infof("watchComet AddComet grpc:%+v", in)
	}
	for key, old := range oldServers {
		if _, ok := comets[key]; !ok {
			old.cancel()
			log.Infof("watchComet DelComet:%s", key)
		}
	}
	j.cometsMutex.Lock()
	j.cometServers = comets
	j.cometsMutex.Unlock()
	return nil
}
