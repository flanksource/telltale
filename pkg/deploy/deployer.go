// Package deploy is the Deployer resource: it puts telltale's JVM agent into
// the Java containers a selector picks and provisions the key their executions
// are verified with. Deployment and execution are separate: a deployer
// installs and owns auth, an execution only reaches a deployed target with a
// short-lived token the deployer's issuer minted.
package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/flanksource/telltale/pkg/definition"
)

// The resource's kind, beside TraceDefinition in the same group.
const (
	Kind   = "Deployer"
	Plural = "deployers"
)

// Deployer selects JVMs and the container kind that receives the agent.
type Deployer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              Spec   `json:"spec"`
	Status            Status `json:"status,omitzero"`
}

// Spec is what a deployer installs where, and who signs its executions.
type Spec struct {
	Selector      Selector          `json:"selector" clicky:"title=Selector,desc=Pods that receive the agent"`
	JavaContainer JavaContainerSpec `json:"javaContainer" clicky:"title=Java container,desc=How the agent gets into the JVM, keyed by kind"`
	Auth          AuthSpec          `json:"auth" clicky:"title=Auth,desc=The issuer of execution tokens"`
}

// WorkloadKind is what a selector's labels match.
type WorkloadKind string

const (
	WorkloadDeployment  WorkloadKind = "Deployment"
	WorkloadStatefulSet WorkloadKind = "StatefulSet"
	WorkloadPod         WorkloadKind = "Pod"
)

// Selector picks the running pods of the one workload it names, or of the
// workloads its labels match.
type Selector struct {
	Kind      WorkloadKind `json:"kind" clicky:"title=Kind,desc=Deployment StatefulSet or Pod"`
	Namespace string       `json:"namespace" clicky:"title=Namespace"`
	// Name selects one workload (or pod) of Kind, in place of Labels.
	Name      string            `json:"name,omitempty" clicky:"title=Name,desc=The one workload (or pod) to select instead of labels"`
	Labels    map[string]string `json:"labels,omitempty" clicky:"title=Labels,desc=Labels of the workloads (or pods) to select"`
	Container string            `json:"container" clicky:"title=Container,desc=The container running the JVM"`
}

// AuthSpec names the connection whose signingKey secret signs execution
// tokens; its reference is the tokens' issuer.
type AuthSpec struct {
	Issuer string `json:"issuer" clicky:"title=Issuer,desc=connection:// reference holding the signingKey secret (RSA PEM)"`
	// JWKSURL is where the issuer runtime serves its JWKS. Agents refresh
	// from it on an unknown key id, so a rotation needs no redeploy.
	JWKSURL string `json:"jwksUrl,omitempty" clicky:"title=JWKS URL,desc=Where the issuer runtime serves its JWKS"`
	// JWKS is where the keys agents verify tokens with come from; unset, they
	// are the issuer's own, provisioned inline.
	JWKS *JWKSSource `json:"jwks,omitempty" clicky:"title=JWKS,desc=A Secret or ConfigMap the agents' keys are read from or published to"`
}

// JWKSMode is how a deploy uses its JWKS object.
type JWKSMode string

const (
	// JWKSRead provisions the JWKS the object already holds, such as one a
	// shared issuer publishes; the issuer's signing key must be among them.
	JWKSRead JWKSMode = "read"
	// JWKSPublish writes the issuer's JWKS to the object on every deploy, then
	// provisions it.
	JWKSPublish JWKSMode = "publish"
)

// JWKSSource is a Secret or ConfigMap key in the selector's namespace holding
// a JWKS document.
type JWKSSource struct {
	Mode JWKSMode `json:"mode" clicky:"title=Mode,desc=read or publish"`
	Kind string   `json:"kind" clicky:"title=Kind,desc=ConfigMap or Secret"`
	Name string   `json:"name" clicky:"title=Name"`
	// Key is the data key; jwks.json when unset.
	Key string `json:"key,omitempty" clicky:"title=Key,desc=Data key holding the JWKS (default jwks.json)"`
}

// State is where one target's deployment stands.
type State string

const (
	// StateSelected is a target a dry run selected and did not deploy to.
	StateSelected State = "selected"
	StateDeployed State = "deployed"
	StateOutdated State = "outdated"
	StateFailed   State = "failed"
)

// Status is every selected target's state.
type Status struct {
	Targets []TargetStatus `json:"targets,omitempty"`
}

// TargetStatus is one selected pod's deployment.
type TargetStatus struct {
	Target          string `json:"target"`
	Namespace       string `json:"namespace"`
	Pod             string `json:"pod"`
	Container       string `json:"container"`
	State           State  `json:"state"`
	AgentAPIVersion int    `json:"agentAPIVersion,omitempty"`
	// Endpoint is the rendered execution URL before workload resolution, so
	// a later process resolves it (a port-forward) again.
	Endpoint string `json:"endpoint,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Decode reads a deployer from YAML or JSON, refusing unknown fields.
func Decode(data []byte) (*Deployer, error) {
	raw, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, fmt.Errorf("decode deployer: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var deployer Deployer
	if err := decoder.Decode(&deployer); err != nil {
		return nil, fmt.Errorf("decode deployer: %w", err)
	}
	if deployer.APIVersion != definition.APIVersion || deployer.Kind != Kind {
		return nil, fmt.Errorf("decode deployer: want apiVersion %s and kind %s, got %q and %q", definition.APIVersion, Kind, deployer.APIVersion, deployer.Kind)
	}
	if deployer.Name == "" {
		return nil, fmt.Errorf("decode deployer: metadata.name is required")
	}
	return &deployer, nil
}
