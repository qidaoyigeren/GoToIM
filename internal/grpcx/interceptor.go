// Package grpcx provides shared gRPC interceptors (recovery, logging) for all services.
package grpcx

import (
	"context"
	"runtime/debug"
	"time"

	log "github.com/Terry-Mao/goim/pkg/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RecoveryUnaryServerInterceptor recovers from panics in unary RPC handlers.
func RecoveryUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("grpc panic recovered: method=%s panic=%v\n%s", info.FullMethod, r, string(debug.Stack()))
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

// RecoveryStreamServerInterceptor recovers from panics in stream RPC handlers.
func RecoveryStreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("grpc stream panic recovered: method=%s panic=%v\n%s", info.FullMethod, r, string(debug.Stack()))
			}
		}()
		return handler(srv, ss)
	}
}

// LoggingUnaryServerInterceptor logs each unary RPC call with latency and status.
func LoggingUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err)
		log.Infof("grpc unary: method=%s code=%s latency=%v", info.FullMethod, code, time.Since(start))
		return resp, err
	}
}

// LoggingStreamServerInterceptor logs each stream RPC call with latency.
func LoggingStreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		code := codes.OK
		if err != nil {
			code = status.Code(err)
		}
		log.Infof("grpc stream: method=%s code=%s latency=%v", info.FullMethod, code, time.Since(start))
		return err
	}
}

// UnaryInterceptorChain is a convenience chain for unary interceptors.
func UnaryInterceptorChain() grpc.UnaryServerInterceptor {
	recovery := RecoveryUnaryServerInterceptor()
	logging := LoggingUnaryServerInterceptor()
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		return recovery(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
			return logging(ctx, req, info, handler)
		})
	}
}

// StreamInterceptorChain is a convenience chain for stream interceptors.
func StreamInterceptorChain() grpc.StreamServerInterceptor {
	recovery := RecoveryStreamServerInterceptor()
	logging := LoggingStreamServerInterceptor()
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return recovery(srv, ss, info, func(srv interface{}, ss grpc.ServerStream) error {
			return logging(srv, ss, info, handler)
		})
	}
}
