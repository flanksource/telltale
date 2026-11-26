package httpdecode

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type Kubernetes struct {
	Group       string `json:"group,omitempty"`
	Version     string `json:"version,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Resource    string `json:"resource,omitempty"`
	Name        string `json:"name,omitempty"`
	Subresource string `json:"subresource,omitempty"`
}

var (
	kubeVersion = regexp.MustCompile(`^v[0-9]+(?:(?:alpha|beta)[0-9]+)?$`)
	kubeGroup   = regexp.MustCompile(`^[a-z0-9-]+(?:\.[a-z0-9-]+)+$`)
)

func KubernetesWatch(req *http.Request, resp *http.Response) bool {
	if resp.StatusCode != http.StatusOK {
		return false
	}
	_, operation := kubernetesRoute(req, resp.Header, req.URL.Query())
	return operation == "watch"
}

func decodeKubernetes(e exchange) Result {
	k, operation := kubernetesRoute(e.req, e.resp.Header, e.query)
	if k == nil {
		return Result{}
	}
	d := &Details{Kubernetes: k, RequestID: observed(e.resp.Header.Get("Audit-Id"))}
	if e.resp.StatusCode >= 400 && e.response.isJSON() {
		var status struct {
			Kind   string `json:"kind"`
			Reason string `json:"reason"`
		}
		if e.response.json(d, &status) && status.Kind == "Status" {
			d.ErrorCode = status.Reason
		}
	}
	if operation == "watch" {
		d.Partial = e.response.truncated || e.options.StreamOpen
	}
	return Result{Protocol: "kubernetes", Operation: operation, Details: d}
}

func kubernetesRoute(req *http.Request, response http.Header, query url.Values) (*Kubernetes, string) {
	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	evidence := kubernetesEvidence(req, response)
	k := &Kubernetes{}
	var resource []string
	switch {
	case len(parts) == 1 && evidence && slices.Contains([]string{"api", "apis", "version"}, parts[0]):
		return k, "discover"
	case len(parts) >= 2 && parts[0] == "api" && parts[1] == "v1":
		k.Version, resource = parts[1], parts[2:]
	case len(parts) == 2 && parts[0] == "apis" && (evidence || knownKubeGroup(parts[1])):
		k.Group = parts[1]
		return k, "discover"
	case len(parts) >= 3 && parts[0] == "apis" && kubeVersion.MatchString(parts[2]) &&
		(evidence || knownKubeGroup(parts[1]) || kubeGroup.MatchString(parts[1])):
		k.Group, k.Version, resource = parts[1], parts[2], parts[3:]
	default:
		return nil, ""
	}
	if len(resource) == 0 {
		if evidence || k.Group != "" {
			return k, "discover"
		}
		return nil, ""
	}
	watch, _ := strconv.ParseBool(query.Get("watch"))
	if resource[0] == "watch" {
		watch, resource = true, resource[1:]
	}
	if !k.setResource(resource, evidence) {
		return nil, ""
	}
	return k, kubeVerb(req.Method, k.Name != "", watch)
}

func kubernetesEvidence(req *http.Request, response http.Header) bool {
	host, agent := hostname(req.URL), req.Header.Get("User-Agent")
	return host == "kubernetes.default" || host == "kubernetes.default.svc" || strings.HasPrefix(host, "kubernetes.default.svc.") ||
		strings.HasPrefix(agent, "kubectl/") || strings.HasPrefix(agent, "kube-") ||
		observed(response.Get("Audit-Id")) != "" ||
		strings.Contains(req.Header.Get("Accept"), "application/vnd.kubernetes.") ||
		strings.Contains(response.Get("Content-Type"), "application/vnd.kubernetes.")
}

func knownKubeGroup(group string) bool {
	switch group {
	case "apps", "batch", "autoscaling", "policy", "extensions":
		return true
	}
	return false
}

func (k *Kubernetes) setResource(parts []string, evidence bool) bool {
	if len(parts) >= 3 && parts[0] == "namespaces" &&
		(k.Group != "" || (parts[2] != "status" && parts[2] != "finalize")) {
		k.Namespace, parts = parts[1], parts[2:]
	}
	if len(parts) == 0 || parts[0] == "" || (k.Group == "" && !evidence && !coreResource(parts[0])) {
		return false
	}
	k.Resource = parts[0]
	if len(parts) > 1 {
		k.Name = parts[1]
	}
	if len(parts) > 2 {
		k.Subresource = parts[2]
	}
	return true
}

func coreResource(resource string) bool {
	switch resource {
	case "bindings", "componentstatuses", "configmaps", "endpoints", "events", "limitranges", "namespaces", "nodes",
		"persistentvolumeclaims", "persistentvolumes", "pods", "podtemplates", "replicationcontrollers", "resourcequotas",
		"secrets", "serviceaccounts", "services":
		return true
	}
	return false
}

func kubeVerb(method string, named, watch bool) string {
	switch method {
	case http.MethodGet:
		if watch {
			return "watch"
		}
		if !named {
			return "list"
		}
		return "get"
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodPatch:
		return "patch"
	case http.MethodDelete:
		if !named {
			return "deletecollection"
		}
		return "delete"
	}
	return method
}
