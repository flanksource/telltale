package server

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OTLP startup endpoints", func() {
	It("prints both receivers and the exporter variables for each on loopback listeners", func() {
		endpoints := otlpEndpoints{
			GRPC: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4317},
			API:  &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080},
		}
		Expect(endpoints.String()).To(Equal(`Telltale OTLP traces receivers:
  gRPC  127.0.0.1:4317
  HTTP  http://127.0.0.1:8080/v1/traces (application/x-protobuf or application/json)
Export over gRPC:
  export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=grpc
  export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:4317
Export over HTTP:
  export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf
  export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:8080/v1/traces
`))
	})

	It("names this host for a wildcard listener and asks for a token only where that transport requires one", func() {
		endpoints := otlpEndpoints{
			GRPC:     &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4317},
			API:      &net.TCPAddr{IP: net.IPv6unspecified, Port: 8080},
			Host:     "build-01",
			HTTPAuth: true,
		}
		Expect(endpoints.String()).To(Equal(`Telltale OTLP traces receivers:
  gRPC  127.0.0.1:4317
  HTTP  http://build-01:8080/v1/traces (application/x-protobuf or application/json; API token required)
Create an API token with: telltale agents token create <name> --scope api
Export over gRPC:
  export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=grpc
  export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:4317
Export over HTTP:
  export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf
  export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://build-01:8080/v1/traces
  export OTEL_EXPORTER_OTLP_TRACES_HEADERS="Authorization=Bearer <token>"
`))
	})
})
