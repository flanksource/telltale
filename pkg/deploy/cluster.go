package deploy

import (
	dbcontext "github.com/flanksource/commons-db/context"
	dbkubernetes "github.com/flanksource/commons-db/kubernetes"

	"github.com/flanksource/telltale/pkg/agentinstall"
)

// The annotations a saved deployer keeps its cluster in. They are the
// deploying client's choice, not part of the resource a cluster applies.
const (
	AnnotationKubeconfig  = "telltale.flanksource.com/kubeconfig"
	AnnotationKubeContext = "telltale.flanksource.com/kubecontext"
)

// Cluster is the kubeconfig and kube context a deploy reached its pods with;
// both unset is kubectl's and client-go's default. A later execution of a
// saved deployer reconnects through the same cluster.
type Cluster struct {
	Kubeconfig string `json:"kubeconfig,omitempty"`
	Context    string `json:"context,omitempty"`
}

// ClusterOf is the cluster a saved deployer was deployed to.
func ClusterOf(deployer Deployer) Cluster {
	return Cluster{Kubeconfig: deployer.Annotations[AnnotationKubeconfig], Context: deployer.Annotations[AnnotationKubeContext]}
}

// Annotate records c on deployer, removing what c leaves unset.
func (c Cluster) Annotate(deployer *Deployer) {
	set := func(key, value string) {
		if value == "" {
			delete(deployer.Annotations, key)
			return
		}
		if deployer.Annotations == nil {
			deployer.Annotations = map[string]string{}
		}
		deployer.Annotations[key] = value
	}
	set(AnnotationKubeconfig, c.Kubeconfig)
	set(AnnotationKubeContext, c.Context)
	if len(deployer.Annotations) == 0 {
		deployer.Annotations = nil
	}
}

// Bind makes ctx's Kubernetes client, which port-forward URLs resolve
// through, this cluster's. The default cluster leaves ctx as it is.
func (c Cluster) Bind(ctx dbcontext.Context) (dbcontext.Context, error) {
	if c == (Cluster{}) {
		return ctx, nil
	}
	client, err := ctx.KubeContextClient(dbkubernetes.Options{Kubeconfig: c.Kubeconfig, Kubecontext: c.Context})
	if err != nil {
		return ctx, err
	}
	return ctx.WithLocalKubernetes(client), nil
}

// Exec runs commands in a target's container with kubectl against c.
func (c Cluster) Exec() ExecFunc {
	return func(target Target) agentinstall.Transport {
		return agentinstall.CommandTransport{Destination: agentinstall.Destination{Kind: "kubernetes", Kubeconfig: c.Kubeconfig, Context: c.Context,
			Namespace: target.Pod.Namespace, Pod: target.Pod.Name, Container: target.Container.Name}}
	}
}
