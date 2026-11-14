package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/commons-db/connection"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"

	javaagent "github.com/flanksource/telltale/pkg/agentinstall/java"
)

// KindTomcat hot-deploys telltale-diag.war through the Tomcat manager.
const KindTomcat = "tomcat"

// The paths a tomcat javaContainer takes when it names none.
const (
	DefaultManagerPath = "/manager"
	DefaultContextPath = "/telltale-diag"
)

// TomcatSpec is a tomcat javaContainer. Its embedded HTTPConnection is the
// manager login (a connection reference, basic auth, bearer, OAuth and TLS).
//
// url is a template rendered per target to the Tomcat base URL the deploy
// uses: the manager upload and the /health probe. It must reach Tomcat with
// its Authorization header intact, so it is a portforward:// or direct
// http(s) URL, never the apiserver proxy, which deletes Authorization from
// every request it proxies.
//
// executionUrl is the Tomcat base URL executions use, defaulting to url. It
// may be a proxy:// URL: executions carry their token in
// X-Telltale-Authorization, which the apiserver forwards.
type TomcatSpec struct {
	Kind string `json:"kind"`
	connection.HTTPConnection
	ExecutionURL  string `json:"executionUrl,omitempty"`
	ManagerPath   string `json:"managerPath,omitempty"`
	ContextPath   string `json:"contextPath,omitempty"`
	RuntimeDir    string `json:"runtimeDir"`
	ExtensionsDir string `json:"extensionsDir,omitempty"`
	AllowMutation bool   `json:"allowMutation,omitempty"`
}

func init() {
	RegisterJavaContainer(KindTomcat, func(raw json.RawMessage) (JavaContainer, error) {
		var spec TomcatSpec
		if err := decodeStrict(raw, &spec); err != nil {
			return nil, err
		}
		return newTomcat(spec)
	})
}

type tomcat struct{ spec TomcatSpec }

// apiServerProxyPath is an apiserver services/proxy or pods/proxy URL path.
var apiServerProxyPath = regexp.MustCompile(`/api/v1/namespaces/[^/]+/(pods|services)/[^/]+/proxy`)

// refuseAPIServerProxy refuses a manager URL the apiserver would proxy.
func refuseAPIServerProxy(template string) error {
	if !strings.HasPrefix(strings.TrimSpace(template), "proxy://") && !apiServerProxyPath.MatchString(template) {
		return nil
	}
	return fmt.Errorf("url %q goes through the apiserver proxy, which deletes the Authorization header from every request it proxies, so the manager login never reaches Tomcat: deploy over portforward://<pod>.<namespace>:<port>?kind=pod (or a direct http(s) URL) and put the proxy:// URL in executionUrl", template)
}

func newTomcat(spec TomcatSpec) (*tomcat, error) {
	if spec.ManagerPath == "" {
		spec.ManagerPath = DefaultManagerPath
	}
	if spec.ContextPath == "" {
		spec.ContextPath = DefaultContextPath
	}
	login := spec.HTTPConnection
	switch {
	case spec.URL == "":
		return nil, errors.New("url (the Tomcat base URL template the deploy uses) is required")
	case !strings.HasPrefix(spec.ContextPath, "/") || spec.ContextPath == "/" || strings.HasSuffix(spec.ContextPath, "/"):
		return nil, fmt.Errorf("contextPath %q must be a non-root path such as %s", spec.ContextPath, DefaultContextPath)
	case !strings.HasPrefix(spec.ManagerPath, "/"):
		return nil, fmt.Errorf("managerPath %q must start with /", spec.ManagerPath)
	case !strings.HasPrefix(spec.RuntimeDir, "/"):
		return nil, fmt.Errorf("runtimeDir %q must be an absolute directory in the JVM's container", spec.RuntimeDir)
	case login.ConnectionName == "" && login.HTTPBasicAuth.IsEmpty() && login.Bearer.IsEmpty() && login.OAuth.IsEmpty():
		return nil, errors.New("a manager login is required: connection, username/password, bearer or oauth")
	}
	if err := refuseAPIServerProxy(spec.URL); err != nil {
		return nil, err
	}
	return &tomcat{spec: spec}, nil
}

// login is the hydrated manager connection; its URL is the template's, not
// the referenced connection's.
func (t *tomcat) login(ctx dbcontext.Context, namespace string) (connection.HTTPConnection, error) {
	login := t.spec.HTTPConnection
	login.URL = ""
	hydrated, err := login.Hydrate(ctx, namespace)
	if err != nil {
		return connection.HTTPConnection{}, fmt.Errorf("hydrate manager login: %w", err)
	}
	return *hydrated, nil
}

// Client reaches the WAR with the manager connection's TLS and no login: an
// execution authenticates with its own token. The connection transport adds
// the apiserver's credentials to an apiserver proxy URL.
func (t *tomcat) Client(ctx dbcontext.Context, namespace string) (*http.Client, error) {
	login, err := t.login(ctx, namespace)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: connection.HTTPConnection{TLS: login.TLS}.Transport(), Timeout: time.Minute}, nil
}

func (t *tomcat) Deploy(ctx dbcontext.Context, target Target, provision Provision) TargetStatus {
	endpoint, err := t.executionEndpoint(target)
	if err != nil {
		return failed("", err)
	}
	base, err := RenderURL(t.spec.URL, target)
	if err != nil {
		return failed(endpoint, err)
	}
	config, err := adapterConfig(target, provision, t.adapterSettings())
	if err != nil {
		return failed(endpoint, err)
	}
	health, err := resolvePath(ctx, base, t.spec.ContextPath, target)
	if err != nil {
		return failed(endpoint, err)
	}
	client, err := t.Client(ctx, target.Pod.Namespace)
	if err != nil {
		return failed(endpoint, err)
	}
	if found, current := t.current(ctx, client, target, health, config); found {
		return TargetStatus{State: StateDeployed, AgentAPIVersion: current, Endpoint: endpoint}
	}
	if err := t.upload(ctx, target, base, config); err != nil {
		return failed(endpoint, err)
	}
	return confirm(ctx, client, target, endpoint, health, config["configDigest"])
}

func (t *tomcat) current(ctx context.Context, client *http.Client, target Target, health string, config map[string]string) (bool, int) {
	found, report, _ := inspect(ctx, client, target, health, config["configDigest"])
	return found == current, report.APIVersion
}

// executionEndpoint is the rendered, unresolved execution URL with the WAR's
// context path: what the status records and Connect resolves again.
func (t *tomcat) executionEndpoint(target Target) (string, error) {
	template := t.spec.ExecutionURL
	if template == "" {
		template = t.spec.URL
	}
	rendered, err := RenderURL(template, target)
	if err != nil {
		return "", err
	}
	return joinPath(rendered, t.spec.ContextPath)
}

func (t *tomcat) adapterSettings() map[string]string {
	settings := map[string]string{"runtimeDir": t.spec.RuntimeDir, "allowMutation": strconv.FormatBool(t.spec.AllowMutation)}
	if t.spec.ExtensionsDir != "" {
		settings["extensionsDir"] = t.spec.ExtensionsDir
	}
	return settings
}

// upload deploys the target's configured WAR with the manager text API,
// replacing any WAR at the context path.
func (t *tomcat) upload(ctx dbcontext.Context, target Target, base string, config map[string]string) error {
	war, err := javaagent.War()
	if err != nil {
		return fmt.Errorf("telltale-diag.war is not embedded; run make build: %w", err)
	}
	if war, err = configureWAR(war, config); err != nil {
		return err
	}
	login, err := t.login(ctx, target.Pod.Namespace)
	if err != nil {
		return err
	}
	manager, err := resolvePath(ctx, base, t.spec.ManagerPath+"/text/deploy", target)
	if err != nil {
		return err
	}
	deploy := manager + "?" + url.Values{"path": {t.spec.ContextPath}, "update": {"true"}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, deploy, bytes.NewReader(war))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := (&http.Client{Transport: login.Transport(), Timeout: 5 * time.Minute}).Do(request)
	if err != nil {
		return fmt.Errorf("tomcat manager deploy to %s: %w", target.ID, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	reply := strings.TrimSpace(string(body))
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(reply, "OK") {
		return fmt.Errorf("tomcat manager deploy to %s: HTTP %d: %s", target.ID, response.StatusCode, reply)
	}
	return nil
}

// joinPath appends path to raw's path, ahead of its query, so a workload URL
// such as portforward://pod.ns:8080?kind=pod keeps its query.
func joinPath(raw, path string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", raw, err)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	return parsed.String(), nil
}

// resolvePath is base with path appended, resolved to a URL this process
// can reach (a port-forward or an apiserver proxy URL).
func resolvePath(ctx dbcontext.Context, base, path string, target Target) (string, error) {
	joined, err := joinPath(base, path)
	if err != nil {
		return "", err
	}
	resolved, err := ctx.ResolveConnectionURL(joined, models.ConnectionTypeHTTP, target.Pod.Namespace)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", joined, err)
	}
	return resolved, nil
}

// confirm reads the target's health after a deploy until its agent answers.
func confirm(ctx context.Context, client *http.Client, target Target, endpoint, executions, digest string) TargetStatus {
	deadline := time.Now().Add(30 * time.Second)
	for {
		found, health, err := inspect(ctx, client, target, executions, digest)
		switch {
		case found == current:
			return TargetStatus{State: StateDeployed, AgentAPIVersion: health.APIVersion, Endpoint: endpoint}
		case found == outdated:
			return outdatedStatus(endpoint, health)
		case found == stale:
			return TargetStatus{State: StateFailed, AgentAPIVersion: health.APIVersion, Endpoint: endpoint, Error: fmt.Sprintf("agent reports config digest %q after deploy, want %q", health.ConfigDigest, digest)}
		case time.Now().After(deadline) || ctx.Err() != nil:
			return failed(endpoint, fmt.Errorf("agent did not become healthy after deploy: %w", errors.Join(err, ctx.Err())))
		}
		time.Sleep(200 * time.Millisecond)
	}
}
