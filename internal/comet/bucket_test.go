package comet

import (
	"testing"

	"github.com/Terry-Mao/goim/api/protocol"
)

func TestBucketBroadcastDoesNotSignalDroppedPush(t *testing.T) {
	b := &Bucket{
		chs: make(map[string]*Channel),
	}
	ch := NewChannel(1, 1)
	ch.Key = "test"
	ch.Watch(protocol.OpRaw)
	b.chs[ch.Key] = ch

	for i := 0; i < 8; i++ {
		if err := ch.Push(&protocol.Proto{Op: protocol.OpRaw}); err != nil {
			t.Fatalf("fill channel queue: %v", err)
		}
	}

	b.Broadcast(&protocol.Proto{Op: protocol.OpRaw}, protocol.OpRaw)

	if got := ch.signal.Len(); got != 8 {
		t.Fatalf("queued messages = %d, want 8; dropped broadcasts must not enqueue ready signals", got)
	}
}
