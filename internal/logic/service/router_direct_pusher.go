package service

import (
	"context"
	"fmt"

	routerpb "github.com/Terry-Mao/goim/api/router"
	"google.golang.org/grpc"
)

type routerDirectPushClient interface {
	DirectPush(ctx context.Context, in *routerpb.DirectPushReq, opts ...grpc.CallOption) (*routerpb.DirectPushReply, error)
}

// RouterDirectPusher adapts the Router gRPC client to SyncService's DirectPusher interface.
type RouterDirectPusher struct {
	client routerDirectPushClient
}

// NewRouterDirectPusher creates a DirectPusher backed by the standalone Router service.
func NewRouterDirectPusher(client routerDirectPushClient) *RouterDirectPusher {
	return &RouterDirectPusher{client: client}
}

// DirectPush forwards direct session pushes to the Router service.
func (p *RouterDirectPusher) DirectPush(ctx context.Context, sessions []*Session, op int32, body []byte) ([]*Session, error) {
	if p == nil || p.client == nil {
		return sessions, fmt.Errorf("router direct pusher not configured")
	}
	req := &routerpb.DirectPushReq{
		Sessions: make([]*routerpb.PushSession, 0, len(sessions)),
		Op:       op,
		Body:     body,
	}
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		req.Sessions = append(req.Sessions, &routerpb.PushSession{
			Server: sess.Server,
			Key:    sess.Key,
		})
	}
	reply, err := p.client.DirectPush(ctx, req)
	if err != nil {
		return nil, err
	}
	if reply == nil {
		return nil, fmt.Errorf("empty router direct push reply")
	}
	failed := make([]*Session, 0, len(reply.FailedSessions))
	for _, sess := range reply.FailedSessions {
		if sess == nil {
			continue
		}
		failed = append(failed, &Session{Server: sess.Server, Key: sess.Key})
	}
	if len(failed) > 0 && len(failed) == len(req.Sessions) {
		return failed, fmt.Errorf("all router direct pushes failed")
	}
	return failed, nil
}
