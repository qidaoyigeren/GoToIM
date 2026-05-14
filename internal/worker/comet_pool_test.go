package worker

import (
	"testing"

	"github.com/Terry-Mao/goim/api/comet"
)

func TestNewCometClientPool(t *testing.T) {
	p := NewCometClientPool()
	if p == nil {
		t.Fatal("NewCometClientPool returned nil")
	}
	if p.Len() != 0 {
		t.Errorf("Len() = %d, want 0", p.Len())
	}
}

func TestCometClientPoolSetConfig(t *testing.T) {
	p := NewCometClientPool()
	p.SetConfig(16, 512)
	if p.routineSize != 16 {
		t.Errorf("routineSize = %d, want 16", p.routineSize)
	}
	if p.routineChan != 512 {
		t.Errorf("routineChan = %d, want 512", p.routineChan)
	}
}

func TestCometClientPoolPushNotFound(t *testing.T) {
	p := NewCometClientPool()
	err := p.Push("nonexistent", &comet.PushMsgReq{
		Keys: []string{"key1"},
	})
	if err == nil {
		t.Error("Push to nonexistent server should return error")
	}
}

func TestCometClientPoolBroadcastEmpty(t *testing.T) {
	p := NewCometClientPool()
	// Broadcast on empty pool should not panic
	err := p.Broadcast(&comet.BroadcastReq{
		ProtoOp: 9,
	})
	if err != nil {
		t.Errorf("Broadcast on empty pool error: %v", err)
	}
}

func TestCometClientPoolBroadcastRoomEmpty(t *testing.T) {
	p := NewCometClientPool()
	err := p.BroadcastRoom(&comet.BroadcastRoomReq{
		RoomID: "live://1000",
	})
	if err != nil {
		t.Errorf("BroadcastRoom on empty pool error: %v", err)
	}
}

func TestCometClientPoolCloseEmpty(t *testing.T) {
	p := NewCometClientPool()
	// Should not panic
	p.Close()
	if p.Len() != 0 {
		t.Errorf("Len() after Close = %d, want 0", p.Len())
	}
}

func TestCometClientPoolUpdateEmpty(t *testing.T) {
	p := NewCometClientPool()
	p.SetConfig(4, 64)
	// Update with empty addrs - should not panic
	p.Update(map[string]string{})
	if p.Len() != 0 {
		t.Errorf("Len() = %d, want 0", p.Len())
	}
}

func TestCometClientPoolUpdateAddsServer(t *testing.T) {
	p := NewCometClientPool()
	p.SetConfig(4, 64)
	// Update with an addr - gRPC dial is lazy so the server will be registered
	// even if the remote is not reachable
	p.Update(map[string]string{
		"comet-1": "localhost:19999",
	})
	if p.Len() != 1 {
		t.Errorf("Len() = %d, want 1", p.Len())
	}
	// Push to the added server should work (goes to channel, not gRPC directly)
	// Update with empty should remove it
	p.Update(map[string]string{})
	if p.Len() != 0 {
		t.Errorf("Len() after removal = %d, want 0", p.Len())
	}
}
