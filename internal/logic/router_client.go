package logic

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	routerpb "github.com/Terry-Mao/goim/api/router"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/bilibili/discovery/naming"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// RouterClientPool maintains Logic-owned gRPC connections to Router nodes.
type RouterClientPool struct {
	mu      sync.RWMutex
	clients map[string]routerpb.RouterClient
	conns   map[string]*grpc.ClientConn
	order   []string
	next    uint64
}

// NewRouterClientPool creates an empty Router client pool.
func NewRouterClientPool() *RouterClientPool {
	return &RouterClientPool{
		clients: make(map[string]routerpb.RouterClient),
		conns:   make(map[string]*grpc.ClientConn),
	}
}

var _ routerpb.RouterClient = (*RouterClientPool)(nil)

// UpdateNodes refreshes Router clients from service discovery instances.
func (p *RouterClientPool) UpdateNodes(nodes []*naming.Instance) {
	newClients := make(map[string]routerpb.RouterClient, len(nodes))
	newConns := make(map[string]*grpc.ClientConn, len(nodes))
	newOrder := make([]string, 0, len(nodes))

	p.mu.RLock()
	for _, node := range nodes {
		if client, ok := p.clients[node.Hostname]; ok {
			newClients[node.Hostname] = client
			newConns[node.Hostname] = p.conns[node.Hostname]
			newOrder = append(newOrder, node.Hostname)
		}
	}
	p.mu.RUnlock()

	for _, node := range nodes {
		if _, ok := newClients[node.Hostname]; ok {
			continue
		}
		addr := grpcAddress(node)
		if addr == "" {
			log.Errorf("router node %s has no grpc address: %v", node.Hostname, node.Addrs)
			continue
		}
		conn, client, err := dialRouterClient(addr)
		if err != nil {
			log.Errorf("dial router %s(%s) error(%v)", node.Hostname, addr, err)
			continue
		}
		newClients[node.Hostname] = client
		newConns[node.Hostname] = conn
		newOrder = append(newOrder, node.Hostname)
		log.Infof("router client connected to %s (%s)", node.Hostname, addr)
	}

	p.mu.Lock()
	oldConns := p.conns
	p.clients = newClients
	p.conns = newConns
	p.order = newOrder
	p.mu.Unlock()

	for hostname, conn := range oldConns {
		if _, ok := newClients[hostname]; !ok {
			conn.Close()
			log.Infof("router client disconnected from %s", hostname)
		}
	}
}

// Close closes all Router connections.
func (p *RouterClientPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for hostname, conn := range p.conns {
		conn.Close()
		delete(p.clients, hostname)
	}
	p.conns = make(map[string]*grpc.ClientConn)
	p.order = nil
}

func (p *RouterClientPool) pick() (routerpb.RouterClient, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.order) == 0 {
		return nil, fmt.Errorf("router client has no available nodes")
	}
	idx := atomic.AddUint64(&p.next, 1)
	hostname := p.order[int((idx-1)%uint64(len(p.order)))]
	client := p.clients[hostname]
	if client == nil {
		return nil, fmt.Errorf("router client %s not found", hostname)
	}
	return client, nil
}

func (p *RouterClientPool) RouteByUser(ctx context.Context, in *routerpb.RouteByUserReq, opts ...grpc.CallOption) (*routerpb.RouteByUserReply, error) {
	client, err := p.pick()
	if err != nil {
		return nil, err
	}
	return client.RouteByUser(ctx, in, opts...)
}

func (p *RouterClientPool) RouteByRoom(ctx context.Context, in *routerpb.RouteByRoomReq, opts ...grpc.CallOption) (*routerpb.RouteByRoomReply, error) {
	client, err := p.pick()
	if err != nil {
		return nil, err
	}
	return client.RouteByRoom(ctx, in, opts...)
}

func (p *RouterClientPool) RouteBroadcast(ctx context.Context, in *routerpb.RouteBroadcastReq, opts ...grpc.CallOption) (*routerpb.RouteBroadcastReply, error) {
	client, err := p.pick()
	if err != nil {
		return nil, err
	}
	return client.RouteBroadcast(ctx, in, opts...)
}

func (p *RouterClientPool) HandleACK(ctx context.Context, in *routerpb.HandleACKReq, opts ...grpc.CallOption) (*routerpb.HandleACKReply, error) {
	client, err := p.pick()
	if err != nil {
		return nil, err
	}
	return client.HandleACK(ctx, in, opts...)
}

func (p *RouterClientPool) HandleACKWithDevice(ctx context.Context, in *routerpb.HandleACKWithDeviceReq, opts ...grpc.CallOption) (*routerpb.HandleACKWithDeviceReply, error) {
	client, err := p.pick()
	if err != nil {
		return nil, err
	}
	return client.HandleACKWithDevice(ctx, in, opts...)
}

func (p *RouterClientPool) GetMessageStatus(ctx context.Context, in *routerpb.GetMessageStatusReq, opts ...grpc.CallOption) (*routerpb.GetMessageStatusReply, error) {
	client, err := p.pick()
	if err != nil {
		return nil, err
	}
	return client.GetMessageStatus(ctx, in, opts...)
}

func (p *RouterClientPool) GetStats(ctx context.Context, in *routerpb.GetStatsReq, opts ...grpc.CallOption) (*routerpb.GetStatsReply, error) {
	client, err := p.pick()
	if err != nil {
		return nil, err
	}
	return client.GetStats(ctx, in, opts...)
}

func (p *RouterClientPool) DirectPush(ctx context.Context, in *routerpb.DirectPushReq, opts ...grpc.CallOption) (*routerpb.DirectPushReply, error) {
	client, err := p.pick()
	if err != nil {
		return nil, err
	}
	return client.DirectPush(ctx, in, opts...)
}

func dialRouterClient(addr string) (*grpc.ClientConn, routerpb.RouterClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr,
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
		return nil, nil, err
	}
	return conn, routerpb.NewRouterClient(conn), nil
}
