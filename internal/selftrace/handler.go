package selftrace

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
)

func (t *Tracer) Handler(next http.Handler) http.Handler {
	if next == nil {
		panic("self-trace: HTTP handler must not be nil")
	}
	return otelhttp.NewHandler(next, "telltale.http",
		otelhttp.WithTracerProvider(t.provider),
		otelhttp.WithMeterProvider(noop.NewMeterProvider()),
		otelhttp.WithPropagators(propagation.TraceContext{}),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/v1/traces" && !strings.HasPrefix(r.URL.Path, "/v1/traces/")
		}),
	)
}
