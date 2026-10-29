package otelreceiver

import (
	"context"

	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (r *Receiver) GRPCServer(requireAuth bool) *grpc.Server {
	server := grpc.NewServer(grpc.MaxRecvMsgSize(r.config.Options.MaxRequestBytes), grpc.MaxConcurrentStreams(uint32(r.config.Options.MaxInflight)), grpc.UnaryInterceptor(func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if requireAuth {
			values, _ := metadata.FromIncomingContext(ctx)
			headers := values.Get("authorization")
			if len(headers) != 1 {
				return nil, status.Error(codes.Unauthenticated, "one authorization header is required")
			}
			if err := r.authorize(ctx, headers[0]); err != nil {
				return nil, err
			}
		}
		return handler(ctx, request)
	}))
	ptraceotlp.RegisterGRPCServer(server, r)
	return server
}
