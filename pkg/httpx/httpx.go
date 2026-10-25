// Package httpx is the one place telltale builds an http.Client. Going through
// it is what lets the `http` trace kind observe telltale's own outbound calls:
// each client's transport is tapped under the commons-db HTTP observability
// feature Feature, which a capture observes only while it runs.
package httpx

import (
	"net/http"
	"time"

	"github.com/flanksource/commons-db/observability"
)

// Feature is the commons-db HTTP observability feature telltale's clients are
// tapped under.
const Feature = "telltale"

// Shared is the default client for telltale's own outbound calls. It has no
// timeout, matching http.DefaultClient, so callers that need one use Client.
var Shared = Client(0)

// Client returns an observable client with the given timeout. Zero means none.
//
// The transport is tapped unconditionally: whether an exchange is captured is
// decided per request by whether an `http` trace observes Feature, so a client
// built at init costs one lookup per request for the runs that trace nothing.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: observability.ApplyHTTPObservability(nil, Feature, http.DefaultTransport, nil)}
}
