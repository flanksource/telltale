package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/flanksource/clicky/route"
	"github.com/flanksource/clicky/rpc"
	"github.com/spf13/cobra"
)

type HandlerOptions struct {
	Assets  fs.FS
	Root    *cobra.Command
	Version string
	// Traces serves the run stores at TracesMount.
	Traces http.Handler
}

func NewHandler(options HandlerOptions) (http.Handler, error) {
	if options.Root == nil || options.Assets == nil || options.Traces == nil {
		return nil, fmt.Errorf("command root, asset filesystem and trace mount are required")
	}
	if _, err := fs.ReadFile(options.Assets, "index.html"); err != nil {
		return nil, fmt.Errorf("web assets unavailable; run make build or serve --dev: %w", err)
	}
	return routes(options, assetHandler(options.Assets)), nil
}

func routes(options HandlerOptions, ui http.Handler) http.Handler {
	api := http.NewServeMux()
	rpc.NewSwaggerServer(&rpc.ServeConfig{Version: options.Version}, options.Root,
		&rpc.OpenAPIConfig{Title: "Telltale", Version: options.Version}).RegisterRoutes(route.NewRouter(api))
	root := http.NewServeMux()
	root.Handle(TracesMount, options.Traces)
	root.Handle(TracesMount+"/", options.Traces)
	root.Handle("/api/", api)
	root.Handle("/api", http.NotFoundHandler())
	root.Handle("/health", api)
	root.Handle("/", ui)
	return root
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
		if errors.Is(err, fs.ErrNotExist) && !strings.Contains(path, ".") && !strings.HasPrefix(path, "assets/") {
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
