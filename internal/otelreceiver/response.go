package otelreceiver

import (
	"net/http"

	"github.com/flanksource/commons/logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type protocolError struct {
	format     string
	httpStatus int
	err        error
}

func writeError(w http.ResponseWriter, failure protocolError) {
	message := status.Convert(failure.err)
	code := failure.httpStatus
	if code == 0 {
		switch message.Code() {
		case codes.InvalidArgument:
			code = http.StatusBadRequest
		case codes.Unauthenticated:
			code = http.StatusUnauthorized
		case codes.PermissionDenied:
			code = http.StatusForbidden
		case codes.ResourceExhausted:
			code = http.StatusRequestEntityTooLarge
		case codes.Unavailable:
			code = http.StatusServiceUnavailable
		case codes.DeadlineExceeded:
			code = http.StatusGatewayTimeout
		default:
			code = http.StatusInternalServerError
		}
	}
	var body []byte
	var err error
	if failure.format == jsonType {
		body, err = protojson.Marshal(message.Proto())
	} else {
		body, err = proto.Marshal(message.Proto())
	}
	if err != nil {
		logger.Errorf("encode OTLP error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", failure.format)
	if code == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(code)
	_, _ = w.Write(body)
}
