package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	dbcontext "github.com/flanksource/commons-db/context"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/deploy/auth"
)

// TargetPrefix starts every target id, so a registry hands those ids to a
// Resolver and every other id to local discovery.
const TargetPrefix = "deployer/"

// ParseTargetID is the deployer, namespace and pod a target id names.
func ParseTargetID(id string) (deployer, namespace, pod string, err error) {
	parts := strings.Split(strings.TrimPrefix(id, TargetPrefix), "/")
	if !strings.HasPrefix(id, TargetPrefix) || len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("target id %q: want deployer/<deployer>/<namespace>/<pod>", id)
	}
	return parts[0], parts[1], parts[2], nil
}

// Resolver connects a stored deployer's target the first time an execution
// names it, minting each request's token with the deployer's issuer.
type Resolver struct {
	Store Store
	// Subject is the principal the tokens name, such as operator@host.
	Subject string
	// Context is the context connection references (the issuer's, a
	// port-forward's) resolve in.
	Context func(context.Context) (dbcontext.Context, error)
	// Issuer is the issuer spec.auth names; nil is NewIssuer.
	Issuer func(dbcontext.Context, AuthSpec) (*auth.Issuer, error)
}

// Resolve connects target id into registry; it is an agentclient.Resolver.
func (r Resolver) Resolve(ctx context.Context, registry *agentclient.Registry, id string) error {
	name, _, _, err := ParseTargetID(id)
	if err != nil {
		return err
	}
	if r.Subject == "" || r.Context == nil {
		return errors.New("deployer resolver: a subject and a context are required")
	}
	deployer, err := r.Store.Load(name)
	if err != nil {
		return err
	}
	var target *TargetStatus
	for i := range deployer.Status.Targets {
		if deployer.Status.Targets[i].Target == id {
			target = &deployer.Status.Targets[i]
		}
	}
	if target == nil {
		return fmt.Errorf("deployer %s has no deployed target %s: run telltale jvm deploy for its current pods", name, id)
	}
	dbctx, err := r.Context(ctx)
	if err != nil {
		return err
	}
	if dbctx, err = ClusterOf(*deployer).Bind(dbctx); err != nil {
		return fmt.Errorf("deployer %s: %w", name, err)
	}
	newIssuer := r.Issuer
	if newIssuer == nil {
		newIssuer = NewIssuer
	}
	issuer, err := newIssuer(dbctx, deployer.Spec.Auth)
	if err != nil {
		return fmt.Errorf("deployer %s: %w", name, err)
	}
	tokens := auth.Tokens{Issuer: issuer, Subject: r.Subject, TTL: auth.MaxTTL}
	_, err = Connect(dbctx, *deployer, Status{Targets: []TargetStatus{*target}}, registry, tokens)
	return err
}
