package telltale

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type metadataTransport func(*http.Request) (*http.Response, error)

func (f metadataTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type metadataBody struct {
	io.Reader
	err error
}

func (b metadataBody) Close() error { return b.err }

var _ = Describe("resource detection", func() {
	DescribeTable("reports response close failures from cloud metadata", func(provider string, status int) {
		closeErr := errors.New("metadata response close failed")
		client := &http.Client{Transport: metadataTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPut {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("token"))}, nil
			}
			body := `{}`
			if provider == "gcp" {
				body = "example-project"
			}
			return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: metadataBody{Reader: strings.NewReader(body), err: closeErr}}, nil
		})}
		resource := map[string]string{"cloud.provider": provider}
		var warning string
		switch provider {
		case "aws":
			_, warning = awsMetadata(resource, client, time.Second)
		case "ecs":
			_, warning = ecsMetadata(resource, client, "http://metadata.example")
		default:
			_, warning = otherMetadata(resource, client)
		}
		Expect(warning).To(ContainSubstring(closeErr.Error()))
		if status != http.StatusOK {
			Expect(warning).To(ContainSubstring(http.StatusText(status)))
		}
	},
		Entry("GCP", "gcp", http.StatusOK),
		Entry("Azure", "azure", http.StatusOK),
		Entry("AWS", "aws", http.StatusOK),
		Entry("ECS", "ecs", http.StatusOK),
		Entry("HTTP error", "ecs", http.StatusForbidden),
	)
	It("enables metadata detection from the environment at startup", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"Cluster":"from-env"}`)) }))
		defer server.Close()
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		GinkgoT().Setenv("TELLTALE_DETECT_CLOUD", "1")
		GinkgoT().Setenv("ECS_CONTAINER_METADATA_URI_V4", server.URL)
		a, err := Start(Options{Listen: "tcp"})
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(a.Close()).To(Succeed()) }()
		Expect(a.endpoint.Resource).To(HaveKeyWithValue("aws.ecs.cluster.name", "from-env"))
	})
	It("does not route metadata requests through environment proxies", func() {
		received := make(chan struct{}, 1)
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received <- struct{}{}; w.WriteHeader(http.StatusOK) }))
		defer proxy.Close()
		GinkgoT().Setenv("HTTP_PROXY", proxy.URL)
		GinkgoT().Setenv("NO_PROXY", "")
		env := func(key string) string {
			if key == "ECS_CONTAINER_METADATA_URI_V4" {
				return "http://metadata.invalid"
			}
			return ""
		}
		_, warning := resourceWithCloud(env, os.ReadFile, true, 100*time.Millisecond)
		Expect(warning).To(ContainSubstring("cloud metadata"))
		Expect(received).To(BeEmpty())
	})
	It("rejects metadata bodies larger than the configured limit", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"Cluster":"` + strings.Repeat("a", 1<<20) + `"}`))
		}))
		defer server.Close()
		env := func(key string) string {
			if key == "ECS_CONTAINER_METADATA_URI_V4" {
				return server.URL
			}
			return ""
		}
		_, warning := resourceWithCloud(env, os.ReadFile, true, time.Second)
		Expect(warning).To(ContainSubstring("cloud metadata"))
	})
	It("merges ECS metadata into the resource when enabled", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Path).To(Equal("/task"))
			_, _ = w.Write([]byte(`{"Cluster":"orders","TaskARN":"arn:aws:ecs:region-1:123456789012:task/example"}`))
		}))
		defer server.Close()
		env := func(key string) string {
			if key == "ECS_CONTAINER_METADATA_URI_V4" {
				return server.URL
			}
			return ""
		}
		resource, warning := resourceWithCloud(env, os.ReadFile, true, time.Second)
		Expect(warning).To(BeEmpty())
		Expect(resource).To(HaveKeyWithValue("aws.ecs.cluster.name", "orders"))
		Expect(resource).To(HaveKeyWithValue("cloud.region", "region-1"))
		Expect(resource).To(HaveKeyWithValue("cloud.account.id", "123456789012"))
	})
	It("reads container ids from cgroup v1 and v2 and cloud environment hints", func() {
		id := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		for _, cgroup := range []string{"12:memory:/docker/" + id, "0::/system.slice/docker-" + id + ".scope"} {
			resource := detectResource(func(key string) string {
				if key == "GOOGLE_CLOUD_PROJECT" {
					return "project"
				}
				return ""
			}, func(path string) ([]byte, error) {
				if path == "/proc/self/cgroup" {
					return []byte(cgroup), nil
				}
				return nil, os.ErrNotExist
			})
			Expect(resource).To(HaveKeyWithValue("container.id", id))
			Expect(resource).To(HaveKeyWithValue("cloud.provider", "gcp"))
			Expect(resource).To(HaveKeyWithValue("cloud.account.id", "project"))
		}
	})
	It("queries cloud metadata only when enabled and warns on timeout", func() {
		requests := make(chan struct{}, 2)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- struct{}{}
			time.Sleep(50 * time.Millisecond)
			_, _ = w.Write([]byte(`{"Cluster":"cluster"}`))
		}))
		defer server.Close()
		env := func(key string) string {
			if key == "ECS_CONTAINER_METADATA_URI_V4" {
				return server.URL
			}
			return ""
		}
		_, warning := resourceWithCloud(env, os.ReadFile, false, 10*time.Millisecond)
		Expect(warning).To(BeEmpty())
		Expect(requests).To(BeEmpty())
		_, warning = resourceWithCloud(env, os.ReadFile, true, 10*time.Millisecond)
		Expect(warning).To(ContainSubstring("cloud metadata"))
		Expect(requests).To(Receive())
	})
	It("detects Kubernetes, cloud and container attributes from injected inputs", func() {
		env := map[string]string{"POD_NAME": "api-1", "POD_UID": "pod-uid", "NODE_NAME": "node-1", "AWS_REGION": "eu-west-1"}
		files := map[string]string{
			"/var/run/secrets/kubernetes.io/serviceaccount/namespace": "orders\n",
			"/proc/self/cgroup": "0::/system.slice/docker-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.scope\n",
		}
		resource := detectResource(func(key string) string { return env[key] }, func(path string) ([]byte, error) {
			if value, ok := files[path]; ok {
				return []byte(value), nil
			}
			return nil, os.ErrNotExist
		})
		Expect(resource).To(HaveKeyWithValue("k8s.namespace.name", "orders"))
		Expect(resource).To(HaveKeyWithValue("k8s.pod.name", "api-1"))
		Expect(resource).To(HaveKeyWithValue("cloud.region", "eu-west-1"))
		Expect(resource).To(HaveKeyWithValue("container.id", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	})

	It("accepts a missing namespace file", func() {
		resource := detectResource(func(string) string { return "" }, func(string) ([]byte, error) { return nil, errors.New("not found") })
		Expect(resource).NotTo(HaveKey("k8s.namespace.name"))
	})
})
