package otelreceiver

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const protobufType = "application/x-protobuf"
const jsonType = "application/json"

func (r *Receiver) HTTPHandler(requireAuth bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, protocolError{format: jsonType, httpStatus: http.StatusMethodNotAllowed, err: status.Error(codes.Unimplemented, "POST is required")})
			return
		}
		format, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || (format != protobufType && format != jsonType) {
			writeError(w, protocolError{format: jsonType, httpStatus: http.StatusUnsupportedMediaType, err: status.Error(codes.InvalidArgument, "use application/x-protobuf or application/json")})
			return
		}
		if requireAuth {
			if len(request.Header.Values("Authorization")) != 1 {
				writeError(w, protocolError{format: format, err: status.Error(codes.Unauthenticated, "one authorization header is required")})
				return
			}
			if err := r.authorize(request.Context(), request.Header.Get("Authorization")); err != nil {
				writeError(w, protocolError{format: format, err: err})
				return
			}
		}
		body, err := r.exportHTTP(request, format)
		if err != nil {
			writeError(w, protocolError{format: format, err: err})
			return
		}
		w.Header().Set("Content-Type", format)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

func (r *Receiver) exportHTTP(request *http.Request, format string) ([]byte, error) {
	if err := r.acquire(); err != nil {
		return nil, err
	}
	defer r.release()
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	body, err := r.readBody(request)
	if err != nil {
		return nil, err
	}
	decoded := ptraceotlp.NewExportRequest()
	if format == jsonType {
		err = decoded.UnmarshalJSON(body)
	} else {
		err = decoded.UnmarshalProto(body)
	}
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "malformed OTLP request")
	}
	response, err := r.ingest(ctx, decoded)
	if err != nil {
		return nil, err
	}
	if format == jsonType {
		body, err = response.MarshalJSON()
	} else {
		body, err = response.MarshalProto()
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "encode OTLP response")
	}
	return body, nil
}

func (r *Receiver) readBody(request *http.Request) ([]byte, error) {
	body, err := limitedBody(request.Body, r.config.Options.MaxRequestBytes)
	if err != nil {
		return nil, err
	}
	switch request.Header.Get("Content-Encoding") {
	case "", "identity":
		return body, nil
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid gzip request")
		}
		body, err = limitedBody(reader, r.config.Options.MaxRequestBytes)
		err = errors.Join(err, reader.Close())
		return body, err
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported content encoding")
	}
}

func limitedBody(reader io.Reader, limit int) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "read OTLP request: "+err.Error())
	}
	if len(body) > limit {
		return nil, status.Error(codes.ResourceExhausted, "OTLP request exceeds its byte limit")
	}
	return body, nil
}
