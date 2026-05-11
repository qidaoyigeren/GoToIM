package logic

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/api/comet"
	"github.com/Terry-Mao/goim/api/protocol"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/bilibili/discovery/naming"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// CometPusher maintains gRPC connections to Comet servers for direct push.
type CometPusher struct {
	mu      sync.RWMutex
	clients map[string]comet.CometClient // hostname -> gRPC client
	conns   map[string]*grpc.ClientConn  // hostname -> conn (for Close)
}

// NewCometPusher creates a new CometPusher with empty connection maps.
func NewCometPusher() *CometPusher {
	return &CometPusher{
		clients: make(map[string]comet.CometClient),
		conns:   make(map[string]*grpc.ClientConn),
	}
}

// PushMsg pushes a message to specific keys on a Comet server via gRPC.
func (p *CometPusher) PushMsg(ctx context.Context, server string, keys []string, op int32, body []byte) error {
	p.mu.RLock()
	client, ok := p.clients[server]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("comet server %s not found", server)
	}
	_, err := client.PushMsg(ctx, &comet.PushMsgReq{
		Keys:    keys,
		ProtoOp: op,
		Proto: &protocol.Proto{
			Ver:  1,
			Op:   op,
			Body: body,
		},
	})
	return err
}

// UpdateNodes refreshes gRPC connections based on discovered Comet instances.
// Uses copy-on-write: new connections are dialed outside the lock, then maps are swapped.
func (p *CometPusher) UpdateNodes(nodes []*naming.Instance) {
	if len(nodes) == 0 {
		log.Errorf("comet pusher: UpdateNodes called with 0 nodes")
		return
	}
	newClients := make(map[string]comet.CometClient, len(nodes))
	newConns := make(map[string]*grpc.ClientConn, len(nodes))

	// Reuse existing connections
	p.mu.RLock()
	for _, node := range nodes {
		if client, ok := p.clients[node.Hostname]; ok {
			newClients[node.Hostname] = client
			newConns[node.Hostname] = p.conns[node.Hostname]
		}
	}
	p.mu.RUnlock()

	// Dial new connections
	for _, node := range nodes {
		if _, ok := newClients[node.Hostname]; ok {
			continue
		}
		grpcAddr := grpcAddress(node)
		if grpcAddr == "" {
			log.Errorf("comet node %s has no grpc address: %v", node.Hostname, node.Addrs)
			continue
		}
		conn, client, err := dialCometClient(grpcAddr)
		if err != nil {
			log.Errorf("dial comet %s(%s) error(%v)", node.Hostname, grpcAddr, err)
			continue
		}
		newClients[node.Hostname] = client
		newConns[node.Hostname] = conn
		log.Infof("comet pusher: connected to %s (%s)", node.Hostname, grpcAddr)
	}

	// Swap maps and close removed connections
	p.mu.Lock()
	oldConns := p.conns
	p.clients = newClients
	p.conns = newConns
	p.mu.Unlock()

	for hostname, conn := range oldConns {
		if _, ok := newClients[hostname]; !ok {
			conn.Close()
			log.Infof("comet pusher: disconnected from %s", hostname)
		}
	}
}

// ConnectDirect directly connects to a Comet gRPC server by address (bypasses Discovery).
// Use this as a fallback when Discovery is unavailable.
func (p *CometPusher) ConnectDirect(addr string) {
	p.mu.RLock()
	for _, client := range p.clients {
		if client != nil {
			p.mu.RUnlock()
			return // already have a connection
		}
	}
	p.mu.RUnlock()

	conn, client, err := dialCometClient(addr)
	if err != nil {
		log.Errorf("comet pusher: direct dial %s error(%v)", addr, err)
		return
	}
	p.mu.Lock()
	p.clients["direct:"+addr] = client
	p.conns["direct:"+addr] = conn
	p.mu.Unlock()
	log.Infof("comet pusher: direct connected to %s", addr)
}

// KickConnection sends a kick signal to a Comet server to close a specific connection.
func (p *CometPusher) KickConnection(ctx context.Context, server, key string) error {
	p.mu.RLock()
	client, ok := p.clients[server]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("comet server %s not found", server)
	}
	_, err := client.PushMsg(ctx, &comet.PushMsgReq{
		Keys:    []string{key},
		ProtoOp: protocol.OpKickConnection,
		Proto: &protocol.Proto{
			Ver: 1,
			Op:  protocol.OpKickConnection,
		},
	})
	return err
}

// Close closes all gRPC connections.
func (p *CometPusher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for hostname, conn := range p.conns {
		conn.Close()
		delete(p.clients, hostname)
	}
	p.conns = make(map[string]*grpc.ClientConn)
}

// grpcAddress extracts the gRPC address from a discovery instance's Addrs.
func grpcAddress(in *naming.Instance) string {
	for _, addr := range in.Addrs {
		u, err := url.Parse(addr)
		if err == nil && u.Scheme == "grpc" {
			return u.Host
		}
	}
	return ""
}

// dialCometClient establishes a gRPC connection to a Comet server.
func dialCometClient(addr string) (*grpc.ClientConn, comet.CometClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithInsecure(),
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
	return conn, comet.NewCometClient(conn), nil
}
