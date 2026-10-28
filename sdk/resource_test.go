package telltale

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resource detection", func() {
	It("enables metadata detection from the environment at startup", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"Cluster":"from-env"}`)) }))
		defer server.Close()
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		GinkgoT().Setenv("TELLTALE_DETECT_CLOUD", "1")
		GinkgoT().Setenv("ECS_CONTAINER_METADATA_URI_V4", server.URL)
		a, err := Start(Options{Listen: "tcp"})
		Expect(err).NotTo(HaveOccurred())
		defer a.Close()
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
