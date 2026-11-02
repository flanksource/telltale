package pgproxy

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

const (
	defaultConnection    = "proxy"
	defaultMaxStatements = 20000
	driver               = "postgres"
)

// dsnEnvKeys are the variables a capture falls back to when `dsn` is unset, in
// preference order.
var dsnEnvKeys = []string{"GAVEL_DB_DSN", "DATABASE_URL"}

// defaultListen is the address a proxy binds when its options name none: a
// free loopback port.
const defaultListen = "127.0.0.1:0"

// Options choose the postgres the proxy forwards to and what it records.
type Options struct {
	// DSN is the upstream postgres://, with $VAR references expanded. Empty
	// falls back to GAVEL_DB_DSN, then DATABASE_URL.
	DSN string `json:"dsn,omitempty" clicky:"title=Upstream DSN,desc=The postgres:// URL to proxy with $VAR references expanded; empty reads GAVEL_DB_DSN then DATABASE_URL"`
	// Env names the variable a process reads its DSN from, such as FIN_DB_URL.
	// When DSN is unset the upstream is read from it, and the proxied DSN is
	// exported under it so a process that reads only that variable is observed.
	Env string `json:"env,omitempty" clicky:"title=DSN variable,desc=Variable the command reads its DSN from; the upstream is read from it and the proxied DSN exported under it"`
	// Params keeps bind parameter values in each statement's args. Off by
	// default: bind values carry row data, the likeliest place for a secret.
	Params bool `json:"params,omitempty" clicky:"title=Capture bind values,desc=Keep bind parameter values which carry row data and may hold secrets"`
	// Connection labels the statements in the SQL connection column.
	Connection string `json:"connection,omitempty" clicky:"title=Connection label,desc=Label the statements are recorded under"`
	// MaxStatements bounds what one capture stores; reaching it marks the
	// capture truncated rather than growing without limit.
	MaxStatements int `json:"maxStatements,omitempty" clicky:"title=Maximum statements,desc=Most statements one capture stores before it is marked truncated"`
	// Listen is the host:port the proxy binds; empty is a free loopback port.
	// A port another process holds fails the start.
	Listen string `json:"listen,omitempty" clicky:"title=Listen,desc=host:port the proxy listens on such as 127.0.0.1:15432; empty takes a free loopback port"`
	// Export, when set, are the only variables the proxied DSN is exported
	// under, in place of GAVEL_DB_DSN, DATABASE_URL and libpq's PG* variables,
	// so several proxies in one run each point the command at their own
	// database. Env is exported either way.
	Export []string `json:"export,omitempty" clicky:"title=Export,desc=The only variables the proxied DSN is exported under instead of GAVEL_DB_DSN DATABASE_URL and the PG* variables"`
}

// Params are a capture's Options on the seam it is armed through.
type Params struct {
	Options
	Target target.Target `json:"target,omitzero"`
}

// Validate is the static check of a proxy's options: it reads no environment,
// so an upstream named by a variable is resolved, and refused when unset, only
// as the capture starts (resolveDSN).
func (p Options) Validate() error {
	var errs []error
	if strings.TrimSpace(p.Connection) == "" {
		errs = append(errs, errors.New("connection must not be blank"))
	}
	if p.MaxStatements < 1 {
		errs = append(errs, fmt.Errorf("maxStatements %d must be at least 1", p.MaxStatements))
	}
	if p.Env != "" {
		if err := query.ValidateEnvName(p.Env); err != nil {
			errs = append(errs, fmt.Errorf("env: %w", err))
		}
	}
	for _, name := range p.Export {
		if err := query.ValidateEnvName(name); err != nil {
			errs = append(errs, fmt.Errorf("export: %w", err))
		}
	}
	if !strings.Contains(p.DSN, "$") && strings.TrimSpace(p.DSN) != "" {
		if _, err := parseDSN(strings.TrimSpace(p.DSN)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(append(errs, tracing.ValidateListen(p.Listen))...)
}

// listenAddress is the address the proxy binds.
func (p Options) listenAddress() string {
	if p.Listen == "" {
		return defaultListen
	}
	return p.Listen
}

// resolveDSN expands `$VAR` references and falls back to the environment when
// the DSN is unset: the named Env, else GAVEL_DB_DSN, then DATABASE_URL. It
// runs as the capture starts, never in Validate, so options validate without
// the environment the capture starts in. It fails loudly rather than
// recording nothing: a proxy with no upstream is the silent-empty-capture
// failure to prevent, and a reference to an unset variable would otherwise
// blank a port to 5432 or the whole DSN to DATABASE_URL — another postgres
// than the one declared. A named Env that is unset is refused for the same
// reason, never replaced by the generic fallbacks.
func resolveDSN(p Options) (*url.URL, error) {
	declared := p.DSN
	if strings.TrimSpace(declared) == "" && p.Env != "" {
		declared = "$" + p.Env
	}
	var unset []string
	raw := strings.TrimSpace(os.Expand(declared, func(name string) string {
		value, ok := os.LookupEnv(name)
		if !ok || value == "" {
			unset = append(unset, "$"+name)
		}
		return value
	}))
	if len(unset) > 0 {
		return nil, fmt.Errorf("sql proxy dsn: %s is not set", strings.Join(unset, ", "))
	}
	if raw == "" {
		for _, key := range dsnEnvKeys {
			if value := strings.TrimSpace(os.Getenv(key)); value != "" {
				raw = value
				break
			}
		}
	}
	if raw == "" {
		return nil, fmt.Errorf("sql proxy needs an upstream — set dsn or one of %s", strings.Join(dsnEnvKeys, ", "))
	}
	return parseDSN(raw)
}

// parseDSN reads an expanded DSN as a postgres:// URL.
func parseDSN(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		// url.Error repeats the raw DSN, password included.
		return nil, errors.New("sql proxy dsn is not a URL")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return nil, fmt.Errorf("sql proxy dsn must be a postgres:// URL, got %q", parsed.Scheme)
	}
	return parsed, nil
}

func upstreamAddr(dsn *url.URL) string {
	host, port := dsn.Hostname(), dsn.Port()
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "5432"
	}
	return net.JoinHostPort(host, port)
}
