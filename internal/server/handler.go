package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/flanksource/clicky/route"
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/commons-db/cmd/query/connections"
	"github.com/flanksource/commons-db/connection/store"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/internal/rules"
	"github.com/flanksource/telltale/internal/selftrace"
	"github.com/flanksource/telltale/internal/tracequery"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/traceserve/records"
	"github.com/spf13/cobra"
)

type HandlerOptions struct {
	Assets         fs.FS
	Root           *cobra.Command
	Version        string
	DataDir        string
	ConfigDirs     []datadir.ConfigDir
	RequireAPIAuth bool
	// Traces serves the run stores at TracesMount.
	Traces http.Handler
	// Top serves the live process groups at TopMount.
	Top         http.Handler
	Rules       *rules.Service
	Connections *store.Store
	OTLP        http.Handler
	SelfTrace   *selftrace.Tracer
}

func NewHandler(options HandlerOptions) (http.Handler, error) {
	if options.Root == nil || options.Assets == nil || options.Traces == nil || options.Top == nil {
		return nil, fmt.Errorf("command root, asset filesystem, trace mount and top mount are required")
	}
	if _, err := fs.ReadFile(options.Assets, "index.html"); err != nil {
		return nil, fmt.Errorf("web assets unavailable; run make build or serve --dev: %w", err)
	}
	return routes(options, assetHandler(options.Assets))
}

func routes(options HandlerOptions, ui http.Handler) (http.Handler, error) {
	if options.Connections == nil {
		var err error
		options.Connections, err = telltale.OpenConnections()
		if err != nil {
			return nil, err
		}
	}
	connectionAPI, err := connectionHandler(options.Connections)
	if err != nil {
		return nil, err
	}
	api := http.NewServeMux()
	rpc.NewSwaggerServer(&rpc.ServeConfig{Version: options.Version}, options.Root,
		&rpc.OpenAPIConfig{Title: "Telltale", Version: options.Version, Extensions: []func(*rpc.OpenAPISpec){addRulesPaths, connections.AddConnectionsOpenAPI, addConnectionPaths, addOtelPaths, addReceiverPath}}).RegisterRoutes(route.NewRouter(api))
	root := http.NewServeMux()
	if options.OTLP == nil {
		options.OTLP = http.NotFoundHandler()
	}
	root.Handle("/v1/traces", options.OTLP)
	installer := http.NewServeMux()
	rpc.NewSwaggerServer(&rpc.ServeConfig{Version: options.Version, Executor: &rpc.ExecutorConfig{Enabled: true}}, options.Root, nil).
		RegisterExecutionRoutes(route.NewRouter(installer))
	root.Handle("/api/v1/agents/install/java", http.NewCrossOriginProtection().Handler(installer))
	hub := agentclient.NewHub(agentclient.Default, agentclient.NewTokenStore(options.ConfigDirs))
	root.HandleFunc("/api/agents", hub.List)
	otel := otelHandler(tracequery.Service{DataDir: options.DataDir, Connections: options.Connections})
	root.Handle("/api/otel", otel)
	root.Handle("/api/otel/", otel)
	root.Handle("/api/v1/connection", connectionAPI)
	root.Handle("/api/v1/connection/", connectionAPI)
	root.Handle("/api/v1/connections/", connectionAPI)
	root.Handle("/api/agents/connect", hub)
	root.Handle(TracesMount, options.Traces)
	root.Handle(TracesMount+"/", options.Traces)
	root.Handle("GET "+TracesMount+"/{run}/traces/{kind}/records", records.Handler(options.DataDir))
	root.Handle(TopMount, options.Top)
	root.Handle(TopProcessesPath, options.Top)
	root.Handle(TopFacetsPath, options.Top)
	root.Handle(TopActionPath, options.Top)
	root.Handle(RulesMount, rulesHandler{service: options.Rules})
	root.Handle(RulesMount+"/", rulesHandler{service: options.Rules})
	root.Handle("/api/", api)
	root.Handle("/api", http.NotFoundHandler())
	root.Handle("/health", api)
	root.Handle("/", ui)
	var handler http.Handler = root
	if options.RequireAPIAuth {
		protected := hub.Authorize(apitoken.ScopeAPI, root)
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/")) && r.URL.Path != "/api/agents/connect" {
				protected.ServeHTTP(w, r)
				return
			}
			root.ServeHTTP(w, r)
		})
	}
	if options.SelfTrace != nil {
		handler = options.SelfTrace.Handler(handler)
	}
	return handler, nil
}

func assetHandler(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		info, err := fs.Stat(assets, path)
		// A client route is the page: any path without an extension, and the
		// /top/<program> pages, whose program names may hold dots.
		if errors.Is(err, fs.ErrNotExist) && (!strings.Contains(path, ".") || strings.HasPrefix(path, "top/") || strings.HasPrefix(path, "arm/") || strings.HasPrefix(path, "records/")) && !strings.HasPrefix(path, "assets/") {
			path = "index.html"
		} else if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if path == "index.html" {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}
