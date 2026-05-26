package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	routerpb "github.com/Terry-Mao/goim/api/router"
	"github.com/Terry-Mao/goim/internal/logic/conf"
	xtime "github.com/Terry-Mao/goim/pkg/time"
	"google.golang.org/grpc"
)

func TestPushToUserAddsRouterRPCTimeout(t *testing.T) {
	client := &blockingRouterClient{}
	l := &Logic{
		c:            &conf.Config{RPCClient: &conf.RPCClient{Timeout: xtime.Duration(25 * time.Millisecond)}},
		routerClient: client,
	}

	start := time.Now()
	err := l.PushToUser(context.Background(), "msg-timeout", 42, 100, []byte("hello"), 1)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("PushToUser error = %v, want context deadline exceeded", err)
	}
	if !client.sawRouteByUserDeadline {
		t.Fatalf("RouteByUser did not receive a context deadline")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("PushToUser took %s, expected bounded router call", elapsed)
	}
}

func TestRouterRPCContextKeepsShorterParentDeadline(t *testing.T) {
	l := &Logic{
		c: &conf.Config{RPCClient: &conf.RPCClient{Timeout: xtime.Duration(time.Second)}},
	}
	parent, parentCancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer parentCancel()

	ctx, cancel := l.routerRPCContext(parent)
	defer cancel()

	got, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("router context has no deadline")
	}
	want, _ := parent.Deadline()
	if got.Sub(want) > time.Millisecond || want.Sub(got) > time.Millisecond {
		t.Fatalf("router deadline = %s, want parent deadline %s", got, want)
	}
}

type blockingRouterClient struct {
	sawRouteByUserDeadline bool
}

func (c *blockingRouterClient) RouteByUser(ctx context.Context, req *routerpb.RouteByUserReq, opts ...grpc.CallOption) (*routerpb.RouteByUserReply, error) {
	_, c.sawRouteByUserDeadline = ctx.Deadline()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *blockingRouterClient) RouteByRoom(ctx context.Context, req *routerpb.RouteByRoomReq, opts ...grpc.CallOption) (*routerpb.RouteByRoomReply, error) {
	return &routerpb.RouteByRoomReply{}, nil
}

func (c *blockingRouterClient) RouteBroadcast(ctx context.Context, req *routerpb.RouteBroadcastReq, opts ...grpc.CallOption) (*routerpb.RouteBroadcastReply, error) {
	return &routerpb.RouteBroadcastReply{}, nil
}

func (c *blockingRouterClient) HandleACK(ctx context.Context, req *routerpb.HandleACKReq, opts ...grpc.CallOption) (*routerpb.HandleACKReply, error) {
	return &routerpb.HandleACKReply{}, nil
}

func (c *blockingRouterClient) HandleACKWithDevice(ctx context.Context, req *routerpb.HandleACKWithDeviceReq, opts ...grpc.CallOption) (*routerpb.HandleACKWithDeviceReply, error) {
	return &routerpb.HandleACKWithDeviceReply{}, nil
}

func (c *blockingRouterClient) GetMessageStatus(ctx context.Context, req *routerpb.GetMessageStatusReq, opts ...grpc.CallOption) (*routerpb.GetMessageStatusReply, error) {
	return &routerpb.GetMessageStatusReply{}, nil
}

func (c *blockingRouterClient) GetStats(ctx context.Context, req *routerpb.GetStatsReq, opts ...grpc.CallOption) (*routerpb.GetStatsReply, error) {
	return &routerpb.GetStatsReply{}, nil
}

func (c *blockingRouterClient) DirectPush(ctx context.Context, req *routerpb.DirectPushReq, opts ...grpc.CallOption) (*routerpb.DirectPushReply, error) {
	return &routerpb.DirectPushReply{}, nil
}
