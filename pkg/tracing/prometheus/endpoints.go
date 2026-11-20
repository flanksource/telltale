package prometheus

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/kubernetes"
	"github.com/flanksource/commons-db/models"
)

type endpoint struct {
	original string
	url      *url.URL
}

func resolveEndpoints(ctx dbcontext.Context, requested []string) (_ []endpoint, _ func() error, err error) {
	forwards := kubernetes.NewForwardManager(0)
	var relay scrapeRelay
	release := sync.OnceValue(func() error {
		err := relay.close()
		forwards.CloseAll()
		return err
	})
	defer func() {
		if err != nil {
			err = errors.Join(err, release())
		}
	}()
	resolveCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx = ctx.Wrap(resolveCtx).WithPortForwardManager(forwards)
	endpoints := make([]endpoint, 0, len(requested))
	for _, raw := range requested {
		u, err := parseEndpoint(raw)
		if err != nil {
			return nil, nil, err
		}
		if u.Scheme == portForwardScheme || u.Scheme == proxyScheme {
			u, err = resolveWorkloadEndpoint(ctx, u, &relay)
			if err != nil {
				return nil, nil, fmt.Errorf("resolve metrics endpoint %q: %w", raw, err)
			}
		}
		endpoints = append(endpoints, endpoint{original: raw, url: u})
	}
	return endpoints, release, nil
}

func resolveWorkloadEndpoint(ctx dbcontext.Context, u *url.URL, relay *scrapeRelay) (*url.URL, error) {
	resolved, err := ctx.ResolveConnectionURL(u.String(), models.ConnectionTypeHTTP, "")
	if err != nil {
		return nil, err
	}
	target, err := parseEndpoint(resolved)
	if err != nil {
		return nil, err
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, errors.New("the Kubernetes workload did not resolve to an HTTP(S) endpoint")
	}
	if u.Scheme == proxyScheme {
		return relay.route(target)
	}
	return target, nil
}
