package deploy_test

import (
	dbcontext "github.com/flanksource/commons-db/context"
	dbkubernetes "github.com/flanksource/commons-db/kubernetes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/deploy"
)

var _ = Describe("the cluster a deployer was deployed to", func() {
	uat := deploy.Cluster{Kubeconfig: "/home/example/.kube/uat", Context: "uat"}

	It("is kept on the saved deployer and read back", func() {
		deployer := tomcatDeployer()
		uat.Annotate(&deployer)
		Expect(deploy.ClusterOf(deployer)).To(Equal(uat))
		deploy.Cluster{}.Annotate(&deployer)
		Expect(deploy.ClusterOf(deployer)).To(Equal(deploy.Cluster{}))
		Expect(deployer.Annotations).To(BeEmpty())
	})

	It("runs kubectl in a target's container against that cluster", func() {
		pod := listenerPod("orders-0", &listenerStub{})
		transport := uat.Exec()(deploy.Target{ID: "deployer/orders/shop/orders-0", Pod: pod, Container: pod.Spec.Containers[0]})
		Expect(transport).To(Equal(agentinstall.CommandTransport{Destination: agentinstall.Destination{Kind: "kubernetes",
			Kubeconfig: uat.Kubeconfig, Context: uat.Context, Namespace: pod.Namespace, Pod: pod.Name, Container: pod.Spec.Containers[0].Name}}))
	})

	It("binds a context to that cluster's client, and leaves the default cluster alone", func() {
		var asked []dbkubernetes.Options
		chosen := &dbkubernetes.Client{}
		ctx := dbcontext.New().WithKubeContextClients(func(options dbkubernetes.Options) (*dbkubernetes.Client, error) {
			asked = append(asked, options)
			return chosen, nil
		})
		bound, err := uat.Bind(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(asked).To(Equal([]dbkubernetes.Options{{Kubeconfig: uat.Kubeconfig, Kubecontext: uat.Context}}))
		Expect(bound.LocalKubernetes()).To(BeIdenticalTo(chosen))

		_, err = deploy.Cluster{}.Bind(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(asked).To(HaveLen(1), "the default cluster asks for no client")
	})
})
