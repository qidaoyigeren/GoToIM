package comet

import (
	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/comet/errors"
)

// PriorityQueue is a two-level priority queue for channel signal messages.
// High priority: control messages (ACK, heartbeat reply, sync)
// Normal priority: regular push messages
type PriorityQueue struct {
	high   chan *protocol.Proto
	normal chan *protocol.Proto
}

// NewPriorityQueue creates a new PriorityQueue.
// highCap is the capacity for high-priority messages,
// normalCap is the capacity for normal messages.
func NewPriorityQueue(highCap, normalCap int) *PriorityQueue {
	return &PriorityQueue{
		high:   make(chan *protocol.Proto, highCap),
		normal: make(chan *protocol.Proto, normalCap),
	}
}

// isHighPriority returns true if the operation should be treated as high priority.
func isHighPriority(op int32) bool {
	switch op {
	case protocol.OpProtoReady, protocol.OpProtoFinish:
		// Internal control signals are always highest priority
		return true
	case protocol.OpHeartbeatReply:
		// Heartbeat replies should not be delayed
		return true
	case protocol.OpPushMsgAck, protocol.OpSyncReply:
		// ACK and sync responses are high priority
		return true
	case protocol.OpSendMsgAck:
		// Send confirmation is high priority
		return true
	default:
		return false
	}
}

// Push adds a proto to the queue. High-priority ops go to the high channel,
// normal ops go to the normal channel. Returns ErrSignalFullMsgDropped if both are full.
func (pq *PriorityQueue) Push(p *protocol.Proto) error {
	if isHighPriority(p.Op) {
		select {
		case pq.high <- p:
			return nil
		default:
			return errors.ErrSignalFullMsgDropped
		}
	}
	select {
	case pq.normal <- p:
		return nil
	default:
		return errors.ErrSignalFullMsgDropped
	}
}

// Pop removes and returns the highest-priority proto. Blocks until one is available.
// High-priority messages are always consumed first.
func (pq *PriorityQueue) Pop() *protocol.Proto {
	// First, try high priority non-blocking
	select {
	case p := <-pq.high:
		return p
	default:
	}
	// Then block on both
	select {
	case p := <-pq.high:
		return p
	case p := <-pq.normal:
		return p
	}
}

// Len returns the total number of queued messages.
func (pq *PriorityQueue) Len() int {
	return len(pq.high) + len(pq.normal)
}

// Close drains both channels and sends ProtoFinish.
func (pq *PriorityQueue) Close() {
	select {
	case pq.high <- protocol.ProtoFinish:
	default:
	}
}
