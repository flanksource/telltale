package prometheus

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/flanksource/commons-db/kubernetes"
	"github.com/flanksource/telltale/internal/lifetime"
)

type scrapeRelay struct {
	server  *http.Server
	service *lifetime.Service
	mux     *http.ServeMux
	address string
}

func (r *scrapeRelay) route(upstream *url.URL) (*url.URL, error) {
	identity, err := kubernetes.APIServerProxyFor(upstream)
	if err != nil {
		return nil, err
	}
	if identity == nil {
		return nil, errors.New("no Kubernetes identity registered for the resolved API server proxy URL")
	}
	transport, err := identity.Transport()
	if err != nil {
		return nil, err
	}
	if r.server == nil {
		if err := r.start(); err != nil {
			return nil, err
		}
	}
	path := "/scrape/" + rand.Text()
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			fixed := *upstream
			request.Out.URL, request.Out.Host = &fixed, fixed.Host
			request.Out.Header = make(http.Header)
			for _, name := range []string{"Accept", "Accept-Encoding", "User-Agent", "X-Prometheus-Scrape-Timeout-Seconds"} {
				for _, value := range request.In.Header.Values(name) {
					request.Out.Header.Add(name, value)
				}
			}
			request.Out.Body, request.Out.ContentLength = nil, 0
			request.Out.TransferEncoding, request.Out.Trailer = nil, nil
		},
	}
	r.mux.HandleFunc(path, func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "only GET is allowed", http.StatusMethodNotAllowed)
			return
		}
		proxy.ServeHTTP(w, request)
	})
	return &url.URL{Scheme: "http", Host: r.address, Path: path}, nil
}

func (r *scrapeRelay) start() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for Kubernetes proxy scrapes: %w", err)
	}
	r.address = listener.Addr().String()
	r.mux = http.NewServeMux()
	r.server = &http.Server{Handler: r.mux, ReadHeaderTimeout: 5 * time.Second}
	r.service = lifetime.Start("Kubernetes scrape relay", func() error {
		if err := r.server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	return nil
}

func (r *scrapeRelay) close() error {
	if r.server == nil {
		return nil
	}
	return errors.Join(r.server.Close(), r.service.Wait())
}
