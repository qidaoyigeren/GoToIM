package comet

import (
	"sync"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/comet/errors"
)

func TestPriorityQueue_PushPop(t *testing.T) {
	pq := NewPriorityQueue(4, 8)

	p := &protocol.Proto{Op: protocol.OpRaw, Body: []byte("test")}
	if err := pq.Push(p); err != nil {
		t.Fatalf("Push: %v", err)
	}

	got := pq.Pop()
	if got != p {
		t.Error("Pop returned different proto than pushed")
	}
}

func TestPriorityQueue_HighPriorityFirst(t *testing.T) {
	pq := NewPriorityQueue(4, 8)

	// Push a normal message first
	normal := &protocol.Proto{Op: protocol.OpRaw, Body: []byte("normal")}
	if err := pq.Push(normal); err != nil {
		t.Fatalf("Push normal: %v", err)
	}

	// Push a high-priority message
	high := &protocol.Proto{Op: protocol.OpHeartbeatReply, Body: []byte("high")}
	if err := pq.Push(high); err != nil {
		t.Fatalf("Push high: %v", err)
	}

	// Pop should return high-priority first
	got := pq.Pop()
	if got != high {
		t.Errorf("first Pop should return high-priority message, got op=%d", got.Op)
	}

	got = pq.Pop()
	if got != normal {
		t.Errorf("second Pop should return normal message, got op=%d", got.Op)
	}
}

func TestPriorityQueue_FullHighDrops(t *testing.T) {
	pq := NewPriorityQueue(2, 8)

	// Fill high channel
	for i := 0; i < 2; i++ {
		p := &protocol.Proto{Op: protocol.OpProtoReady}
		if err := pq.Push(p); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}

	// Next high-priority push should fail
	p := &protocol.Proto{Op: protocol.OpProtoReady}
	err := pq.Push(p)
	if err != errors.ErrSignalFullMsgDropped {
		t.Errorf("expected ErrSignalFullMsgDropped, got %v", err)
	}
}

func TestPriorityQueue_FullNormalDrops(t *testing.T) {
	pq := NewPriorityQueue(4, 2)

	// Fill normal channel
	for i := 0; i < 2; i++ {
		p := &protocol.Proto{Op: protocol.OpRaw}
		if err := pq.Push(p); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}

	// Next normal push should fail
	p := &protocol.Proto{Op: protocol.OpRaw}
	err := pq.Push(p)
	if err != errors.ErrSignalFullMsgDropped {
		t.Errorf("expected ErrSignalFullMsgDropped, got %v", err)
	}
}

func TestPriorityQueue_Len(t *testing.T) {
	pq := NewPriorityQueue(4, 8)

	if pq.Len() != 0 {
		t.Errorf("Len = %d, want 0", pq.Len())
	}

	pq.Push(&protocol.Proto{Op: protocol.OpProtoReady})     // high
	pq.Push(&protocol.Proto{Op: protocol.OpRaw})            // normal
	pq.Push(&protocol.Proto{Op: protocol.OpHeartbeatReply}) // high
	pq.Push(&protocol.Proto{Op: protocol.OpRaw})            // normal

	if pq.Len() != 4 {
		t.Errorf("Len = %d, want 4", pq.Len())
	}
}

func TestPriorityQueue_EmptyPopBlocks(t *testing.T) {
	pq := NewPriorityQueue(4, 8)

	done := make(chan struct{})
	go func() {
		_ = pq.Pop() // should block
		close(done)
	}()

	select {
	case <-done:
		t.Error("Pop should block on empty queue")
	case <-time.After(50 * time.Millisecond):
		// expected: Pop is blocking
	}

	// Push to unblock
	pq.Push(&protocol.Proto{Op: protocol.OpRaw})

	select {
	case <-done:
		// Pop unblocked
	case <-time.After(time.Second):
		t.Error("Pop did not unblock after push")
	}
}

func TestIsHighPriority(t *testing.T) {
	highOps := []int32{
		protocol.OpProtoReady,
		protocol.OpProtoFinish,
		protocol.OpHeartbeatReply,
		protocol.OpPushMsgAck,
		protocol.OpSyncReply,
		protocol.OpSendMsgAck,
	}
	for _, op := range highOps {
		if !isHighPriority(op) {
			t.Errorf("op %d should be high priority", op)
		}
	}

	normalOps := []int32{
		protocol.OpHandshake,
		protocol.OpHeartbeat,
		protocol.OpSendMsg,
		protocol.OpRaw,
		protocol.OpAuth,
		protocol.OpChangeRoom,
	}
	for _, op := range normalOps {
		if isHighPriority(op) {
			t.Errorf("op %d should NOT be high priority", op)
		}
	}
}

func TestPriorityQueue_ConcurrentPushPop(t *testing.T) {
	pq := NewPriorityQueue(100, 200)
	const n = 50

	var wg sync.WaitGroup

	// Producers
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int32) {
			defer wg.Done()
			p := &protocol.Proto{Op: protocol.OpRaw}
			pq.Push(p)
		}(int32(i))
	}

	// Consumers
	results := make(chan *protocol.Proto, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := pq.Pop()
			results <- p
		}()
	}

	wg.Wait()
	close(results)

	count := 0
	for range results {
		count++
	}
	if count != n {
		t.Errorf("consumed %d messages, want %d", count, n)
	}
}
