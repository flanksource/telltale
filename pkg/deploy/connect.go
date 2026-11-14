package deploy

import (
	"errors"
	"fmt"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/deploy/auth"
)

// The resource attributes a connected target carries, which a capture's
// target selector matches.
const (
	AttributeDeployer  = "telltale.deployer"
	AttributeTarget    = "telltale.target.id"
	AttributeNamespace = "k8s.namespace.name"
	AttributePod       = "k8s.pod.name"
	AttributeContainer = "k8s.container.name"
)

// SigningKeySecret is the named secret of the spec.auth.issuer connection
// that holds the RSA signing key, PEM encoded.
const SigningKeySecret = "signingKey"

// NewIssuer is the issuer spec.auth names: its connection's signingKey
// secret signs, and its reference is the tokens' iss.
func NewIssuer(ctx dbcontext.Context, spec AuthSpec) (*auth.Issuer, error) {
	if spec.Issuer == "" {
		return nil, errors.New("spec.auth.issuer is required")
	}
	connection, err := ctx.HydrateConnectionByURL(spec.Issuer)
	if err != nil {
		return nil, fmt.Errorf("issuer %s: %w", spec.Issuer, err)
	}
	if connection == nil {
		return nil, fmt.Errorf("issuer %s: connection not found", spec.Issuer)
	}
	pem, err := connection.Secret(SigningKeySecret)
	if err != nil {
		return nil, fmt.Errorf("issuer %s: %w", spec.Issuer, err)
	}
	key, err := auth.ParsePrivateKey([]byte(pem))
	if err != nil {
		return nil, fmt.Errorf("issuer %s: %w", spec.Issuer, err)
	}
	return auth.NewIssuer(spec.Issuer, key)
}

// Connect dials every target of status over direct HTTP and adds it to
// registry, named by its target id and carrying its deployer, namespace, pod
// and container, so a capture selects a deployed target and never a URL.
// Every target must be deployed.
func Connect(ctx dbcontext.Context, deployer Deployer, status Status, registry *agentclient.Registry, tokens agentclient.TokenSource) ([]*agentclient.Agent, error) {
	container, err := deployer.Spec.JavaContainer.Container()
	if err != nil {
		return nil, fmt.Errorf("deployer %s: %w", deployer.Name, err)
	}
	var agents []*agentclient.Agent
	for _, target := range status.Targets {
		agent, err := connect(ctx, deployer.Name, container, target, tokens)
		if err == nil {
			err = registry.Add(agent)
		}
		if err != nil {
			return nil, errors.Join(fmt.Errorf("connect %s: %w", target.Target, err), closeAll(agents))
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

func connect(ctx dbcontext.Context, deployer string, container JavaContainer, target TargetStatus, tokens agentclient.TokenSource) (*agentclient.Agent, error) {
	if target.State != StateDeployed {
		return nil, fmt.Errorf("target is %s: %s", target.State, target.Error)
	}
	client, err := container.Client(ctx, target.Namespace)
	if err != nil {
		return nil, err
	}
	url, err := ctx.ResolveConnectionURL(target.Endpoint, models.ConnectionTypeHTTP, target.Namespace)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", target.Endpoint, err)
	}
	resource := map[string]string{AttributeDeployer: deployer, AttributeTarget: target.Target, AttributeNamespace: target.Namespace, AttributePod: target.Pod, AttributeContainer: target.Container}
	return agentclient.DialHTTP(ctx, agentclient.HTTPEndpoint{ID: target.Target, URL: url, Client: client, Tokens: tokens, Resource: resource})
}

func closeAll(agents []*agentclient.Agent) error {
	var errs []error
	for _, agent := range agents {
		errs = append(errs, agent.Close())
	}
	return errors.Join(errs...)
}
