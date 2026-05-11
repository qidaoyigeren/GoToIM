package worker

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Terry-Mao/goim/api/comet"

	log "github.com/Terry-Mao/goim/pkg/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// CometClientPool manages gRPC connections to all Comet servers.
type CometClientPool struct {
	mu          sync.RWMutex
	servers     map[string]*cometClient
	routineSize int
	routineChan int
}

// cometClient wraps a gRPC connection to a single Comet server with fan-out channels.
type cometClient struct {
	serverID      string
	client        comet.CometClient
	grpcConn      *grpc.ClientConn
	pushChan      []chan *comet.PushMsgReq
	roomChan      []chan *comet.BroadcastRoomReq
	broadcastChan chan *comet.BroadcastReq
	pushChanNum   uint64
	roomChanNum   uint64

	ctx    context.Context
	cancel context.CancelFunc
}

// NewCometClientPool creates a new pool.
func NewCometClientPool() *CometClientPool {
	return &CometClientPool{
		servers: make(map[string]*cometClient),
	}
}

// SetConfig sets routine parameters (must be called before Update).
func (p *CometClientPool) SetConfig(routineSize, routineChan int) {
	p.routineSize = routineSize
	p.routineChan = routineChan
}

// Update refreshes connections from a map of hostname→grpcAddr.
func (p *CometClientPool) Update(addrs map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for hostname, addr := range addrs {
		if _, ok := p.servers[hostname]; ok {
			continue
		}
		c, err := newCometClient(hostname, addr, p.routineSize, p.routineChan)
		if err != nil {
			log.Errorf("comet pool: dial %s failed: %v", hostname, err)
			continue
		}
		p.servers[hostname] = c
	}

	for hostname, c := range p.servers {
		if _, ok := addrs[hostname]; !ok {
			c.close()
			delete(p.servers, hostname)
		}
	}
}

// Push sends a PushMsg to a specific Comet server.
func (p *CometClientPool) Push(serverID string, arg *comet.PushMsgReq) error {
	p.mu.RLock()
	c, ok := p.servers[serverID]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("comet server %s not found", serverID)
	}
	return c.push(arg)
}

// BroadcastRoom sends a room broadcast to all Comet servers.
func (p *CometClientPool) BroadcastRoom(arg *comet.BroadcastRoomReq) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, c := range p.servers {
		if err := c.broadcastRoom(arg); err != nil {
			log.Errorf("broadcastRoom to %s error(%v)", c.serverID, err)
		}
	}
	return nil
}

// Broadcast sends a global broadcast to all Comet servers.
func (p *CometClientPool) Broadcast(arg *comet.BroadcastReq) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, c := range p.servers {
		if err := c.broadcast(arg); err != nil {
			log.Errorf("broadcast to %s error(%v)", c.serverID, err)
		}
	}
	return nil
}

// Len returns the number of active Comet connections.
func (p *CometClientPool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.servers)
}

// Close closes all connections.
func (p *CometClientPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.servers {
		c.close()
	}
	p.servers = make(map[string]*cometClient)
}

func newCometClient(hostname, grpcAddr string, routineSize, routineChan int) (*cometClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithInitialWindowSize(1<<24),
		grpc.WithInitialConnWindowSize(1<<24),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1<<24)),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(1<<24)),
		grpc.WithBackoffMaxDelay(3*time.Second),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, err
	}

	cctx, ccancel := context.WithCancel(context.Background())
	c := &cometClient{
		serverID:      hostname,
		client:        comet.NewCometClient(conn),
		grpcConn:      conn,
		pushChan:      make([]chan *comet.PushMsgReq, routineSize),
		roomChan:      make([]chan *comet.BroadcastRoomReq, routineSize),
		broadcastChan: make(chan *comet.BroadcastReq, routineSize),
		ctx:           cctx,
		cancel:        ccancel,
	}

	for i := 0; i < routineSize; i++ {
		c.pushChan[i] = make(chan *comet.PushMsgReq, routineChan)
		c.roomChan[i] = make(chan *comet.BroadcastRoomReq, routineChan)
		go c.process(c.pushChan[i], c.roomChan[i], c.broadcastChan)
	}
	return c, nil
}

func (c *cometClient) push(arg *comet.PushMsgReq) error {
	idx := atomic.AddUint64(&c.pushChanNum, 1) % uint64(len(c.pushChan))
	select {
	case c.pushChan[idx] <- arg:
		return nil
	default:
		return fmt.Errorf("push chan full server:%s", c.serverID)
	}
}

func (c *cometClient) broadcastRoom(arg *comet.BroadcastRoomReq) error {
	idx := atomic.AddUint64(&c.roomChanNum, 1) % uint64(len(c.roomChan))
	select {
	case c.roomChan[idx] <- arg:
		return nil
	default:
		return fmt.Errorf("room chan full server:%s", c.serverID)
	}
}

func (c *cometClient) broadcast(arg *comet.BroadcastReq) error {
	select {
	case c.broadcastChan <- arg:
		return nil
	default:
		return fmt.Errorf("broadcast chan full server:%s", c.serverID)
	}
}

func (c *cometClient) process(pushCh chan *comet.PushMsgReq, roomCh chan *comet.BroadcastRoomReq, broadcastCh chan *comet.BroadcastReq) {
	for {
		select {
		case arg := <-broadcastCh:
			if _, err := c.client.Broadcast(c.ctx, &comet.BroadcastReq{
				Proto: arg.Proto, ProtoOp: arg.ProtoOp, Speed: arg.Speed,
			}); err != nil {
				log.Errorf("Broadcast(%s) server:%s error(%v)", arg, c.serverID, err)
			}
		case arg := <-roomCh:
			if _, err := c.client.BroadcastRoom(c.ctx, &comet.BroadcastRoomReq{
				RoomID: arg.RoomID, Proto: arg.Proto,
			}); err != nil {
				log.Errorf("BroadcastRoom(%s) server:%s error(%v)", arg, c.serverID, err)
			}
		case arg := <-pushCh:
			if _, err := c.client.PushMsg(c.ctx, &comet.PushMsgReq{
				Keys: arg.Keys, Proto: arg.Proto, ProtoOp: arg.ProtoOp,
			}); err != nil {
				log.Errorf("PushMsg(%s) server:%s error(%v)", arg, c.serverID, err)
			}
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *cometClient) close() {
	c.cancel()
	if c.grpcConn != nil {
		c.grpcConn.Close()
	}
}

// ensure imports used
var _ = url.Parse
