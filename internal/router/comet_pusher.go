package router

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/api/comet"
	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/pkg/bytes"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/bilibili/discovery/naming"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// CometPusherPool manages Router-owned gRPC connections to Comet servers.
type CometPusherPool struct {
	mu      sync.RWMutex
	clients map[string]comet.CometClient
	conns   map[string]*grpc.ClientConn
}

// NewCometPusher creates an empty Comet pusher pool.
func NewCometPusher() *CometPusherPool {
	return &CometPusherPool{
		clients: make(map[string]comet.CometClient),
		conns:   make(map[string]*grpc.ClientConn),
	}
}

// PushMsg pushes a message to specific keys on one Comet server.
func (p *CometPusherPool) PushMsg(ctx context.Context, server string, keys []string, op int32, body []byte) error {
	p.mu.RLock()
	client, ok := p.clients[server]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("comet server %s not found", server)
	}

	pb := wrapProto(op, body)
	reply, err := client.PushMsg(ctx, &comet.PushMsgReq{
		Keys:    keys,
		ProtoOp: op,
		Proto:   pb,
	})
	if err != nil {
		return err
	}
	if reply == nil {
		return fmt.Errorf("empty comet push reply")
	}
	if failedKeys := reply.GetFailedKeys(); len(failedKeys) > 0 {
		return fmt.Errorf("comet push failed for keys %v", failedKeys)
	}
	return nil
}

// BroadcastRoom broadcasts a room message through all known Comet nodes.
func (p *CometPusherPool) BroadcastRoom(ctx context.Context, op int32, roomKey string, body []byte) error {
	pb := wrapProto(op, body)

	p.mu.RLock()
	defer p.mu.RUnlock()

	var lastErr error
	okCount := 0
	for server, client := range p.clients {
		if _, err := client.BroadcastRoom(ctx, &comet.BroadcastRoomReq{RoomID: roomKey, Proto: pb}); err != nil {
			lastErr = err
			log.Warningf("router broadcast room to %s failed: %v", server, err)
			continue
		}
		okCount++
	}
	if okCount == 0 && lastErr != nil {
		return fmt.Errorf("all comet broadcast room failed: %w", lastErr)
	}
	return nil
}

// BroadcastAll broadcasts a message through all known Comet nodes.
func (p *CometPusherPool) BroadcastAll(ctx context.Context, op, speed int32, body []byte) error {
	pb := wrapProto(op, body)

	p.mu.RLock()
	n := len(p.clients)
	p.mu.RUnlock()
	if n > 0 && speed > 0 {
		speed /= int32(n)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	var lastErr error
	okCount := 0
	for server, client := range p.clients {
		if _, err := client.Broadcast(ctx, &comet.BroadcastReq{ProtoOp: op, Proto: pb, Speed: speed}); err != nil {
			lastErr = err
			log.Warningf("router broadcast to %s failed: %v", server, err)
			continue
		}
		okCount++
	}
	if okCount == 0 && lastErr != nil {
		return fmt.Errorf("all comet broadcast failed: %w", lastErr)
	}
	return nil
}

// UpdateNodes refreshes Comet clients from service discovery instances.
func (p *CometPusherPool) UpdateNodes(nodes []*naming.Instance) {
	if len(nodes) == 0 {
		log.Warning("router comet pusher: no comet nodes discovered")
		return
	}

	newClients := make(map[string]comet.CometClient, len(nodes))
	newConns := make(map[string]*grpc.ClientConn, len(nodes))

	p.mu.RLock()
	for _, node := range nodes {
		if client, ok := p.clients[node.Hostname]; ok {
			newClients[node.Hostname] = client
			newConns[node.Hostname] = p.conns[node.Hostname]
		}
	}
	p.mu.RUnlock()

	for _, node := range nodes {
		if _, ok := newClients[node.Hostname]; ok {
			continue
		}
		addr := routerCometGRPCAddress(node)
		if addr == "" {
			log.Errorf("router comet node %s has no grpc address: %v", node.Hostname, node.Addrs)
			continue
		}
		conn, client, err := dialRouterCometClient(addr)
		if err != nil {
			log.Errorf("router dial comet %s(%s) error(%v)", node.Hostname, addr, err)
			continue
		}
		newClients[node.Hostname] = client
		newConns[node.Hostname] = conn
		log.Infof("router comet pusher connected to %s (%s)", node.Hostname, addr)
	}

	p.mu.Lock()
	oldConns := p.conns
	p.clients = newClients
	p.conns = newConns
	p.mu.Unlock()

	for hostname, conn := range oldConns {
		if _, ok := newClients[hostname]; !ok {
			conn.Close()
			log.Infof("router comet pusher disconnected from %s", hostname)
		}
	}
}

// Close closes all Comet connections.
func (p *CometPusherPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for hostname, conn := range p.conns {
		conn.Close()
		delete(p.clients, hostname)
	}
	p.conns = make(map[string]*grpc.ClientConn)
}

func wrapProto(op int32, body []byte) *protocol.Proto {
	buf := bytes.NewWriterSize(len(body) + 64)
	pb := &protocol.Proto{Ver: 1, Op: op, Body: body}
	pb.WriteTo(buf)
	pb.Body = buf.Buffer()
	pb.Op = protocol.OpRaw
	return pb
}

func routerCometGRPCAddress(in *naming.Instance) string {
	for _, addr := range in.Addrs {
		u, err := url.Parse(addr)
		if err == nil && u.Scheme == "grpc" {
			return u.Host
		}
	}
	return ""
}

func dialRouterCometClient(addr string) (*grpc.ClientConn, comet.CometClient, error) {
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
	return conn, comet.NewCometClient(conn), nil
}
