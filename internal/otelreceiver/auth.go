package otelreceiver

import (
	"context"
	"errors"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (r *Receiver) authorize(ctx context.Context, header string) error {
	token, ok := apitoken.BearerFromHeader(header)
	if !ok {
		return status.Error(codes.Unauthenticated, "unauthorized")
	}
	_, err := r.config.Verifier.VerifyScope(ctx, token, apitoken.ScopeAPI)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, apitoken.ErrScope):
		return status.Error(codes.PermissionDenied, "API token scope required")
	case errors.Is(err, apitoken.ErrMalformed), errors.Is(err, apitoken.ErrUnknown), errors.Is(err, apitoken.ErrRevoked), errors.Is(err, apitoken.ErrExpired):
		return status.Error(codes.Unauthenticated, "unauthorized")
	default:
		logger.Errorf("OTLP token verification failed: %v", err)
		return status.Error(codes.Internal, "token verification failed")
	}
}
