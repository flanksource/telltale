package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	dbcontext "github.com/flanksource/commons-db/context"
	"k8s.io/client-go/kubernetes"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/deploy/auth"
)

// Provision is what every deployed agent is given to verify its executions,
// and how a deploy runs commands in a target's container.
type Provision struct {
	Issuer  string
	JWKS    auth.JWKS
	JWKSURL string
	Exec    ExecFunc
	Upgrade bool
}

// ExecFunc runs commands in a target's JVM container.
type ExecFunc func(Target) agentinstall.Transport

// Options are a deploy's cluster and signing key.
type Options struct {
	Kubernetes kubernetes.Interface
	Issuer     *auth.Issuer
	// Cluster is the cluster Kubernetes reaches; nil Exec runs kubectl exec
	// against it.
	Cluster Cluster
	// Exec runs commands in a target's container; nil is Cluster.Exec().
	Exec ExecFunc
	// DryRun only selects the targets, each StateSelected: it installs
	// nothing and needs no Issuer.
	DryRun  bool
	Upgrade bool
}

// Deploy installs the agent into every target the deployer selects and
// reports each one. It is idempotent: a target already running this agent API
// with this provision is left alone. The error names every target that is
// not deployed. A dry run reports the targets it would deploy to.
func Deploy(ctx dbcontext.Context, deployer Deployer, options Options) (Status, error) {
	if options.DryRun {
		return selected(ctx, deployer, options.Kubernetes)
	}
	container, provision, err := prepare(deployer, options)
	if err != nil {
		return Status{}, err
	}
	targets, err := Resolve(ctx, options.Kubernetes, deployer.Name, deployer.Spec.Selector)
	if err != nil {
		return Status{}, fmt.Errorf("deployer %s: %w", deployer.Name, err)
	}
	// Published only once the selector picks pods to provision with it.
	if provision.JWKS, err = AgentJWKS(ctx, options.Kubernetes, deployer, options.Issuer); err != nil {
		return Status{}, fmt.Errorf("deployer %s: %w", deployer.Name, err)
	}
	var status Status
	var failures []error
	for _, target := range targets {
		result := container.Deploy(ctx, target, provision)
		result.Target, result.Namespace, result.Pod, result.Container = target.ID, target.Pod.Namespace, target.Pod.Name, target.Container.Name
		status.Targets = append(status.Targets, result)
		if result.State != StateDeployed {
			failures = append(failures, fmt.Errorf("%s %s: %s", target.ID, result.State, result.Error))
		}
	}
	if len(failures) > 0 {
		return status, fmt.Errorf("deployer %s: %w", deployer.Name, errors.Join(failures...))
	}
	return status, nil
}

// selected is every target the deployer selects, each StateSelected, once its
// java container is known to be deployable.
func selected(ctx dbcontext.Context, deployer Deployer, client kubernetes.Interface) (Status, error) {
	if client == nil {
		return Status{}, fmt.Errorf("deployer %s: a kubernetes client is required", deployer.Name)
	}
	if _, err := deployer.Spec.JavaContainer.Container(); err != nil {
		return Status{}, fmt.Errorf("deployer %s: %w", deployer.Name, err)
	}
	if source := deployer.Spec.Auth.JWKS; source != nil {
		if err := source.validate(); err != nil {
			return Status{}, fmt.Errorf("deployer %s: %w", deployer.Name, err)
		}
	}
	targets, err := Resolve(ctx, client, deployer.Name, deployer.Spec.Selector)
	if err != nil {
		return Status{}, fmt.Errorf("deployer %s: %w", deployer.Name, err)
	}
	var status Status
	for _, target := range targets {
		status.Targets = append(status.Targets, TargetStatus{Target: target.ID, Namespace: target.Pod.Namespace, Pod: target.Pod.Name, Container: target.Container.Name, State: StateSelected})
	}
	return status, nil
}

func prepare(deployer Deployer, options Options) (JavaContainer, Provision, error) {
	switch {
	case options.Kubernetes == nil:
		return nil, Provision{}, fmt.Errorf("deployer %s: a kubernetes client is required", deployer.Name)
	case options.Issuer == nil:
		return nil, Provision{}, fmt.Errorf("deployer %s: an issuer is required", deployer.Name)
	case deployer.Spec.Auth.Issuer == "":
		return nil, Provision{}, fmt.Errorf("deployer %s: spec.auth.issuer is required", deployer.Name)
	case options.Issuer.Name() != deployer.Spec.Auth.Issuer:
		return nil, Provision{}, fmt.Errorf("deployer %s: issuer %q does not sign for spec.auth.issuer %q", deployer.Name, options.Issuer.Name(), deployer.Spec.Auth.Issuer)
	}
	container, err := deployer.Spec.JavaContainer.Container()
	if err != nil {
		return nil, Provision{}, fmt.Errorf("deployer %s: %w", deployer.Name, err)
	}
	exec := options.Exec
	if exec == nil {
		exec = options.Cluster.Exec()
	}
	if options.Upgrade && deployer.Spec.JavaContainer.Kind != KindAttach {
		return nil, Provision{}, errors.New("--upgrade replaces attached Java runtimes; select an attach deployer")
	}
	return container, Provision{Issuer: options.Issuer.Name(), JWKS: options.Issuer.JWKS(), JWKSURL: deployer.Spec.Auth.JWKSURL, Exec: exec, Upgrade: options.Upgrade}, nil
}

// adapterConfig is the agent configuration a target is provisioned with,
// with its digest: the agent echoes the digest, so a deploy can tell whether
// the agent it finds was given this provision.
func adapterConfig(target Target, provision Provision, extra map[string]string) (map[string]string, error) {
	jwks, err := json.Marshal(provision.JWKS)
	if err != nil {
		return nil, err
	}
	config := map[string]string{"issuer": provision.Issuer, "audience": target.ID, "jwks": string(jwks)}
	if provision.JWKSURL != "" {
		config["jwksUrl"] = provision.JWKSURL
	}
	for key, value := range extra {
		config[key] = value
	}
	canonical, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	config["configDigest"] = hex.EncodeToString(sum[:])
	return config, nil
}

// presence is what a target's health says about the agent already there.
type presence int

const (
	absent   presence = iota // nothing answers at the endpoint
	current                  // this agent API with this provision
	stale                    // this agent API with another provision
	outdated                 // another agent API
)

// inspect reads a target's /health against the provision's digest.
func inspect(ctx context.Context, client *http.Client, target Target, endpoint, digest string) (presence, agentclient.Health, error) {
	health, err := agentclient.HTTPClient{Target: target.ID, URL: endpoint, Client: client, Tokens: noTokens{}}.Health(ctx)
	switch {
	case err != nil:
		return absent, health, err
	case health.Status == "outdated" || health.APIVersion != agentclient.AgentAPIVersion:
		return outdated, health, nil
	case health.Status != "ok":
		return absent, health, fmt.Errorf("agent %s health status %q", target.ID, health.Status)
	case health.ConfigDigest != digest:
		return stale, health, nil
	}
	return current, health, nil
}

// noTokens backs the unauthenticated health check; asking it is a bug.
type noTokens struct{}

func (noTokens) Token(context.Context, string, string) (string, error) {
	return "", errors.New("the health check carries no token")
}

func outdatedStatus(endpoint string, health agentclient.Health) TargetStatus {
	message := health.Error
	if message == "" {
		message = fmt.Sprintf("agent API %d is loaded; this deployer requires %d; restart the JVM to replace it", health.APIVersion, agentclient.AgentAPIVersion)
	}
	return TargetStatus{State: StateOutdated, AgentAPIVersion: health.APIVersion, Endpoint: endpoint, Error: message}
}

func failed(endpoint string, err error) TargetStatus {
	return TargetStatus{State: StateFailed, Endpoint: endpoint, Error: err.Error()}
}
