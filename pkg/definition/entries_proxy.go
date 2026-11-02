package definition

import (
	"errors"
	"net"
	"slices"
	"strconv"

	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/pgproxy"
)

// ProxyHTTP is a recording HTTP proxy the command is routed through.
type ProxyHTTP struct {
	Named
	httpproxy.Options
}

func (p *ProxyHTTP) options(Located) (any, any, error) { return p.Options, nil, nil }

// exports are the variables the proxy points the command at it with.
func (p *ProxyHTTP) exports() []string { return p.ExportNames() }

func (p *ProxyHTTP) listen() string { return p.Listen }

// ProxyPostgres is a Postgres proxy in front of the DSN the command reads
// from Env, exporting the proxied DSN under Env alone.
type ProxyPostgres struct {
	Named
	pgproxy.Options
}

// options export the proxied DSN under Env and the variables Export names,
// never the standard ones, so several postgres proxies each keep their own.
func (p *ProxyPostgres) options(Located) (any, any, error) {
	if p.Env == "" {
		return nil, nil, errors.New("env is required: the variable the command reads its DSN from, and the proxied DSN is exported under")
	}
	options := p.Options
	options.Export = p.exports()
	return options, nil, nil
}

func (p *ProxyPostgres) exports() []string {
	if slices.Contains(p.Export, p.Env) {
		return slices.Clone(p.Export)
	}
	return append(slices.Clone(p.Export), p.Env)
}

func (p *ProxyPostgres) listen() string { return p.Listen }

// proxyEntry is a proxy: the address it binds and the variables it exports.
type proxyEntry interface {
	listen() string
	exports() []string
}

// fixedPort is the port listen names, 0 for a free one or none.
func fixedPort(listen string) (int, error) {
	if listen == "" {
		return 0, nil
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(port)
}
