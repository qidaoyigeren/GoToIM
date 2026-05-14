package worker

import (
	"encoding/binary"
	"os"
	"path/filepath"
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

// NewRoomAggregator creates a new room aggregator. Replays any existing WAL.
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
		wal  *os.File
	)
	idleTimeout := r.w.idleTimeout()
	if idleTimeout == 0 {
		idleTimeout = 15 * time.Minute
	}

	log.Infof("worker room:%s started", r.id)

	// Replay any existing WAL
	if r.w.cfg.WALDir != "" {
		walPath := filepath.Join(r.w.cfg.WALDir, r.id+".wal")
		if data, err := os.ReadFile(walPath); err == nil && len(data) > 0 {
			log.Infof("worker room:%s replaying WAL (%d bytes)", r.id, len(data))
			_ = r.w.broadcastRoomRawBytes(r.id, data)
		}
		// Open WAL for appending
		f, err := os.OpenFile(walPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err == nil {
			wal = f
		}
	}

	signalTimer := time.AfterFunc(sigTime, func() {
		select {
		case r.proto <- roomReadyProto:
		default:
		}
	})
	defer signalTimer.Stop()

	idleTimer := time.NewTimer(idleTimeout)
	defer idleTimer.Stop()

	for {
		select {
		case p = <-r.proto:
		case <-idleTimer.C:
			// Idle timeout: flush any remaining, remove from worker, exit
			if n > 0 {
				data := buf.Buffer()
				_ = r.w.broadcastRoomRawBytes(r.id, data)
			}
			cleanWAL(wal)
			log.Infof("worker room:%s idle timeout, exit", r.id)
			r.w.delRoom(r.id)
			return
		}

		if p == nil {
			break
		} else if p != roomReadyProto {
			p.WriteTo(buf)
			if n++; n == 1 {
				last = time.Now()
				signalTimer.Reset(sigTime)
				idleTimer.Reset(idleTimeout)
				continue
			} else if n < batch {
				if sigTime > time.Since(last) {
					idleTimer.Reset(idleTimeout)
					continue
				}
			}
		} else {
			if n == 0 {
				break
			}
		}

		data := buf.Buffer()
		writeWAL(wal, data)
		_ = r.w.broadcastRoomRawBytes(r.id, data)
		cleanWAL(wal)
		buf = bytes.NewWriterSize(buf.Size())
		n = 0
		signalTimer.Reset(time.Minute)
		idleTimer.Reset(idleTimeout)
	}
	cleanWAL(wal)
	log.Infof("worker room:%s exit", r.id)
}

// writeWAL appends a batch to the write-ahead log.
func writeWAL(wal *os.File, data []byte) {
	if wal == nil {
		return
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	wal.Write(lenBuf[:])
	wal.Write(data)
	wal.Sync()
}

// cleanWAL truncates the WAL after a successful flush.
func cleanWAL(wal *os.File) {
	if wal == nil {
		return
	}
	wal.Truncate(0)
	wal.Seek(0, 0)
}
