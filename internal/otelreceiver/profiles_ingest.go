package otelreceiver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"go.opentelemetry.io/collector/pdata/pprofile/pprofileotlp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type profilesGRPC struct {
	pprofileotlp.UnimplementedGRPCServer
	receiver *Receiver
}

func (g *profilesGRPC) Export(ctx context.Context, request pprofileotlp.ExportRequest) (pprofileotlp.ExportResponse, error) {
	if err := g.receiver.acquire(); err != nil {
		return pprofileotlp.ExportResponse{}, err
	}
	defer g.receiver.release()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return g.receiver.ingestProfiles(ctx, request)
}

func (r *Receiver) exportProfilesHTTP(request *http.Request, format string) ([]byte, error) {
	if err := pprofileotlp.ValidateDevelopmentVersion(request.Header.Values(pprofileotlp.DevelopmentVersionHeader)); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := r.acquire(); err != nil {
		return nil, err
	}
	defer r.release()
	body, err := r.readBody(request)
	if err != nil {
		return nil, err
	}
	decoded := pprofileotlp.NewExportRequest()
	if format == jsonType {
		err = decoded.UnmarshalJSON(body)
	} else {
		err = decoded.UnmarshalProto(body)
	}
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "malformed OTLP Profiles request")
	}
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	response, err := r.ingestProfiles(ctx, decoded)
	if err != nil {
		return nil, err
	}
	if format == jsonType {
		return response.MarshalJSON()
	}
	return response.MarshalProto()
}

func (r *Receiver) ingestProfiles(ctx context.Context, request pprofileotlp.ExportRequest) (pprofileotlp.ExportResponse, error) {
	response := pprofileotlp.NewExportResponse()
	data, err := request.MarshalProto()
	if err != nil {
		return response, status.Error(codes.InvalidArgument, "invalid OTLP Profiles request")
	}
	if len(data) > r.config.Options.MaxRequestBytes {
		return response, status.Error(codes.ResourceExhausted, "OTLP Profiles request exceeds its byte limit")
	}
	if request.Profiles().ProfileCount() == 0 {
		return response, nil
	}
	snapshots, err := profiles.Decode(data, profiles.Options{MaxBytes: r.config.Options.MaxRequestBytes, MaxSamples: r.config.Options.MaxSpans})
	if err != nil {
		if errors.Is(err, profiles.ErrLimit) {
			return response, status.Error(codes.ResourceExhausted, err.Error())
		}
		return response, status.Error(codes.InvalidArgument, err.Error())
	}
	outcome, err := r.config.Store.PersistProfiles(ctx, snapshots)
	if err != nil {
		if errors.Is(err, errInvalidProfile) {
			return response, status.Error(codes.InvalidArgument, err.Error())
		}
		if ctx.Err() != nil {
			return response, status.FromContextError(ctx.Err()).Err()
		}
		return response, status.Error(codes.Unavailable, fmt.Sprintf("OTLP Profiles storage unavailable: %v", err))
	}
	if outcome.Rejected > 0 {
		response.PartialSuccess().SetRejectedProfiles(outcome.Rejected)
		response.PartialSuccess().SetErrorMessage(outcome.Reason)
	}
	return response, nil
}
