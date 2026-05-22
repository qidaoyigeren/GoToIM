package grpc

import (
	"context"
	"fmt"
	"net"
	"time"

	pb "github.com/Terry-Mao/goim/api/router"
	"github.com/Terry-Mao/goim/internal/grpcx"
	logicconf "github.com/Terry-Mao/goim/internal/logic/conf"
	"github.com/Terry-Mao/goim/internal/logic/service"
	"github.com/Terry-Mao/goim/internal/router"
	log "github.com/Terry-Mao/goim/pkg/log"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	_ "google.golang.org/grpc/encoding/gzip"
)

// New creates and starts the Router gRPC server.
func New(c *logicconf.RPCServer, engine *router.DispatchEngine) *grpc.Server {
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
	pb.RegisterRouterServer(srv, &server{engine: engine})
	lis, err := net.Listen(c.Network, c.Addr)
	if err != nil {
		log.Fatalf("router grpc net.Listen(%s, %s) error(%v)", c.Network, c.Addr, err)
	}
	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("router grpc srv.Serve error(%v)", err)
		}
	}()
	return srv
}

type server struct {
	pb.UnimplementedRouterServer
	engine *router.DispatchEngine
}

var _ pb.RouterServer = &server{}

func (s *server) RouteByUser(ctx context.Context, req *pb.RouteByUserReq) (*pb.RouteByUserReply, error) {
	result, err := s.engine.RouteByUserResult(ctx, req.MsgId, req.ToUid, req.Op, req.Body, req.Seq)
	reply := &pb.RouteByUserReply{
		MsgId:        result.MsgID,
		Path:         normalizePath(result.Path),
		TargetNode:   result.TargetNode,
		ErrorCode:    result.ErrorCode,
		ErrorMessage: result.ErrorMessage,
		LatencyMs:    result.LatencyMs,
	}
	if err != nil {
		if reply.ErrorCode == "" {
			reply.ErrorCode = "route_failed"
		}
		if reply.ErrorMessage == "" {
			reply.ErrorMessage = err.Error()
		}
	}
	return reply, nil
}

func (s *server) RouteByRoom(ctx context.Context, req *pb.RouteByRoomReq) (*pb.RouteByRoomReply, error) {
	if err := s.engine.RouteByRoom(ctx, req.Op, req.RoomKey, req.Body); err != nil {
		return &pb.RouteByRoomReply{}, err
	}
	return &pb.RouteByRoomReply{}, nil
}

func (s *server) RouteBroadcast(ctx context.Context, req *pb.RouteBroadcastReq) (*pb.RouteBroadcastReply, error) {
	if err := s.engine.RouteBroadcast(ctx, req.Op, req.Speed, req.Body); err != nil {
		return &pb.RouteBroadcastReply{}, err
	}
	return &pb.RouteBroadcastReply{}, nil
}

func (s *server) HandleACK(ctx context.Context, req *pb.HandleACKReq) (*pb.HandleACKReply, error) {
	if err := s.engine.HandleACK(ctx, req.Uid, req.MsgId); err != nil {
		return &pb.HandleACKReply{}, err
	}
	return &pb.HandleACKReply{}, nil
}

func (s *server) HandleACKWithDevice(ctx context.Context, req *pb.HandleACKWithDeviceReq) (*pb.HandleACKWithDeviceReply, error) {
	if err := s.engine.HandleACKWithDevice(ctx, req.Uid, req.MsgId, req.DeviceId, req.SessionId); err != nil {
		return &pb.HandleACKWithDeviceReply{}, err
	}
	return &pb.HandleACKWithDeviceReply{}, nil
}

func (s *server) GetMessageStatus(ctx context.Context, req *pb.GetMessageStatusReq) (*pb.GetMessageStatusReply, error) {
	status, err := s.engine.GetMessageStatus(ctx, req.MsgId)
	if err != nil {
		return &pb.GetMessageStatusReply{}, err
	}
	return &pb.GetMessageStatusReply{Status: status}, nil
}

func (s *server) GetStats(ctx context.Context, req *pb.GetStatsReq) (*pb.GetStatsReply, error) {
	stats := s.engine.Stats()
	return &pb.GetStatsReply{Direct: stats.Direct, Kafka: stats.Kafka}, nil
}

func (s *server) DirectPush(ctx context.Context, req *pb.DirectPushReq) (*pb.DirectPushReply, error) {
	sessions := make([]*service.Session, 0, len(req.Sessions))
	for _, sess := range req.Sessions {
		if sess == nil {
			continue
		}
		sessions = append(sessions, &service.Session{Server: sess.Server, Key: sess.Key})
	}
	failed, err := s.engine.DirectPush(ctx, sessions, req.Op, req.Body)
	reply := &pb.DirectPushReply{FailedSessions: toProtoSessions(failed)}
	if err != nil && len(reply.FailedSessions) == 0 {
		return reply, fmt.Errorf("direct push: %w", err)
	}
	return reply, nil
}

func toProtoSessions(sessions []*service.Session) []*pb.PushSession {
	out := make([]*pb.PushSession, 0, len(sessions))
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		out = append(out, &pb.PushSession{Server: sess.Server, Key: sess.Key})
	}
	return out
}

func normalizePath(path string) string {
	switch path {
	case "grpc_direct":
		return "direct"
	case "kafka_fallback":
		return "kafka"
	default:
		return path
	}
}
