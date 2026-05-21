package comet

import (
	"sync"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/pkg/bufio"
	"github.com/Terry-Mao/goim/pkg/ratelimit"
)

// Channel used by message pusher send msg to write goroutine.
type Channel struct {
	Room     *Room
	CliProto Ring
	signal   *PriorityQueue
	Writer   bufio.Writer
	Reader   bufio.Reader
	Next     *Channel
	Prev     *Channel
	limiter  *ratelimit.TokenBucket

	Mid      int64
	Key      string
	IP       string
	watchOps map[int32]struct{}
	mutex    sync.RWMutex
	done     chan struct{}
	doneOnce sync.Once
}

var channelPool = sync.Pool{
	New: func() any {
		return new(Channel)
	},
}

// NewChannel new a channel.
func NewChannel(cli, svr int) *Channel {
	return GetChannel(cli, svr)
}

func GetChannel(cli, svr int) *Channel {
	c := channelPool.Get().(*Channel)
	c.Reset(cli, svr)
	return c
}

func PutChannel(c *Channel) {
	if c == nil {
		return
	}
	c.Room = nil
	c.Next = nil
	c.Prev = nil
	c.Mid = 0
	c.Key = ""
	c.IP = ""
	c.limiter = nil
	c.Reader.ResetBuffer(nil, nil)
	c.Writer.ResetBuffer(nil, nil)
	channelPool.Put(c)
}

func (c *Channel) Reset(cli, svr int) {
	c.Room = nil
	c.CliProto.Reset(cli)
	if c.signal == nil {
		c.signal = NewPriorityQueue(svr, svr*8) // high: svr, normal: svr*8
	} else {
		c.signal.Reset(svr, svr*8)
	}
	c.Next = nil
	c.Prev = nil
	c.Mid = 0
	c.Key = ""
	c.IP = ""
	c.limiter = nil
	c.done = make(chan struct{})
	c.doneOnce = sync.Once{}
	c.mutex.Lock()
	if c.watchOps == nil {
		c.watchOps = make(map[int32]struct{})
	} else {
		for op := range c.watchOps {
			delete(c.watchOps, op)
		}
	}
	c.mutex.Unlock()
}

// Watch watch a operation.
func (c *Channel) Watch(accepts ...int32) {
	c.mutex.Lock()
	for _, op := range accepts {
		c.watchOps[op] = struct{}{}
	}
	c.mutex.Unlock()
}

// UnWatch unwatch an operation
func (c *Channel) UnWatch(accepts ...int32) {
	c.mutex.Lock()
	for _, op := range accepts {
		delete(c.watchOps, op)
	}
	c.mutex.Unlock()
}

// NeedPush verify if in watch.
func (c *Channel) NeedPush(op int32) bool {
	c.mutex.RLock()
	if _, ok := c.watchOps[op]; ok {
		c.mutex.RUnlock()
		return true
	}
	c.mutex.RUnlock()
	return false
}

// Push server push message.
func (c *Channel) Push(p *protocol.Proto) (err error) {
	return c.signal.Push(p)
}

// Ready check the channel ready or close?
func (c *Channel) Ready() *protocol.Proto {
	return c.signal.Pop()
}

// Signal send signal to the channel, protocol ready.
func (c *Channel) Signal() {
	c.signal.high <- protocol.ProtoReady
}

// SetRateLimiter sets a per-connection rate limiter.
func (c *Channel) SetRateLimiter(limiter *ratelimit.TokenBucket) {
	c.limiter = limiter
}

// AllowMessage checks if a message is allowed by the rate limiter.
// Returns true if no limiter is set or if tokens are available.
func (c *Channel) AllowMessage() bool {
	if c.limiter == nil {
		return true
	}
	return c.limiter.Allow()
}

// Close close the channel.
func (c *Channel) Close() {
	c.signal.high <- protocol.ProtoFinish
}

func (c *Channel) Done() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.done
}

func (c *Channel) markDone() {
	if c == nil || c.done == nil {
		return
	}
	c.doneOnce.Do(func() {
		close(c.done)
	})
}
