package prometheus

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// commitWait bounds how long a scrape Prometheus reports may go without its
// scrape_samples_scraped sample: it reports the scrape before it commits it.
const commitWait = 5 * time.Second

// Summary is the kind's report of its capture.
type Summary struct {
	Prometheus string `json:"prometheus"`
	Job        string `json:"job"`
	Targets    int    `json:"targets"`
	Up         int    `json:"up"`
	Down       int    `json:"down"`
	Scrapes    int    `json:"scrapes"`
}

// watcher turns a session's targets into one Scrape each time a target's
// last scrape advances.
type watcher struct {
	client   *client
	stream   string
	interval time.Duration
	last     map[string]time.Time
	scrapes  int
}

// poll reads the session's targets and the scrapes completed since the last
// poll, with what the capture has seen so far.
func (w *watcher) poll(ctx context.Context) ([]Scrape, Summary, error) {
	targets, err := w.client.targets(ctx, w.stream)
	if err != nil {
		return nil, Summary{}, err
	}
	summary := Summary{Prometheus: w.client.server.URL, Job: w.client.server.Job, Targets: len(targets)}
	var advanced []scrapeTarget
	for _, t := range targets {
		switch t.Health {
		case "up":
			summary.Up++
		case "down":
			summary.Down++
		}
		if t.LastScrape.After(w.last[t.Labels[LabelEndpoint]]) {
			advanced = append(advanced, t)
		}
	}
	if len(advanced) == 0 {
		summary.Scrapes = w.scrapes
		return nil, summary, nil
	}
	samples, err := w.samplesScraped(ctx, advanced)
	if err != nil {
		return nil, Summary{}, err
	}
	scrapes := make([]Scrape, 0, len(advanced))
	for index, t := range advanced {
		endpoint := t.Labels[LabelEndpoint]
		w.last[endpoint] = t.LastScrape
		scrapes = append(scrapes, Scrape{
			TakenAt: t.LastScrape.UTC(), Endpoint: endpoint, Health: t.Health,
			DurationMs: t.LastScrapeDuration * 1000, Samples: int64(samples[index]), Error: t.LastError,
		})
	}
	w.scrapes += len(scrapes)
	summary.Scrapes = w.scrapes
	return scrapes, summary, nil
}

// samplesScraped is the scrape_samples_scraped sample of each scrape, the one
// nearest its start within half an interval, waiting up to commitWait for
// Prometheus to commit those it reported before committing them.
func (w *watcher) samplesScraped(ctx context.Context, scraped []scrapeTarget) ([]float64, error) {
	promql := fmt.Sprintf(`scrape_samples_scraped{%s=%q}[%dms]`, LabelStream, w.stream, (2*w.interval + commitWait).Milliseconds())
	deadline := time.Now().Add(commitWait)
	for {
		series, err := w.client.series(ctx, promql)
		if err != nil {
			return nil, err
		}
		samples, missing := matchScrapes(series, scraped, w.interval/2)
		if missing == nil {
			return samples, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("prometheus %s has no scrape_samples_scraped sample for the scrape of %s at %s after %s",
				w.client.server.URL, missing.Labels[LabelEndpoint], missing.LastScrape.Format(time.RFC3339Nano), commitWait)
		}
		if err := sleep(ctx, 100*time.Millisecond); err != nil {
			return nil, err
		}
	}
}

// matchScrapes is the value series holds for each scrape, the point of its
// endpoint nearest its last scrape within tolerance, or the first scrape that
// has none.
func matchScrapes(series map[string][]point, scraped []scrapeTarget, tolerance time.Duration) ([]float64, *scrapeTarget) {
	samples := make([]float64, len(scraped))
	for index, t := range scraped {
		points := series[t.Labels[LabelEndpoint]]
		distance := func(p point) time.Duration { return max(p.at.Sub(t.LastScrape), t.LastScrape.Sub(p.at)) }
		nearest := slices.IndexFunc(points, func(p point) bool { return distance(p) <= tolerance })
		if nearest < 0 {
			return nil, &scraped[index]
		}
		for candidate, p := range points {
			if distance(p) < distance(points[nearest]) {
				nearest = candidate
			}
		}
		samples[index] = points[nearest].value
	}
	return samples, nil
}
