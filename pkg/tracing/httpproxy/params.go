package httpproxy

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"

	"github.com/flanksource/commons/properties"
)

// Mode is how much of the traffic through the proxy a capture sees.
type Mode string

const (
	// ModeConnect records each CONNECT tunnel by its host, duration and byte
	// counts, its payload left encrypted.
	ModeConnect Mode = "connect"
	// ModeMITM terminates TLS with an ephemeral CA the capture exports for its
	// processes to trust, recording each decrypted exchange in full.
	ModeMITM Mode = "mitm"
)

// JSONSchema offers the modes as a choice to a start form.
func (Mode) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []any{ModeConnect, ModeMITM}}
}

// defaultBodies is how much of each body a capture keeps unless its params
// say otherwise: bodies are the likeliest place for both a large payload and
// a secret, so the default stays small enough to read.
const defaultBodies = "256KiB"

// maxBodies is the most of a body a capture may keep.
const maxBodies = 4 << 20

// defaultListen is the address a proxy binds when its options name none: a
// free loopback port.
const defaultListen = "127.0.0.1:0"

// Options configure the recording proxy a capture's processes are pointed at.
// Plain (non-TLS) exchanges are recorded in full in either mode.
type Options struct {
	Mode Mode `json:"mode,omitempty" clicky:"title=Mode,desc=connect records HTTPS tunnels by host; mitm decrypts and records each exchange"`
	// Hosts are host globs (path.Match), without a port, limiting what is
	// recorded and, under mitm, what is decrypted; empty records every host.
	Hosts []string `json:"hosts,omitempty" clicky:"title=Hosts,desc=Host globs without a port to record and decrypt; empty records every host"`
	// Paths are URL-path globs (path.Match) limiting which exchanges are
	// recorded; empty records every path. A tunnel has no path to match, so
	// it is recorded on Hosts alone.
	Paths []string `json:"paths,omitempty" clicky:"title=Paths,desc=URL path globs to record; empty records every path"`
	// Bodies caps how much of each body is kept, as a size such as 64KiB or
	// 4MiB, at most 4MiB; 0 keeps none.
	Bodies string `json:"bodies,omitempty" clicky:"title=Bodies,desc=How much of each body to keep such as 64KiB (at most 4MiB; 0 keeps none)"`
	// Redact names headers to blank on top of the built-in credential headers,
	// which are always blanked.
	Redact []string `json:"redact,omitempty" clicky:"title=Redact headers,desc=Headers to blank beside the credential headers that are always blanked"`
	// NoProxy are host:port addresses the processes reach directly, such as a
	// server the host started, beside the proxy itself.
	NoProxy []string `json:"noProxy,omitempty" clicky:"title=No proxy,desc=host:port addresses the command reaches directly"`
	// UpstreamCA is a PEM file of authorities the proxy trusts beside the
	// system roots when, under mitm, it connects to the real server, such as a
	// local test server's own certificate. It is read when the capture starts.
	UpstreamCA string `json:"upstreamCA,omitempty" clicky:"title=Upstream CA file,desc=PEM file of authorities the proxy trusts beside the system roots under mitm"`
	// Listen is the host:port the proxy binds; empty is a free loopback port.
	// A port another process holds fails the start.
	Listen string `json:"listen,omitempty" clicky:"title=Listen,desc=host:port the proxy listens on such as 127.0.0.1:18080; empty takes a free loopback port"`
	// Export renames the variables the proxy is exported under, so two
	// proxies in one run each point the command at their own.
	Export Export `json:"export,omitzero" clicky:"title=Export,desc=Variable names the proxy and its CA are exported under"`
}

// Export names the variables a proxy capture exports. An empty name keeps the
// standard ones: HTTP(S)_PROXY in both cases, NO_PROXY in both cases, and,
// under mitm, the TLS trust variables.
type Export struct {
	// Proxy is the one variable the proxy URL is exported under instead of
	// HTTP_PROXY, http_proxy, HTTPS_PROXY and https_proxy.
	Proxy string `json:"proxy,omitempty" clicky:"title=Proxy variable,desc=Variable the proxy URL is exported under instead of HTTP(S)_PROXY"`
	// NoProxy is the one variable the bypass list is exported under instead
	// of NO_PROXY and no_proxy. With Proxy renamed and NoProxy empty no bypass
	// list is exported: a client reads it only beside the standard proxy names.
	NoProxy string `json:"noProxy,omitempty" clicky:"title=No-proxy variable,desc=Variable the bypass list is exported under instead of NO_PROXY"`
	// CA is the one variable a mitm capture's CA file is exported under
	// instead of the TLS trust variables.
	CA string `json:"ca,omitempty" clicky:"title=CA variable,desc=Variable the mitm CA file is exported under instead of SSL_CERT_FILE and the other trust variables"`
}

// Validate is the static check of a proxy's options: it binds no port and
// reads no file.
func (p Options) Validate() error {
	if p.Mode != ModeConnect && p.Mode != ModeMITM {
		return fmt.Errorf("mode %q is neither %q nor %q", p.Mode, ModeConnect, ModeMITM)
	}
	if _, err := p.bodyLimit(); err != nil {
		return err
	}
	if p.UpstreamCA != "" && strings.TrimSpace(p.UpstreamCA) == "" {
		return errors.New("upstreamCA must not be blank")
	}
	if p.UpstreamCA != "" && p.Mode != ModeMITM {
		return fmt.Errorf("upstreamCA applies only to mode %q: a %q tunnel is verified by its client", ModeMITM, p.Mode)
	}
	if p.Export.CA != "" && p.Mode != ModeMITM {
		return fmt.Errorf("export.ca applies only to mode %q: a %q proxy has no CA to export", ModeMITM, p.Mode)
	}
	return errors.Join(globs("hosts", p.Hosts), globs("paths", p.Paths), names("redact", p.Redact), names("noProxy", p.NoProxy),
		tracing.ValidateListen(p.Listen), p.Export.validate())
}

// listenAddress is the address the proxy binds.
func (p Options) listenAddress() string {
	if p.Listen == "" {
		return defaultListen
	}
	return p.Listen
}

// configured reports whether any proxy-only option is set, which only the
// proxy seam honours.
func (p Options) configured() bool { return p.Listen != "" || p.Export != Export{} }

func (e Export) validate() error {
	var errs []error
	for _, name := range []struct{ field, value string }{{"export.proxy", e.Proxy}, {"export.noProxy", e.NoProxy}, {"export.ca", e.CA}} {
		if name.value == "" {
			continue
		}
		if err := query.ValidateEnvName(name.value); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name.field, err))
		}
	}
	return errors.Join(errs...)
}

// Params are a capture's Options on the seam it is armed through.
type Params struct {
	Options
	Target target.Target `json:"target,omitzero"`
}

// bodyLimit is how many bytes of each body the capture keeps.
func (p Options) bodyLimit() (int64, error) {
	limit, err := properties.ParseBytes(p.Bodies)
	if err != nil {
		return 0, fmt.Errorf("bodies: %w", err)
	}
	if limit < 0 || limit > maxBodies {
		return 0, fmt.Errorf("bodies %q is not between 0 and 4MiB", p.Bodies)
	}
	return limit, nil
}

func globs(field string, patterns []string) error {
	for _, pattern := range patterns {
		if _, err := path.Match(pattern, ""); err != nil || strings.TrimSpace(pattern) == "" {
			return fmt.Errorf("%s: %q is not a glob", field, pattern)
		}
	}
	return nil
}

func names(field string, values []string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s: a name must not be blank", field)
		}
	}
	return nil
}
