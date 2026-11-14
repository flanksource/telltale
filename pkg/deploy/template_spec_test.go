package deploy_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/flanksource/telltale/pkg/deploy"
)

func target() deploy.Target {
	running := pod("orders-0", map[string]string{"app": "orders"}, corev1.PodRunning, corev1.ContainerPort{Name: "http", ContainerPort: 8080})
	return deploy.Target{ID: "deployer/example/orders/orders-0", Pod: running, Container: running.Spec.Containers[1]}
}

var _ = Describe("URL templates", func() {
	It("renders the pod, its namespace, labels and a named container port", func() {
		rendered, err := deploy.RenderURL(`proxy://{{.Pod.Name}}.{{.Pod.Namespace}}:{{.Port "http"}}/{{.Pod.Labels.app}}?kind=pod`, target())
		Expect(err).ToNot(HaveOccurred())
		Expect(rendered).To(Equal("proxy://orders-0.orders:8080/orders?kind=pod"))
	})

	DescribeTable("fails loud on a binding the target does not have, never rendering <no value>",
		func(template, message string) {
			rendered, err := deploy.RenderURL(template, target())
			Expect(err).To(MatchError(ContainSubstring(message)))
			Expect(rendered).To(BeEmpty())
		},
		Entry("a missing port", `http://{{.Pod.Name}}:{{.Port "admin"}}`, `container "app" of pod orders/orders-0 has no port named "admin"`),
		Entry("a missing label", `http://{{.Pod.Labels.tier}}`, `map has no entry for key "tier"`),
		Entry("an unknown field", `http://{{.Node}}`, `can't evaluate field Node`),
		Entry("an empty template", ``, "url template is required"),
	)
})
