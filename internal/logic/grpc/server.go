package grpc

import (
	"context"
	"net"
	"time"

	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/grpcx"
	"github.com/Terry-Mao/goim/internal/logic"
	"github.com/Terry-Mao/goim/internal/logic/conf"
	log "github.com/Terry-Mao/goim/pkg/log"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	// use gzip decoder
	_ "google.golang.org/grpc/encoding/gzip"
)

// New logic grpc server
func New(c *conf.RPCServer, l *logic.Logic) *grpc.Server {
	keepParams := grpc.KeepaliveParams(keepalive.ServerParameters{
		MaxConnectionIdle:     time.Duration(c.IdleTimeout),
		MaxConnectionAgeGrace: time.Duration(c.ForceCloseWait),
		Time:                  time.Duration(c.KeepAliveInterval),
		Timeout:               time.Duration(c.KeepAliveTimeout),
		MaxConnectionAge:      time.Duration(c.MaxLifeTime),
	})
	srv := grpc.NewServer(keepParams,
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.UnaryInterceptor(grpcx.UnaryInterceptorChain()),
		grpc.StreamInterceptor(grpcx.StreamInterceptorChain()),
	)
	pb.RegisterLogicServer(srv, &server{srv: l})
	lis, err := net.Listen(c.Network, c.Addr)
	if err != nil {
		log.Fatalf("logic grpc net.Listen(%s, %s) error(%v)", c.Network, c.Addr, err)
	}
	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("logic grpc srv.Serve error(%v)", err)
		}
	}()
	return srv
}

type server struct {
	pb.UnimplementedLogicServer
	srv *logic.Logic
}

var _ pb.LogicServer = &server{}

// Connect connect a conn.
func (s *server) Connect(ctx context.Context, req *pb.ConnectReq) (*pb.ConnectReply, error) {
	mid, key, room, accepts, hb, err := s.srv.Connect(ctx, req.Server, req.Cookie, req.Token)
	if err != nil {
		return &pb.ConnectReply{}, err
	}
	return &pb.ConnectReply{Mid: mid, Key: key, RoomID: room, Accepts: accepts, Heartbeat: hb}, nil
}

// Disconnect disconnect a conn.
func (s *server) Disconnect(ctx context.Context, req *pb.DisconnectReq) (*pb.DisconnectReply, error) {
	has, err := s.srv.Disconnect(ctx, req.Mid, req.Key, req.Server)
	if err != nil {
		return &pb.DisconnectReply{}, err
	}
	return &pb.DisconnectReply{Has: has}, nil
}

// Heartbeat heartbeat a conn.
func (s *server) Heartbeat(ctx context.Context, req *pb.HeartbeatReq) (*pb.HeartbeatReply, error) {
	if err := s.srv.Heartbeat(ctx, req.Mid, req.Key, req.Server); err != nil {
		return &pb.HeartbeatReply{}, err
	}
	return &pb.HeartbeatReply{}, nil
}

// RenewOnline renew server online.
func (s *server) RenewOnline(ctx context.Context, req *pb.OnlineReq) (*pb.OnlineReply, error) {
	allRoomCount, err := s.srv.RenewOnline(ctx, req.Server, req.RoomCount)
	if err != nil {
		return &pb.OnlineReply{}, err
	}
	return &pb.OnlineReply{AllRoomCount: allRoomCount}, nil
}

// Receive receive a message.
func (s *server) Receive(ctx context.Context, req *pb.ReceiveReq) (*pb.ReceiveReply, error) {
	if err := s.srv.Receive(ctx, req.Mid, req.Proto); err != nil {
		return &pb.ReceiveReply{}, err
	}
	// Return the modified proto (e.g., SyncReply changes Op and Body)
	return &pb.ReceiveReply{Proto: req.Proto}, nil
}

// nodes return nodes.
func (s *server) Nodes(ctx context.Context, req *pb.NodesReq) (*pb.NodesReply, error) {
	return s.srv.NodesWeighted(ctx, req.Platform, req.ClientIP), nil
}

// AckMessage acknowledges a delivered message.
func (s *server) AckMessage(ctx context.Context, req *pb.AckReq) (*pb.AckReply, error) {
	if err := s.srv.AckMessage(ctx, req.Mid, req.MsgId); err != nil {
		return &pb.AckReply{}, err
	}
	return &pb.AckReply{}, nil
}

// SyncOffline syncs offline messages for a user.
func (s *server) SyncOffline(ctx context.Context, req *pb.SyncOfflineReq) (*pb.SyncOfflineReply, error) {
	reply, err := s.srv.GetOfflineMessages(ctx, req.Mid, req.LastSeq, req.Limit)
	if err != nil {
		return &pb.SyncOfflineReply{}, err
	}
	return &pb.SyncOfflineReply{
		CurrentSeq: reply.CurrentSeq,
		HasMore:    reply.HasMore,
	}, nil
}
