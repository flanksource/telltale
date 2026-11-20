package deploy_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/flanksource/telltale/pkg/deploy"
)

const (
	namespace = "orders"
	jvm       = "app"
)

func pod(name string, labels map[string]string, phase corev1.PodPhase, ports ...corev1.ContainerPort) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "sidecar"}, {Name: jvm, Ports: ports}}},
		Status:     corev1.PodStatus{Phase: phase, PodIP: "10.0.0.1"},
	}
}

func cluster() *fake.Clientset {
	web := map[string]string{"app": "orders-web", "pod-template-hash": "abc"}
	objects := []runtime.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "orders-web", Namespace: namespace, Labels: map[string]string{"tier": "web"}},
			Spec: appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "orders-web"}}}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "orders-db", Namespace: namespace, Labels: map[string]string{"tier": "web"}},
			Spec: appsv1.StatefulSetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "orders-db"}}}},
		pod("orders-web-b", web, corev1.PodRunning),
		pod("orders-web-a", web, corev1.PodRunning),
		pod("orders-web-pending", web, corev1.PodPending),
		pod("orders-db-0", map[string]string{"app": "orders-db"}, corev1.PodRunning),
		pod("billing", map[string]string{"app": "billing", "tier": "web"}, corev1.PodRunning),
	}
	return fake.NewClientset(objects...)
}

func selected(targets []deploy.Target) []string {
	names := make([]string, len(targets))
	for i, target := range targets {
		names[i] = target.Pod.Name
	}
	return names
}

var _ = Describe("Selector", func() {
	DescribeTable("resolves exactly the running pods of the workloads its labels match",
		func(kind deploy.WorkloadKind, labels map[string]string, want []string) {
			targets, err := deploy.Resolve(context.Background(), cluster(), "example", deploy.Selector{Kind: kind, Namespace: namespace, Labels: labels, Container: jvm})
			Expect(err).ToNot(HaveOccurred())
			Expect(selected(targets)).To(Equal(want))
		},
		Entry("a Deployment's pods, sorted, without the pending one", deploy.WorkloadDeployment, map[string]string{"tier": "web"}, []string{"orders-web-a", "orders-web-b"}),
		Entry("a StatefulSet's pods", deploy.WorkloadStatefulSet, map[string]string{"tier": "web"}, []string{"orders-db-0"}),
		Entry("pods by their own labels", deploy.WorkloadPod, map[string]string{"tier": "web"}, []string{"billing"}),
	)

	DescribeTable("resolves the running pods of the one workload it names",
		func(kind deploy.WorkloadKind, name string, want []string) {
			targets, err := deploy.Resolve(context.Background(), cluster(), "example", deploy.Selector{Kind: kind, Namespace: namespace, Name: name, Container: jvm})
			Expect(err).ToNot(HaveOccurred())
			Expect(selected(targets)).To(Equal(want))
		},
		Entry("a Deployment by name, though its own labels match nothing of its pods", deploy.WorkloadDeployment, "orders-web", []string{"orders-web-a", "orders-web-b"}),
		Entry("a StatefulSet by name", deploy.WorkloadStatefulSet, "orders-db", []string{"orders-db-0"}),
		Entry("a pod by name", deploy.WorkloadPod, "billing", []string{"billing"}),
	)

	It("names each target by deployer, namespace and pod", func() {
		targets, err := deploy.Resolve(context.Background(), cluster(), "example", deploy.Selector{Kind: deploy.WorkloadStatefulSet, Namespace: namespace, Labels: map[string]string{"tier": "web"}, Container: jvm})
		Expect(err).ToNot(HaveOccurred())
		Expect(targets).To(HaveLen(1))
		Expect(targets[0].ID).To(Equal("deployer/example/orders/orders-db-0"))
		Expect(targets[0].Container.Name).To(Equal(jvm))
	})

	It("takes a pod's only container when the selector names none", func() {
		single := pod("cycle-0", map[string]string{"app": "cycle"}, corev1.PodRunning)
		single.Spec.Containers = single.Spec.Containers[1:]
		targets, err := deploy.Resolve(context.Background(), fake.NewClientset(single), "example", deploy.Selector{Kind: deploy.WorkloadPod, Namespace: namespace, Name: "cycle-0"})
		Expect(err).ToNot(HaveOccurred())
		Expect(targets).To(HaveLen(1))
		Expect(targets[0].Container.Name).To(Equal(jvm))
	})

	DescribeTable("fails loud on a selector that cannot select",
		func(selector deploy.Selector, message string) {
			_, err := deploy.Resolve(context.Background(), cluster(), "example", selector)
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("an unknown kind", deploy.Selector{Kind: "DaemonSet", Namespace: namespace, Labels: map[string]string{"tier": "web"}, Container: jvm}, `unsupported selector kind "DaemonSet"`),
		Entry("neither labels nor a name", deploy.Selector{Kind: deploy.WorkloadPod, Namespace: namespace, Container: jvm}, "selector labels or a workload name is required"),
		Entry("both labels and a name", deploy.Selector{Kind: deploy.WorkloadDeployment, Namespace: namespace, Name: "orders-web", Labels: map[string]string{"tier": "web"}, Container: jvm}, "selector names a workload or matches labels, not both"),
		Entry("a named workload that does not exist", deploy.Selector{Kind: deploy.WorkloadDeployment, Namespace: namespace, Name: "orders-api", Container: jvm}, `get Deployment orders/orders-api`),
		Entry("a pending named pod", deploy.Selector{Kind: deploy.WorkloadPod, Namespace: namespace, Name: "orders-web-pending", Container: jvm}, "selector Pod orders-web-pending in orders matched no running pods"),
		Entry("no namespace", deploy.Selector{Kind: deploy.WorkloadPod, Labels: map[string]string{"tier": "web"}, Container: jvm}, "namespace"),
		Entry("no container, on a pod running several", deploy.Selector{Kind: deploy.WorkloadPod, Namespace: namespace, Labels: map[string]string{"tier": "web"}}, `pod orders/billing runs containers sidecar, app: name the JVM container`),
		Entry("a container a pod lacks", deploy.Selector{Kind: deploy.WorkloadPod, Namespace: namespace, Labels: map[string]string{"tier": "web"}, Container: "jvm"}, `pod orders/billing has no container "jvm"`),
		Entry("no match", deploy.Selector{Kind: deploy.WorkloadPod, Namespace: namespace, Labels: map[string]string{"tier": "none"}, Container: jvm}, "matched no running pods"),
	)
})
