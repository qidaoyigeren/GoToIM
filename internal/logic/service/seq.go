package service

import (
	"context"
	"sync"
)

const defaultSeqBlockSize int64 = 100

// seqRange is the local in-memory range reserved from Redis.
type seqRange struct {
	next int64
	max  int64
}

// SeqStore is the Redis-side primitive required by SeqAllocator.
type SeqStore interface {
	IncrUserSeqBy(ctx context.Context, uid int64, delta int64) (int64, error)
}

// SeqAllocator reserves per-user sequence numbers from Redis in batches and
// serves them from memory until the range is exhausted.
type SeqAllocator struct {
	store     SeqStore
	blockSize int64

	mu     sync.Mutex
	ranges map[int64]*seqRange
}

// NewSeqAllocator creates an allocator with the default Redis reservation size.
func NewSeqAllocator(store SeqStore) *SeqAllocator {
	return NewSeqAllocatorWithBlockSize(store, defaultSeqBlockSize)
}

// NewSeqAllocatorWithBlockSize creates an allocator with an explicit block size.
func NewSeqAllocatorWithBlockSize(store SeqStore, blockSize int64) *SeqAllocator {
	if blockSize <= 0 {
		blockSize = defaultSeqBlockSize
	}
	return &SeqAllocator{
		store:     store,
		blockSize: blockSize,
		ranges:    make(map[int64]*seqRange),
	}
}

// Allocate returns the next authoritative per-user sequence number.
func (a *SeqAllocator) Allocate(ctx context.Context, uid int64) (int64, error) {
	if a == nil || a.store == nil {
		return 0, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if r := a.ranges[uid]; r != nil && r.next <= r.max {
		seq := r.next
		r.next++
		return seq, nil
	}

	end, err := a.store.IncrUserSeqBy(ctx, uid, a.blockSize)
	if err != nil {
		return 0, err
	}
	start := end - a.blockSize + 1
	a.ranges[uid] = &seqRange{next: start + 1, max: end}
	return start, nil
}
