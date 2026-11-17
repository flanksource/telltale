package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/commons-db/connection"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"

	"github.com/flanksource/telltale/pkg/agentinstall"
)

// KindAttach copies the agent into the container and attaches it to a
// running JVM with kubectl exec, starting its HTTP listener.
const KindAttach = "attach"

// AttachSpec is an attach javaContainer. url is a template rendered per
// target to the listener's base URL, e.g.
// portforward://{{.Pod.Name}}.{{.Pod.Namespace}}:{{.Port "telltale"}}?kind=pod.
type AttachSpec struct {
	Kind          string `json:"kind"`
	URL           string `json:"url"`
	PID           int    `json:"pid"`
	Directory     string `json:"directory"`
	Bind          string `json:"bind"`
	Port          int    `json:"port"`
	Service       string `json:"service"`
	JavaHome      string `json:"javaHome,omitempty"`
	ExtensionsDir string `json:"extensionsDir,omitempty"`
	AllowMutation bool   `json:"allowMutation,omitempty"`
}

func init() {
	RegisterJavaContainer(KindAttach, func(raw json.RawMessage) (JavaContainer, error) {
		var spec AttachSpec
		if err := decodeStrict(raw, &spec); err != nil {
			return nil, err
		}
		return newAttach(spec)
	})
}

type attach struct{ spec AttachSpec }

func newAttach(spec AttachSpec) (*attach, error) {
	switch {
	case spec.URL == "":
		return nil, errors.New("url (the listener URL template) is required")
	case spec.PID < 0:
		return nil, errors.New("pid of the JVM in the container must be non-negative; omit it to discover the JVM")
	case !strings.HasPrefix(spec.Directory, "/"):
		return nil, fmt.Errorf("directory %q must be absolute", spec.Directory)
	case spec.Bind == "":
		return nil, errors.New("bind (the listener address in the container) is required")
	case spec.Port <= 0 || spec.Port > 65535:
		return nil, fmt.Errorf("port %d must be a fixed listener port the url reaches", spec.Port)
	case spec.Service == "":
		return nil, errors.New("service is required")
	}
	return &attach{spec: spec}, nil
}

// Client reaches the listener with plain HTTP; the token is the auth.
func (a *attach) Client(dbcontext.Context, string) (*http.Client, error) {
	return listenerClient(), nil
}

// listenerClient reaches a listener directly, over a port-forward, or through
// the apiserver proxy, whose credentials the connection transport adds.
func listenerClient() *http.Client {
	return &http.Client{Transport: connection.HTTPConnection{}.Transport(), Timeout: time.Minute}
}

// settings are what the attached agent is configured with beside its
// listener's auth; they count in the digest, as the agent cannot change
// them without a JVM restart.
func (a *attach) settings() map[string]string {
	return map[string]string{"bind": a.spec.Bind, "port": strconv.Itoa(a.spec.Port), "pid": strconv.Itoa(a.spec.PID), "service": a.spec.Service,
		"allowMutation": strconv.FormatBool(a.spec.AllowMutation), "extensionsDir": a.spec.ExtensionsDir, "directory": a.spec.Directory}
}

func (a *attach) Deploy(ctx dbcontext.Context, target Target, provision Provision) TargetStatus {
	endpoint, err := RenderURL(a.spec.URL, target)
	if err != nil {
		return failed("", err)
	}
	resolved, err := ctx.ResolveConnectionURL(endpoint, models.ConnectionTypeHTTP, target.Pod.Namespace)
	if err != nil {
		return failed(endpoint, fmt.Errorf("resolve %s: %w", endpoint, err))
	}
	config, err := adapterConfig(target, provision, a.settings())
	if err != nil {
		return failed(endpoint, err)
	}
	client := listenerClient()
	found, health, _ := inspect(ctx, client, target, resolved, config["configDigest"])
	switch found {
	case current:
		return TargetStatus{State: StateDeployed, AgentAPIVersion: health.APIVersion, Endpoint: endpoint}
	case outdated:
		return outdatedStatus(endpoint, health)
	case stale:
		return failed(endpoint, errors.New("an agent attached with another provision answers; an attached agent cannot be replaced, restart the JVM to re-provision it"))
	}
	if err := a.install(ctx, target, config, provision); err != nil {
		return failed(endpoint, err)
	}
	return confirm(ctx, client, target, endpoint, func() (string, error) {
		return ctx.ResolveConnectionURL(endpoint, models.ConnectionTypeHTTP, target.Pod.Namespace)
	}, config["configDigest"])
}

func (a *attach) install(ctx dbcontext.Context, target Target, config map[string]string, provision Provision) error {
	listener := agentinstall.HTTPListener{Bind: a.spec.Bind, Port: a.spec.Port, Issuer: config["issuer"], Audience: config["audience"], JWKS: json.RawMessage(config["jwks"]), JWKSURL: config["jwksUrl"], ConfigDigest: config["configDigest"]}
	destination := agentinstall.Destination{Kind: "kubernetes", Namespace: target.Pod.Namespace, Pod: target.Pod.Name, Container: target.Container.Name}
	installer := agentinstall.Installer{Runtime: agentinstall.Java{}, Transport: provision.Exec(target)}
	_, err := installer.Install(ctx, agentinstall.Options{Destination: destination, PID: a.spec.PID, Directory: a.spec.Directory, Service: a.spec.Service, AllowMutation: a.spec.AllowMutation, ExtensionsDir: a.spec.ExtensionsDir, JavaHome: a.spec.JavaHome, HTTP: &listener})
	if err != nil {
		return fmt.Errorf("attach agent to pid %d in %s: %w", a.spec.PID, target.ID, err)
	}
	return nil
}
