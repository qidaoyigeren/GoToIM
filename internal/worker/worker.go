package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/Terry-Mao/goim/internal/mq/kafka"
	"github.com/Terry-Mao/goim/internal/tracectx"
	"google.golang.org/protobuf/proto"

	log "github.com/Terry-Mao/goim/pkg/log"
)

// DeliveryWorker pulls messages from the MQ consumer group and
// dispatches them to Comet servers via gRPC. It replaces the
// legacy internal/job/ Kafka consumer with the MQ abstraction.
type DeliveryWorker struct {
	cfg          Config
	consumer     mq.Consumer
	comets       *CometClientPool
	reporter     *ACKReporter
	rooms        map[string]*RoomAggregator
	roomsMu      sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
	dlq          mq.DLQProducer  // optional: dead-letter queue for undeliverable messages
	retryCounter mq.RetryCounter // optional: tracks retry counts per message
}

// Config holds DeliveryWorker configuration.
type Config struct {
	Brokers       []string
	ConsumerGroup string
	Topics        []string
	RoutineChan   int             // per-Comet channel buffer size (default 1024)
	RoutineSize   int             // goroutines per Comet (default 32)
	RoomBatch     int             // room message batch size (default 20)
	RoomSignal    time.Duration   // batch flush signal interval (default 1s)
	RoomIdle      time.Duration   // idle timeout before room eviction (default 15m)
	WALDir        string          // directory for room WAL files (empty = disabled)
	DLQ           mq.DLQProducer  // optional: dead-letter queue
	RetryCounter  mq.RetryCounter // optional: retry count tracker (Redis-backed)
}

// New creates a new DeliveryWorker.
func New(cfg Config) (*DeliveryWorker, error) {
	consumer, err := kafka.NewConsumer(cfg.Brokers, cfg.ConsumerGroup, cfg.Topics)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := &DeliveryWorker{
		cfg:          cfg,
		consumer:     consumer,
		comets:       NewCometClientPool(),
		reporter:     &ACKReporter{},
		rooms:        make(map[string]*RoomAggregator),
		ctx:          ctx,
		cancel:       cancel,
		dlq:          cfg.DLQ,
		retryCounter: cfg.RetryCounter,
	}
	if cfg.DLQ != nil {
		consumer.SetDLQ(cfg.DLQ)
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
	ctx = tracectx.WithTraceID(ctx, tracectx.FromHeaders(msg.Headers))
	pushMsg := new(pb.PushMsg)
	if err := proto.Unmarshal(msg.Value, pushMsg); err != nil {
		log.Errorf("proto.Unmarshal error(%v)", err)
		return nil // malformed message, don't retry
	}

	switch pushMsg.Type {
	case pb.PushMsg_PUSH:
		return w.checkRetry(ctx, msg, w.pushKeys(pushMsg.Operation, pushMsg.Server, pushMsg.Keys, pushMsg.Msg))
	case pb.PushMsg_ROOM:
		return w.checkRetry(ctx, msg, w.getRoom(pushMsg.Room).Push(pushMsg.Operation, pushMsg.Msg))
	case pb.PushMsg_BROADCAST:
		return w.checkRetry(ctx, msg, w.broadcast(pushMsg.Operation, pushMsg.Msg, pushMsg.Speed))
	default:
		return nil
	}
}

// checkRetry increments the retry counter and returns a DeadLetterError if
// the maximum retry count has been exceeded. Returns the original error when
// the message should be retried.
func (w *DeliveryWorker) checkRetry(ctx context.Context, msg *mq.Message, pushErr error) error {
	if pushErr == nil {
		return nil
	}
	if w.retryCounter == nil {
		return pushErr
	}
	cnt, err := w.retryCounter.Incr(ctx, msg.Topic, msg.Partition, msg.Offset)
	if err != nil {
		log.Errorf("retry counter incr failed: topic=%s partition=%d offset=%d err=%v",
			msg.Topic, msg.Partition, msg.Offset, err)
		return pushErr
	}
	if cnt >= mq.MaxRetries {
		log.Warningf("message exceeded max retries (%d): topic=%s partition=%d offset=%d",
			cnt, msg.Topic, msg.Partition, msg.Offset)
		return &mq.DeadLetterError{
			Err:     pushErr,
			Reason:  fmt.Sprintf("max retries exceeded (%d)", cnt),
			Retries: cnt,
		}
	}
	log.Infof("message retry %d/%d: topic=%s partition=%d offset=%d",
		cnt, mq.MaxRetries, msg.Topic, msg.Partition, msg.Offset)
	return pushErr
}

func (w *DeliveryWorker) getRoom(roomID string) *RoomAggregator {
	w.roomsMu.RLock()
	r, ok := w.rooms[roomID]
	w.roomsMu.RUnlock()
	if !ok {
		w.roomsMu.Lock()
		if r, ok = w.rooms[roomID]; !ok {
			signal := w.cfg.RoomSignal
			if signal == 0 {
				signal = time.Second
			}
			r = NewRoomAggregator(w, roomID, w.cfg.RoomBatch, signal)
			w.rooms[roomID] = r
		}
		w.roomsMu.Unlock()
	}
	return r
}

// delRoom removes a room aggregator from the worker.
func (w *DeliveryWorker) delRoom(roomID string) {
	w.roomsMu.Lock()
	if r, ok := w.rooms[roomID]; ok {
		r.Close()
		delete(w.rooms, roomID)
	}
	w.roomsMu.Unlock()
}

// idleTimeout returns the configured room idle timeout.
func (w *DeliveryWorker) idleTimeout() time.Duration {
	return w.cfg.RoomIdle
}
