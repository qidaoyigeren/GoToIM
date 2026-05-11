package worker

import (
	"sync"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/pkg/bytes"

	log "github.com/Terry-Mao/goim/pkg/log"
)

var roomReadyProto = new(protocol.Proto)

// RoomAggregator batches room messages and flushes them to Comet.
type RoomAggregator struct {
	w         *DeliveryWorker
	id        string
	proto     chan *protocol.Proto
	closeOnce sync.Once
}

// NewRoomAggregator creates a new room aggregator.
func NewRoomAggregator(w *DeliveryWorker, id string, batch int, signal time.Duration) *RoomAggregator {
	r := &RoomAggregator{
		w:     w,
		id:    id,
		proto: make(chan *protocol.Proto, batch*2),
	}
	go r.pushproc(batch, signal)
	return r
}

// Push adds a message to the room's batch buffer.
func (r *RoomAggregator) Push(op int32, msg []byte) {
	p := &protocol.Proto{
		Ver:  1,
		Op:   op,
		Body: msg,
	}
	select {
	case r.proto <- p:
	default:
	}
}

// Close stops the aggregator.
func (r *RoomAggregator) Close() {
	r.closeOnce.Do(func() {
		close(r.proto)
	})
}

func (r *RoomAggregator) pushproc(batch int, sigTime time.Duration) {
	var (
		n    int
		last time.Time
		p    *protocol.Proto
		buf  = bytes.NewWriterSize(int(protocol.MaxBodySize))
	)
	log.Infof("worker room:%s started", r.id)
	td := time.AfterFunc(sigTime, func() {
		select {
		case r.proto <- roomReadyProto:
		default:
		}
	})
	defer td.Stop()

	for {
		if p = <-r.proto; p == nil {
			break
		} else if p != roomReadyProto {
			p.WriteTo(buf)
			if n++; n == 1 {
				last = time.Now()
				td.Reset(sigTime)
				continue
			} else if n < batch {
				if sigTime > time.Since(last) {
					continue
				}
			}
		} else {
			if n == 0 {
				break
			}
		}
		_ = r.w.broadcastRoomRawBytes(r.id, buf.Buffer())
		buf = bytes.NewWriterSize(buf.Size())
		n = 0
		td.Reset(time.Minute)
	}
	log.Infof("worker room:%s exit", r.id)
}
