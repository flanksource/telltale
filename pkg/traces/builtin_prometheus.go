package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/prometheus"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{
		Name: prometheus.Kind, Title: "Prometheus metrics", Seams: []target.Seam{target.Host}, DefaultSeam: target.Host,
		Summary: "Scrape Prometheus metrics endpoints into VictoriaMetrics",
		Description: "Adds endpoints to the data directory's managed VictoriaMetrics " + prometheus.Version + ", which scrapes them at --interval and stores samples for 15 days. " +
			"Captures report target health and emit zero scrape records. Run telltale serve --prometheus first to keep the instance available between runs using the same data directory. " +
			"Query samples with PromQL using the URL shown by the capture or the read-only " + prometheus.ConnectionName +
			" connection. Samples have telltale_run, telltale_stream and telltale_endpoint labels plus --labels. " +
			"Inline Kubernetes endpoints use Telltale's Kubernetes identity. Replace the example service, namespace and port with yours. portforward:// and port-forward:// are equivalent.",
		Examples: []Example{
			{Args: "--endpoints http://localhost:8080/metrics --interval 5s --duration 1m", Says: "scrape one endpoint every 5 seconds for a minute"},
			{Args: "--endpoints http://localhost:9100/metrics,http://localhost:9090/metrics --labels env=dev", Says: "scrape two exporters every 15 seconds, labelled env=dev, until Ctrl+C"},
			{Args: "--endpoints port-forward://metrics-exporter.monitoring:9100/metrics --duration 1m", Says: "scrape a Kubernetes Service through a port-forward managed by Telltale"},
			{Args: "--endpoints proxy://metrics-exporter.monitoring:9100/metrics --duration 1m", Says: "scrape a Kubernetes Service through the API server proxy"},
			{Args: "--endpoints https://metrics.example.com/metrics --duration 1m", Says: "scrape an exporter reachable over HTTPS"},
		},
		New: func(deps KindDeps) tracing.TracePlugin {
			managed := prometheus.NewManaged(deps.DataDir)
			if deps.OnClose != nil {
				deps.OnClose(managed.Release)
			}
			return prometheus.NewKind(prometheus.KindOptions{RunID: deps.RunID, Server: managed.Server})
		},
	})
}
