package service

import (
	"context"
	"sync"
)

const defaultSeqBlockSize int64 = 100
const defaultSeqPrefetchRemaining int64 = 20
const defaultSeqPrefetchPercent int64 = 90

// seqRange is the local in-memory range reserved from Redis.
type seqRange struct {
	start int64
	next  int64
	max   int64
}

func (r *seqRange) remaining() int64 {
	if r == nil || r.next > r.max {
		return 0
	}
	return r.max - r.next + 1
}

// SeqStore is the Redis-side primitive required by SeqAllocator.
type SeqStore interface {
	IncrUserSeqBy(ctx context.Context, uid int64, delta int64) (int64, error)
}

type userSeqState struct {
	mu sync.Mutex

	current *seqRange
	next    *seqRange

	reserving   bool
	reserveDone chan struct{}
}

// SeqAllocator reserves per-user sequence numbers from Redis in batches and
// serves them from memory until the range is exhausted.
type SeqAllocator struct {
	store             SeqStore
	blockSize         int64
	prefetchRemaining int64
	prefetchPercent   int64

	mu     sync.Mutex
	states map[int64]*userSeqState
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
		store:             store,
		blockSize:         blockSize,
		prefetchRemaining: defaultPrefetchRemaining(blockSize),
		prefetchPercent:   defaultSeqPrefetchPercent,
		states:            make(map[int64]*userSeqState),
	}
}

// Allocate returns the next authoritative per-user sequence number.
func (a *SeqAllocator) Allocate(ctx context.Context, uid int64) (int64, error) {
	if a == nil || a.store == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	st := a.state(uid)
	for {
		st.mu.Lock()
		if seq, ok := a.allocateLocked(st, uid); ok {
			st.mu.Unlock()
			return seq, nil
		}

		if st.reserving {
			done := st.reserveDone
			st.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}

		done := make(chan struct{})
		st.reserving = true
		st.reserveDone = done
		st.mu.Unlock()

		r, err := a.reserveRange(ctx, uid)

		st.mu.Lock()
		if err == nil {
			st.current = r
		}
		st.reserving = false
		st.reserveDone = nil
		close(done)
		st.mu.Unlock()

		if err != nil {
			return 0, err
		}
	}
}

func (a *SeqAllocator) state(uid int64) *userSeqState {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.states[uid]
	if st == nil {
		st = &userSeqState{}
		a.states[uid] = st
	}
	return st
}

func (a *SeqAllocator) allocateLocked(st *userSeqState, uid int64) (int64, bool) {
	if st.current == nil || st.current.next > st.current.max {
		if st.next == nil {
			return 0, false
		}
		st.current = st.next
		st.next = nil
	}

	seq := st.current.next
	st.current.next++
	a.maybeStartPrefetchLocked(st, uid)
	return seq, true
}

func (a *SeqAllocator) reserveRange(ctx context.Context, uid int64) (*seqRange, error) {
	end, err := a.store.IncrUserSeqBy(ctx, uid, a.blockSize)
	if err != nil {
		return nil, err
	}
	start := end - a.blockSize + 1
	return &seqRange{start: start, next: start, max: end}, nil
}

func (a *SeqAllocator) maybeStartPrefetchLocked(st *userSeqState, uid int64) {
	if st.current == nil || st.next != nil || st.reserving || !a.shouldPrefetchLocked(st.current) {
		return
	}
	done := make(chan struct{})
	st.reserving = true
	st.reserveDone = done
	go func() {
		r, err := a.reserveRange(context.Background(), uid)
		st.mu.Lock()
		if err == nil && st.next == nil {
			st.next = r
		}
		st.reserving = false
		st.reserveDone = nil
		close(done)
		st.mu.Unlock()
	}()
}

func (a *SeqAllocator) shouldPrefetchLocked(r *seqRange) bool {
	used := r.next - r.start
	return r.remaining() <= a.prefetchRemaining || used*100 >= a.blockSize*a.prefetchPercent
}

func defaultPrefetchRemaining(blockSize int64) int64 {
	if blockSize <= defaultSeqPrefetchRemaining {
		remaining := blockSize / 10
		if remaining < 1 {
			return 1
		}
		return remaining
	}
	return defaultSeqPrefetchRemaining
}
