package otelreceiver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Config struct {
	Store    *Store
	Options  Options
	Verifier *apitoken.Verifier
}

type Receiver struct {
	ptraceotlp.UnimplementedGRPCServer
	config  Config
	slots   chan struct{}
	mu      sync.Mutex
	stopped bool
	active  int
	drained chan struct{}
}

func New(config Config) (*Receiver, error) {
	if config.Store == nil || config.Verifier == nil {
		return nil, fmt.Errorf("OTLP store and token verifier are required")
	}
	if err := config.Options.Validate(); err != nil {
		return nil, err
	}
	return &Receiver{config: config, slots: make(chan struct{}, config.Options.MaxInflight), drained: make(chan struct{})}, nil
}

func (r *Receiver) acquire() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return status.Error(codes.Unavailable, "OTLP receiver is stopping")
	}
	select {
	case r.slots <- struct{}{}:
		r.active++
		return nil
	default:
		return status.Error(codes.Unavailable, "OTLP receiver is at capacity; retry later")
	}
}

func (r *Receiver) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	<-r.slots
	r.active--
	if r.stopped && r.active == 0 {
		close(r.drained)
	}
}

func (r *Receiver) StopIntake() {
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		if r.active == 0 {
			close(r.drained)
		}
	}
	r.mu.Unlock()
}

func (r *Receiver) Stop(ctx context.Context) error {
	r.StopIntake()
	select {
	case <-r.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Receiver) Export(ctx context.Context, request ptraceotlp.ExportRequest) (ptraceotlp.ExportResponse, error) {
	if err := r.acquire(); err != nil {
		return ptraceotlp.ExportResponse{}, err
	}
	defer r.release()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return r.ingest(ctx, request)
}

func (r *Receiver) ingest(ctx context.Context, request ptraceotlp.ExportRequest) (ptraceotlp.ExportResponse, error) {
	response := ptraceotlp.NewExportResponse()
	encoded, err := request.MarshalProto()
	if err != nil {
		return response, status.Error(codes.InvalidArgument, "invalid OTLP request")
	}
	if len(encoded) > r.config.Options.MaxRequestBytes {
		return response, status.Error(codes.ResourceExhausted, "OTLP request exceeds its byte limit")
	}
	converted, err := normalize(request, r.config.Options)
	if err != nil {
		if errors.Is(err, ErrLimit) {
			return response, status.Error(codes.ResourceExhausted, err.Error())
		}
		return response, status.Error(codes.InvalidArgument, err.Error())
	}
	outcome, err := r.config.Store.Persist(ctx, converted.Spans)
	if err != nil {
		if ctx.Err() != nil {
			return response, status.FromContextError(ctx.Err()).Err()
		}
		logger.Errorf("OTLP persistence failed: %v", err)
		return response, status.Error(codes.Unavailable, "OTLP storage unavailable; retry later")
	}
	if rejected := converted.Rejected + outcome.Rejected; rejected > 0 {
		response.PartialSuccess().SetRejectedSpans(rejected)
		response.PartialSuccess().SetErrorMessage("invalid or conflicting spans were rejected")
	}
	logger.Infof("OTLP export accepted=%d duplicates=%d rejected=%d", outcome.Accepted, outcome.Duplicates, converted.Rejected+outcome.Rejected)
	return response, nil
}
