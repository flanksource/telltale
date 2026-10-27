package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/flanksource/clicky/route"
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/internal/rules"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/spf13/cobra"
)

type HandlerOptions struct {
	Assets         fs.FS
	Root           *cobra.Command
	Version        string
	WorkDir        string
	RequireAPIAuth bool
	// Traces serves the run stores at TracesMount.
	Traces http.Handler
	// Top serves the live process groups at TopMount.
	Top   http.Handler
	Rules *rules.Service
}

func NewHandler(options HandlerOptions) (http.Handler, error) {
	if options.Root == nil || options.Assets == nil || options.Traces == nil || options.Top == nil {
		return nil, fmt.Errorf("command root, asset filesystem, trace mount and top mount are required")
	}
	if _, err := fs.ReadFile(options.Assets, "index.html"); err != nil {
		return nil, fmt.Errorf("web assets unavailable; run make build or serve --dev: %w", err)
	}
	return routes(options, assetHandler(options.Assets)), nil
}

func routes(options HandlerOptions, ui http.Handler) http.Handler {
	api := http.NewServeMux()
	rpc.NewSwaggerServer(&rpc.ServeConfig{Version: options.Version}, options.Root,
		&rpc.OpenAPIConfig{Title: "Telltale", Version: options.Version, Extensions: []func(*rpc.OpenAPISpec){addRulesPaths}}).RegisterRoutes(route.NewRouter(api))
	root := http.NewServeMux()
	hub := agentclient.NewHub(agentclient.Default, agentclient.NewTokenStore(options.WorkDir))
	root.HandleFunc("/api/agents", hub.List)
	root.Handle("/api/agents/connect", hub)
	root.Handle(TracesMount, options.Traces)
	root.Handle(TracesMount+"/", options.Traces)
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
	if !options.RequireAPIAuth {
		return root
	}
	protected := hub.Authorize(apitoken.ScopeAPI, root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/")) && r.URL.Path != "/api/agents/connect" {
			protected.ServeHTTP(w, r)
			return
		}
		root.ServeHTTP(w, r)
	})
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
		if errors.Is(err, fs.ErrNotExist) && (!strings.Contains(path, ".") || strings.HasPrefix(path, "top/")) && !strings.HasPrefix(path, "assets/") {
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
