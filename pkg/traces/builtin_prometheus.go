package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/prometheus"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func init() {
	MustRegister(Registration{
		Name: prometheus.Kind, Title: "Prometheus metrics", Seams: []target.Seam{target.Host}, DefaultSeam: target.Host,
		Summary: "Scrape Prometheus metrics endpoints into a Prometheus telltale manages",
		Description: "Hands each endpoint to the data dir's managed Prometheus " + prometheus.Version + " as a file_sd target of job " + prometheus.Job +
			", scraped every --interval, and records one row per completed scrape: its endpoint, health, duration, error and sample count. " +
			"The first capture downloads and starts that Prometheus through deps (state and TSDB under <data-dir>/services/prometheus) as telltale's sub-process: the run's captures share it, it stops when the run ends, and the next run starts it again on the same TSDB and port. " +
			"The samples stay in its TSDB, labelled telltale_run, telltale_stream and telltale_endpoint plus --labels: query them with PromQL at the URL the run shows, or as the read-only " +
			prometheus.ConnectionName + " connection of telltale serve. Ending the capture stops the scraping, not the samples.",
		Examples: []Example{
			{Args: "--endpoints http://localhost:8080/metrics --interval 5s --duration 1m", Says: "scrape one endpoint every 5 seconds for a minute"},
			{Args: "--endpoints http://localhost:9100/metrics,http://localhost:9090/metrics --labels env=dev", Says: "scrape two exporters every 15 seconds, labelled env=dev, until Ctrl+C"},
		},
		New: func(deps KindDeps) tracing.TracePlugin {
			managed := prometheus.NewManaged(deps.DataDir)
			if deps.OnClose != nil {
				deps.OnClose(managed.Release)
			}
			return prometheus.NewKind(prometheus.Options{RunID: deps.RunID, Server: managed.Server})
		},
	})
}
