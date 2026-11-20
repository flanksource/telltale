package prometheus

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	dbkubernetes "github.com/flanksource/commons-db/kubernetes"
	"github.com/flanksource/commons/logger"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/streaming/pkg/httpstream"
	"k8s.io/streaming/pkg/httpstream/spdy"
)

var _ = Describe("Kubernetes port-forward scrape endpoints", func() {
	It("forwards to the pod and closes its tunnel independently of another capture", func(ctx SpecContext) {
		const endpoint = "port-forward://exporter.monitoring:9100/metrics?kind=pod&module=fixture"
		api := httptest.NewServer(http.HandlerFunc(portForwardFixture))
		DeferCleanup(api.Close)
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: "monitoring", Name: "exporter"},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "exporter", Ports: []corev1.ContainerPort{{ContainerPort: 9100}}}}},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		}
		client := dbkubernetes.NewKubeClient(logger.GetLogger("test"), fake.NewSimpleClientset(pod), &rest.Config{Host: api.URL})
		capture := dbcontext.NewContext(ctx).WithLocalKubernetes(client)
		first, releaseFirst, err := resolveEndpoints(capture, []string{endpoint})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(releaseFirst)
		second, releaseSecond, err := resolveEndpoints(capture, []string{endpoint})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(releaseSecond)
		Expect(first[0].original).To(Equal(endpoint))
		Expect(first[0].url.Host).NotTo(Equal(second[0].url.Host))
		Expect(releaseFirst()).To(Succeed())

		Eventually(func() error {
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", first[0].url.Host)
			if conn != nil {
				_ = conn.Close()
			}
			return err
		}).Should(HaveOccurred())
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, second[0].url.String(), nil)
		Expect(err).NotTo(HaveOccurred())
		response, err := http.DefaultClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(response.Body)
		Expect(response.Body.Close()).To(Succeed())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("fixture_metric 1\n"))
	}, SpecTimeout(10*time.Second))
})

func portForwardFixture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/namespaces/monitoring/pods/exporter/portforward" {
		http.NotFound(w, r)
		return
	}
	if _, err := httpstream.Handshake(r, w, []string{portforward.PortForwardProtocolV1Name}); err != nil {
		return
	}
	conn := spdy.NewResponseUpgrader().UpgradeResponse(w, r, func(stream httpstream.Stream, ready <-chan struct{}) error {
		go func() {
			<-ready
			defer func() { _ = stream.Close() }()
			if stream.Headers().Get(corev1.StreamType) != corev1.StreamTypeData {
				return
			}
			request, err := http.ReadRequest(bufio.NewReader(stream))
			if err != nil || request.URL.RequestURI() != "/metrics?module=fixture" || stream.Headers().Get(corev1.PortHeader) != "9100" {
				return
			}
			const body = "fixture_metric 1\n"
			_, _ = fmt.Fprintf(stream, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
		}()
		return nil
	})
	if conn != nil {
		<-conn.CloseChan()
	}
}
