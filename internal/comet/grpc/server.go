package grpc

import (
	"context"
	"net"
	"time"

	pb "github.com/Terry-Mao/goim/api/comet"
	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/comet"
	"github.com/Terry-Mao/goim/internal/comet/conf"
	"github.com/Terry-Mao/goim/internal/comet/errors"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/golang/protobuf/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// New comet grpc server.
func New(c *conf.RPCServer, s *comet.Server) *grpc.Server {
	keepParams := grpc.KeepaliveParams(keepalive.ServerParameters{
		MaxConnectionIdle:     time.Duration(c.IdleTimeout),
		MaxConnectionAgeGrace: time.Duration(c.ForceCloseWait),
		Time:                  time.Duration(c.KeepAliveInterval),
		Timeout:               time.Duration(c.KeepAliveTimeout),
		MaxConnectionAge:      time.Duration(c.MaxLifeTime),
	})
	srv := grpc.NewServer(keepParams)
	pb.RegisterCometServer(srv, &server{s})
	lis, err := net.Listen(c.Network, c.Addr)
	if err != nil {
		log.Fatalf("comet grpc net.Listen(%s, %s) error(%v)", c.Network, c.Addr, err)
	}
	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("comet grpc srv.Serve error(%v)", err)
		}
	}()
	return srv
}

type server struct {
	srv *comet.Server
}

var _ pb.CometServer = &server{}

// PushMsg push a message to specified sub keys.
func (s *server) PushMsg(ctx context.Context, req *pb.PushMsgReq) (reply *pb.PushMsgReply, err error) {
	if len(req.Keys) == 0 || req.Proto == nil {
		return nil, errors.ErrPushMsgArg
	}
	log.Infof("PushMsg: keys=%v protoOp=%d bodyLen=%d", req.Keys, req.ProtoOp, len(req.Proto.Body))
	for _, key := range req.Keys {
		bucket := s.srv.Bucket(key)
		if bucket == nil {
			log.Warningf("PushMsg: bucket nil for key=%s", key)
			continue
		}
		if channel := bucket.Channel(key); channel != nil {
			// OpKickConnection: close the channel directly
			if req.ProtoOp == protocol.OpKickConnection {
				channel.Close()
				continue
			}
			if !channel.NeedPush(req.ProtoOp) {
				log.Warningf("PushMsg: NeedPush=false key=%s op=%d (client did not accept this op)", key, req.ProtoOp)
				continue
			}
			if err = channel.Push(req.Proto); err != nil {
				log.Warningf("PushMsg: Push failed key=%s err=%v", key, err)
				return
			}
			// Signal the dispatch goroutine to wake up and write the message
			channel.Signal()
			log.Infof("PushMsg: delivered key=%s op=%d", key, req.ProtoOp)
		} else {
			log.Warningf("PushMsg: channel nil for key=%s", key)
		}
	}
	return &pb.PushMsgReply{}, nil
}

// Broadcast broadcast msg to all user.
func (s *server) Broadcast(ctx context.Context, req *pb.BroadcastReq) (*pb.BroadcastReply, error) {
	if req.Proto == nil {
		return nil, errors.ErrBroadCastArg
	}
	// Deep-copy proto since the goroutine outlives this RPC handler and
	// the gRPC framework may reuse the request message.
	p := proto.Clone(req.Proto).(*protocol.Proto)
	op := req.ProtoOp
	speed := req.Speed
	// TODO use broadcast queue
	go func() {
		srvCtx := s.srv.Context()
		for _, bucket := range s.srv.Buckets() {
			bucket.Broadcast(p, op)
			if speed > 0 {
				count := bucket.ChannelCount()
				if count > 0 {
					t := count / int(speed)
					if t > 0 {
						select {
						case <-time.After(time.Duration(t) * time.Second):
						case <-srvCtx.Done():
							return
						}
					}
				}
			}
		}
	}()
	return &pb.BroadcastReply{}, nil
}

// BroadcastRoom broadcast msg to specified room.
func (s *server) BroadcastRoom(ctx context.Context, req *pb.BroadcastRoomReq) (*pb.BroadcastRoomReply, error) {
	if req.Proto == nil || req.RoomID == "" {
		return nil, errors.ErrBroadCastRoomArg
	}
	for _, bucket := range s.srv.Buckets() {
		bucket.BroadcastRoom(req)
	}
	return &pb.BroadcastRoomReply{}, nil
}

// Rooms gets all the room ids for the server.
func (s *server) Rooms(ctx context.Context, req *pb.RoomsReq) (*pb.RoomsReply, error) {
	var (
		roomIds = make(map[string]bool)
	)
	for _, bucket := range s.srv.Buckets() {
		for roomID := range bucket.Rooms() {
			roomIds[roomID] = true
		}
	}
	return &pb.RoomsReply{Rooms: roomIds}, nil
}
