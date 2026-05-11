package worker

import (
	"context"
	"sync"
	"time"

	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/Terry-Mao/goim/internal/mq/kafka"
	"github.com/golang/protobuf/proto"

	log "github.com/Terry-Mao/goim/pkg/log"
)

// DeliveryWorker pulls messages from the MQ consumer group and
// dispatches them to Comet servers via gRPC. It replaces the
// legacy internal/job/ Kafka consumer with the MQ abstraction.
type DeliveryWorker struct {
	consumer mq.Consumer
	comets   *CometClientPool
	reporter *ACKReporter
	rooms    map[string]*RoomAggregator
	roomsMu  sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
}

// Config holds DeliveryWorker configuration.
type Config struct {
	Brokers       []string
	ConsumerGroup string
	Topics        []string
	RoutineChan   int // per-Comet channel buffer size (default 1024)
	RoutineSize   int // goroutines per Comet (default 32)
	RoomBatch     int // room message batch size (default 20)
	RoomSignal    time.Duration
}

// New creates a new DeliveryWorker.
func New(cfg Config) (*DeliveryWorker, error) {
	consumer, err := kafka.NewConsumer(cfg.Brokers, cfg.ConsumerGroup, cfg.Topics)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := &DeliveryWorker{
		consumer: consumer,
		comets:   NewCometClientPool(),
		reporter: &ACKReporter{},
		rooms:    make(map[string]*RoomAggregator),
		ctx:      ctx,
		cancel:   cancel,
	}
	if cfg.RoutineSize == 0 {
		cfg.RoutineSize = 32
	}
	if cfg.RoutineChan == 0 {
		cfg.RoutineChan = 1024
	}
	if cfg.RoomBatch == 0 {
		cfg.RoomBatch = 20
	}
	if cfg.RoomSignal == 0 {
		cfg.RoomSignal = time.Second
	}
	w.comets.SetConfig(cfg.RoutineSize, cfg.RoutineChan)
	return w, nil
}

// Start begins the consume-dispatch loop. Blocks until ctx is cancelled.
func (w *DeliveryWorker) Start() error {
	log.Info("delivery worker started")
	return w.consumer.Consume(w.ctx, w.processMessage)
}

// Close shuts down the worker.
func (w *DeliveryWorker) Close() error {
	w.cancel()
	w.comets.Close()
	w.roomsMu.Lock()
	for _, r := range w.rooms {
		r.Close()
	}
	w.rooms = nil
	w.roomsMu.Unlock()
	return w.consumer.Close()
}

// UpdateComets refreshes gRPC connections to Comet servers.
func (w *DeliveryWorker) UpdateComets(addrs map[string]string) {
	w.comets.Update(addrs)
}

// processMessage handles a single message from the MQ.
func (w *DeliveryWorker) processMessage(ctx context.Context, msg *mq.Message) error {
	pushMsg := new(pb.PushMsg)
	if err := proto.Unmarshal(msg.Value, pushMsg); err != nil {
		log.Errorf("proto.Unmarshal error(%v)", err)
		return nil // malformed message, don't retry
	}

	switch pushMsg.Type {
	case pb.PushMsg_PUSH:
		return w.pushKeys(pushMsg.Operation, pushMsg.Server, pushMsg.Keys, pushMsg.Msg)
	case pb.PushMsg_ROOM:
		w.getRoom(pushMsg.Room).Push(pushMsg.Operation, pushMsg.Msg)
		return nil
	case pb.PushMsg_BROADCAST:
		return w.broadcast(pushMsg.Operation, pushMsg.Msg, pushMsg.Speed)
	default:
		return nil
	}
}

func (w *DeliveryWorker) getRoom(roomID string) *RoomAggregator {
	w.roomsMu.RLock()
	r, ok := w.rooms[roomID]
	w.roomsMu.RUnlock()
	if !ok {
		w.roomsMu.Lock()
		if r, ok = w.rooms[roomID]; !ok {
			r = NewRoomAggregator(w, roomID, 20, time.Second)
			w.rooms[roomID] = r
		}
		w.roomsMu.Unlock()
	}
	return r
}
