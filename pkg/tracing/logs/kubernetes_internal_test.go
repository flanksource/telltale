package logs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

var _ = Describe("Kubernetes log source", func() {
	It("follows the requested container of matching pods", func() {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "orders", Labels: map[string]string{"app": "api"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}}}
		client := fake.NewSimpleClientset(pod)
		err := followKubernetes(context.Background(), client, Params{Namespace: "orders", PodSelector: "app=api", Container: "app"}, func(Log) error { return nil })
		Expect(err).NotTo(HaveOccurred())
		actions := client.Actions()
		Expect(actions).To(HaveLen(2))
		Expect(actions[0].GetVerb()).To(Equal("list"))
		Expect(actions[1].GetSubresource()).To(Equal("log"))
	})
	It("emits Kubernetes log lines with pod and container resource attributes", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/namespaces/orders/pods" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"kind":"PodList","apiVersion":"v1","items":[{"metadata":{"name":"api-1","namespace":"orders","uid":"pod-uid","labels":{"app":"api"}},"spec":{"nodeName":"node-1","containers":[{"name":"app"}]}}]}`)
				return
			}
			_, _ = fmt.Fprintln(w, "2026-04-19T11:23:40Z hello kubernetes")
		}))
		DeferCleanup(server.Close)
		client, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
		Expect(err).NotTo(HaveOccurred())
		var records []Log
		err = followKubernetes(context.Background(), client, Params{Namespace: "orders", PodSelector: "app=api", Container: "app"}, func(row Log) error { records = append(records, row); return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].Message).To(Equal("hello kubernetes"))
		Expect(records[0].Resource).To(HaveKeyWithValue("k8s.pod.uid", "pod-uid"))
		Expect(records[0].Resource).To(HaveKeyWithValue("k8s.container.name", "app"))
	})
})
