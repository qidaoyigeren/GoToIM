package service

import (
	"sync"
	"time"
)

const (
	onlineRouterShards = 64
	onlineRouterTTL    = 10 * time.Minute
)

type userNode struct {
	uid       int64
	key       string
	server    string
	updatedAt time.Time
}

type onlineRouterShard struct {
	mu    sync.RWMutex
	nodes map[int64]*userNode
}

// OnlineRouter is a local in-memory uid -> comet node cache.
type OnlineRouter struct {
	shards [onlineRouterShards]onlineRouterShard
	ttl    time.Duration
}

func NewOnlineRouter() *OnlineRouter {
	r := &OnlineRouter{ttl: onlineRouterTTL}
	for i := range r.shards {
		r.shards[i].nodes = make(map[int64]*userNode)
	}
	return r
}

// Set records the latest locally known route for a user.
func (r *OnlineRouter) Set(uid int64, key, server string) {
	if r == nil || uid <= 0 || key == "" || server == "" {
		return
	}
	s := r.shard(uid)
	s.mu.Lock()
	s.nodes[uid] = &userNode{uid: uid, key: key, server: server, updatedAt: time.Now()}
	s.mu.Unlock()
}

// Get returns a synthetic Session for the cached route. Entries older than
// ten minutes are treated as expired and removed.
func (r *OnlineRouter) Get(uid int64) (*Session, bool) {
	if r == nil || uid <= 0 {
		return nil, false
	}
	s := r.shard(uid)
	s.mu.RLock()
	n := s.nodes[uid]
	if n == nil {
		s.mu.RUnlock()
		return nil, false
	}
	expired := time.Since(n.updatedAt) > r.ttl
	if !expired {
		sess := &Session{UID: n.uid, Key: n.key, Server: n.server}
		s.mu.RUnlock()
		return sess, true
	}
	s.mu.RUnlock()

	s.mu.Lock()
	if current := s.nodes[uid]; current != nil && time.Since(current.updatedAt) > r.ttl {
		delete(s.nodes, uid)
	}
	s.mu.Unlock()
	return nil, false
}

func (r *OnlineRouter) Delete(uid int64) {
	if r == nil || uid <= 0 {
		return
	}
	s := r.shard(uid)
	s.mu.Lock()
	delete(s.nodes, uid)
	s.mu.Unlock()
}

func (r *OnlineRouter) DeleteByServer(server string) {
	if r == nil || server == "" {
		return
	}
	for i := range r.shards {
		s := &r.shards[i]
		s.mu.Lock()
		for uid, n := range s.nodes {
			if n != nil && n.server == server {
				delete(s.nodes, uid)
			}
		}
		s.mu.Unlock()
	}
}

func (r *OnlineRouter) shard(uid int64) *onlineRouterShard {
	return &r.shards[uint64(uid)%onlineRouterShards]
}
