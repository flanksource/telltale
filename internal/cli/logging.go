package cli

import (
	"net/http"
	"sync"

	"github.com/flanksource/commons/har"
	commonshttp "github.com/flanksource/commons/http"
	"github.com/flanksource/commons/http/middlewares"
	"github.com/flanksource/commons/logger"
)

// log is the package-scoped logger for cli diagnostics. Its level follows the
// global -v/--log-level and can be tuned independently with -Plog.level.cli=debug.
var log = logger.GetLogger("cli")

var httpLoggingOnce sync.Once

// EnableHTTPWireLogging installs commons' HTTP trace middleware on the default
// transport, so every outbound call made through http.DefaultClient is logged.
//
// commons has the same hook built in (logger.onPropertyUpdate wraps
// http.DefaultTransport for log.level.http), but it never fires here: the
// listener is registered by logger.UseSlog(), which telltale does not call,
// and -P values are bound straight into properties.commandlineProperties
// without going through Set/Update, so no listener is notified. If telltale
// ever adopts UseSlog(), the Once below is what keeps a second printer from
// stacking onto the first.
//
// The same transport carries HAR capture when -Phttp.har=<path> is set; the
// archive is written by FlushHAR once the command finishes.
//
// Must run before the first outbound request; NewRoot wires it from
// PersistentPreRunE, after clicky applies the logging flags.
func EnableHTTPWireLogging() error {
	var err error
	httpLoggingOnce.Do(func() {
		var captured http.RoundTripper
		// HAR sits innermost, closest to the transport, matching the ordering
		// commons/http uses so the archive records the request as it went out.
		if captured, err = harRegistry.Transport(harFeature, http.DefaultTransport); err != nil {
			return
		}
		if wrapped := wrapHTTPLogging(captured); wrapped != http.DefaultTransport {
			http.DefaultTransport = wrapped
			http.DefaultClient = &http.Client{Transport: wrapped}
		}
	})
	return err
}

// harFeature names telltale's traffic for per-subsystem overrides, so
// -Phttp.telltale.har=<path> and the global -Phttp.har=<path> both resolve.
const harFeature = "telltale"

// harRegistry resolves -Phttp.har* into collectors. It is process-wide because
// http.DefaultTransport is. Its own logger means capture announcements can be
// silenced or raised with -Plog.level.har.
var harRegistry = har.NewRegistry(logger.GetLogger("har"))

// FlushHAR writes any archive requested with -Phttp.har=<path>. cmd/telltale
// calls it after Execute returns — including on the error path, which is the
// run most worth capturing.
func FlushHAR() {
	if err := harRegistry.Flush(); err != nil {
		log.Errorf("%v", err)
	}
}

// wrapHTTPLogging returns base wrapped with the commons trace middleware, or
// base unchanged when the resolved level is too low to log anything.
func wrapHTTPLogging(base http.RoundTripper) http.RoundTripper {
	cfg, ok := httpTraceConfig()
	if !ok {
		return base
	}
	return middlewares.NewLogger(cfg)(base)
}

// httpTraceConfig maps the effective HTTP log level onto commons' trace ladder,
// which is relative to a base level (Debug by default, overridable with
// -Phttp.log.base-level or HTTP_LOG_BASE_LEVEL):
//
//	warn and below  nothing is installed
//	info            failed requests only (status >= 400 or transport error)
//	-v              an access line per request
//	-vv             + request/response headers, query and form params
//	-vvv            + request bodies, TLS summary
//	-vvvv           + response bodies
func httpTraceConfig() (commonshttp.TraceConfig, bool) {
	cfg, ok := commonshttp.TraceConfigForLogLevel(httpTraceLevel())
	if !ok {
		return cfg, false
	}
	// commons redacts these already via CommonRedactedHeaders ("*-Key"); naming
	// the provider credential headers keeps that guarantee explicit, since the
	// Anthropic and Gemini APIs authenticate with them rather than Bearer.
	cfg.RedactedHeaders = append(cfg.RedactedHeaders, "x-api-key", "x-goog-api-key")
	return cfg, true
}

// httpTraceLevel resolves the level the ladder is built from. Named loggers
// take their level from --log-level/-Plog.level.http, while the -v count is
// applied to the global logger, so the effective level is the more verbose of
// the two.
func httpTraceLevel() logger.LogLevel {
	level := logger.GetLogger("http").GetLevel()
	if root := logger.StandardLogger().GetLevel(); root > level {
		level = root
	}
	return level
}
