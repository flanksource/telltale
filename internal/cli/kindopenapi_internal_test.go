package cli

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/internal/jvm"
	"github.com/flanksource/telltale/internal/server"
	"github.com/flanksource/telltale/pkg/traces"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

func kindOpenAPI(root *cobra.Command) rpc.OpenAPISpec {
	handler, err := server.NewHandler(server.HandlerOptions{
		Root: root, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}},
		Traces: http.NotFoundHandler(), Top: http.NotFoundHandler(),
	})
	Expect(err).NotTo(HaveOccurred())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil))
	Expect(response.Code).To(Equal(http.StatusOK))
	var spec rpc.OpenAPISpec
	Expect(json.Unmarshal(response.Body.Bytes(), &spec)).To(Succeed())
	return spec
}

var _ = Describe("kind argument OpenAPI", func() {
	It("serves schema-derived arguments for every registered run kind", func() {
		spec := kindOpenAPI(NewRoot("test"))
		for _, registration := range traces.Registrations() {
			path := "/api/v1/run/" + registration.Name
			Expect(slices.Sorted(maps.Keys(spec.Paths))).To(ContainElement(path))
			Expect(spec.Paths[path]).To(HaveKey("post"))
		}
		http := spec.Paths["/api/v1/run/http"]["post"].RequestBody.Content["application/json"].Schema.Properties
		Expect(http["mode"].Enum).To(Equal([]any{"connect", "mitm"}))
		Expect(http["seam"].Enum).To(Equal([]any{"self", "agent", "proxy"}))
		Expect(http["seam"].Default).To(Equal("self"))
		Expect(http["hosts"].Items.Type).To(Equal("string"))
		Expect(http).NotTo(HaveKey("format"))
		checkpoint := spec.Paths["/api/v1/run/goprof"]["post"].RequestBody.Content["application/json"].Schema.Properties
		Expect(checkpoint["profile"].Enum).To(Equal([]any{"cpu", "heap", "allocs", "mutex", "block", "goroutine"}))
		Expect(checkpoint["profile"].Default).To(Equal("heap"))
		Expect(checkpoint["seconds"].Type).To(Equal("integer"))
	})

	It("serves a typed operation for every built-in JVM operation", func() {
		spec := kindOpenAPI(NewRoot("test"))
		// inputs are an operation's typed inputs, whether the path, query or
		// JSON body carries them, and which of them are required.
		inputs := func(path string) (map[string]string, []string) {
			types, required := map[string]string{}, []string{}
			Expect(spec.Paths).To(HaveKey(path))
			for _, operation := range spec.Paths[path] {
				for _, parameter := range operation.Parameters {
					types[parameter.Name] = parameter.Schema.Type
					if parameter.Required {
						required = append(required, parameter.Name)
					}
				}
				if operation.RequestBody != nil {
					body := operation.RequestBody.Content["application/json"].Schema
					for name, property := range body.Properties {
						types[name] = property.Type
					}
					required = append(required, body.Required...)
				}
			}
			return types, required
		}
		served := 0
		for path, item := range spec.Paths {
			if strings.HasPrefix(path, "/api/v1/jvm/") && path != "/api/v1/jvm/call" && path != "/api/v1/jvm/deployers" {
				served += len(item)
			}
		}
		presets := 0
		for _, registration := range traces.Registrations() {
			presets += len(registration.Commands)
		}
		Expect(served).To(Equal(len(jvm.Operations()) + presets))
		Expect(spec.Paths).NotTo(HaveKey("/api/v1/jvm/classes/source"))
		watch, _ := inputs("/api/v1/jvm/watch")
		Expect(watch).To(HaveKeyWithValue("calls", "boolean"))
		Expect(watch).To(HaveKeyWithValue("duration", "string"))
		decompile, _ := inputs("/api/v1/jvm/decompile")
		Expect(decompile).To(HaveKeyWithValue("class", "string"))
		search, _ := inputs("/api/v1/jvm/jmx/search")
		Expect(search).To(HaveKeyWithValue("pattern", "string"))
		stack, required := inputs("/api/v1/jvm/threads/{id}/stack")
		Expect(stack).To(HaveKeyWithValue("id", "integer"))
		Expect(required).To(ContainElements("agent", "id"))
		install, _ := inputs("/api/v1/jvm/extensions/install")
		Expect(install).To(HaveKeyWithValue("jar", "string"))
	})

	It("publishes required inputs while preserving optional defaults and list choices", func() {
		root := &cobra.Command{Use: "test"}
		command := &cobra.Command{Use: "capture", Run: func(*cobra.Command, []string) {}}
		_, err := bindParamFlags(command.Flags(), &rpc.OpenAPISchema{Type: "object", Required: []string{"name", "count"}, Properties: map[string]*rpc.OpenAPISchema{
			"name":     {Type: "string", Description: "Capture name"},
			"count":    {Type: "integer", Default: 0},
			"features": {Type: "array", Items: &rpc.OpenAPISchema{Type: "string", Enum: []any{"http", "sql"}}, Default: []string{"http"}},
		}})
		Expect(err).NotTo(HaveOccurred())
		root.AddCommand(command)
		schema := kindOpenAPI(root).Paths["/api/v1/capture"]["post"].RequestBody.Content["application/json"].Schema
		Expect(schema.Required).To(Equal([]string{"name"}))
		Expect(schema.Properties["name"].Description).To(Equal("Capture name"))
		Expect(schema.Properties["features"].Items.Enum).To(Equal([]any{"http", "sql"}))
		Expect(schema.Properties["features"].Default).To(Equal([]any{"http"}))
	})
})
