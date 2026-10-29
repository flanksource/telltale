package server

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// otlpEndpoints is what serve prints at startup so an OpenTelemetry SDK can be
// pointed at the receiver: both transports and the exporter variables for each.
type otlpEndpoints struct {
	GRPC net.Addr
	// API is the HTTP listener; it serves OTLP/HTTP at /v1/traces.
	API net.Addr
	// Host replaces a wildcard bind address, which no exporter can dial.
	Host string
	// Each transport requires an API token when its own listener is not loopback.
	GRPCAuth bool
	HTTPAuth bool
}

func (e otlpEndpoints) dialable(addr net.Addr) string {
	tcp := addr.(*net.TCPAddr)
	host := tcp.IP.String()
	if tcp.IP.IsUnspecified() {
		host = e.Host
	}
	return net.JoinHostPort(host, strconv.Itoa(tcp.Port))
}

func (e otlpEndpoints) String() string {
	grpc := e.dialable(e.GRPC)
	httpURL := "http://" + e.dialable(e.API) + "/v1/traces"
	grpcNote, httpNote := "", ""
	if e.GRPCAuth {
		grpcNote = " (API token required)"
	}
	if e.HTTPAuth {
		httpNote = "; API token required"
	}
	var b strings.Builder
	b.WriteString("Telltale OTLP traces receivers:\n")
	fmt.Fprintf(&b, "  gRPC  %s%s\n", grpc, grpcNote)
	fmt.Fprintf(&b, "  HTTP  %s (application/x-protobuf or application/json%s)\n", httpURL, httpNote)
	if e.GRPCAuth || e.HTTPAuth {
		b.WriteString("Create an API token with: telltale agents token create <name> --scope api\n")
	}
	for _, export := range []struct {
		name, protocol, endpoint string
		auth                     bool
	}{
		{"gRPC", "grpc", "http://" + grpc, e.GRPCAuth},
		{"HTTP", "http/protobuf", httpURL, e.HTTPAuth},
	} {
		fmt.Fprintf(&b, "Export over %s:\n  export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=%s\n  export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=%s\n", export.name, export.protocol, export.endpoint)
		if export.auth {
			b.WriteString("  export OTEL_EXPORTER_OTLP_TRACES_HEADERS=\"Authorization=Bearer <token>\"\n")
		}
	}
	return b.String()
}
