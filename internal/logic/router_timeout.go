package logic

import (
	"context"
	"time"
)

const defaultRouterRPCTimeout = time.Second

func (l *Logic) routerRPCContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	timeout := defaultRouterRPCTimeout
	if l != nil && l.c != nil && l.c.RPCClient != nil && l.c.RPCClient.Timeout > 0 {
		timeout = time.Duration(l.c.RPCClient.Timeout)
	}
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}
