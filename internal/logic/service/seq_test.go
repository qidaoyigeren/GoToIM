package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

type testSeqStore struct {
	mu       sync.Mutex
	seq      map[int64]int64
	calls    map[int64]int
	blockUID int64
	blocked  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newTestSeqStore() *testSeqStore {
	return &testSeqStore{
		seq:     make(map[int64]int64),
		calls:   make(map[int64]int),
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *testSeqStore) IncrUserSeqBy(ctx context.Context, uid int64, delta int64) (int64, error) {
	if uid == s.blockUID {
		s.once.Do(func() { close(s.blocked) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[uid]++
	s.seq[uid] += delta
	return s.seq[uid], nil
}

func (s *testSeqStore) callCount(uid int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[uid]
}

func TestSeqAllocatorDoesNotBlockOtherUsersDuringReserve(t *testing.T) {
	store := newTestSeqStore()
	store.blockUID = 1
	alloc := NewSeqAllocatorWithBlockSize(store, 100)

	errCh := make(chan error, 1)
	go func() {
		_, err := alloc.Allocate(context.Background(), 1)
		errCh <- err
	}()

	select {
	case <-store.blocked:
	case <-time.After(time.Second):
		t.Fatal("uid 1 reserve did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	seq, err := alloc.Allocate(ctx, 2)
	if err != nil {
		t.Fatalf("allocate uid 2: %v", err)
	}
	if seq != 1 {
		t.Fatalf("uid 2 seq = %d, want 1", seq)
	}

	close(store.release)
	if err := <-errCh; err != nil {
		t.Fatalf("allocate uid 1: %v", err)
	}
}

func TestSeqAllocatorPrefetchesAtLowWatermark(t *testing.T) {
	store := newTestSeqStore()
	alloc := NewSeqAllocatorWithBlockSize(store, 100)

	for i := int64(1); i <= 80; i++ {
		seq, err := alloc.Allocate(context.Background(), 7)
		if err != nil {
			t.Fatalf("allocate %d: %v", i, err)
		}
		if seq != i {
			t.Fatalf("seq = %d, want %d", seq, i)
		}
	}

	deadline := time.Now().Add(time.Second)
	for store.callCount(7) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls := store.callCount(7); calls != 2 {
		t.Fatalf("reserve calls = %d, want 2", calls)
	}

	for i := int64(81); i <= 101; i++ {
		seq, err := alloc.Allocate(context.Background(), 7)
		if err != nil {
			t.Fatalf("allocate %d: %v", i, err)
		}
		if seq != i {
			t.Fatalf("seq = %d, want %d", seq, i)
		}
	}
	if calls := store.callCount(7); calls != 2 {
		t.Fatalf("reserve calls after using prefetched range = %d, want 2", calls)
	}
}
