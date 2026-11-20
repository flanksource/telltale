package sessions

import (
	"context"
	"slices"

	"github.com/flanksource/clicky/rpc"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	profilepkg "github.com/flanksource/commons-db/cmd/query/profiles"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/query/profilestore"
)

func openAPIParameterNames(operation rpc.OpenAPIOperation) []string {
	names := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		names = append(names, parameter.Name)
	}
	return names
}

func openAPIParameter(operation rpc.OpenAPIOperation, name string) rpc.OpenAPIParameter {
	index := slices.IndexFunc(operation.Parameters, func(p rpc.OpenAPIParameter) bool { return p.Name == name })
	Expect(index).To(BeNumerically(">=", 0), "parameter %s", name)
	return operation.Parameters[index]
}

var _ = ginkgo.Describe("the sessions API's OpenAPI operations", func() {
	var spec *rpc.OpenAPISpec

	ginkgo.BeforeEach(func() {
		spec = &rpc.OpenAPISpec{Paths: map[string]rpc.OpenAPIPath{}}
		AddSessionsOpenAPI(spec)
	})

	ginkgo.It("emits the list as the sessions surface's collection, with lookups on every filter", func() {
		list := spec.Paths["/api/v1/sessions"]["get"]
		Expect(list.OperationID).To(Equal("list-sessions"))
		Expect(*list.Clicky).To(Equal(rpc.ClickyOperationMeta{Command: "sessions", Surface: "sessions", Verb: "list", Scope: "collection"}))
		Expect(spec.Clicky.Surfaces).To(ContainElement(rpc.ClickySurface{Key: "sessions", Entity: "sessions", Title: "Sessions"}))

		Expect(openAPIParameterNames(list)).To(Equal([]string{
			"profile", "kind", "state", "principal", "restartOf", "label.target", "label.origin", "label.via",
			"label.runId", "label.plan", "label.step", "label.environment", "from", "to", "sort", "order", "limit", "offset",
		}))
		Expect(*openAPIParameter(list, "label.runId").Lookup).To(Equal(rpc.ClickyLookupMeta{
			Ref: "#/components/x-clicky-filters/sessions-label-runId", URL: "/api/v1/sessions",
			Filter: "label.runId", SearchParam: "__lookup_q", Multi: true,
		}))
		Expect(openAPIParameter(list, "restartOf").Lookup.Multi).To(BeFalse())
		Expect(openAPIParameter(list, "state").Schema.Enum).To(Equal([]any{"starting", "running", "stopping", "completed", "failed", "stopped", "interrupted"}))
		Expect(openAPIParameter(list, "from").Clicky.Role).To(Equal("time-from"))
		Expect(openAPIParameter(list, "sort").Schema.Enum).To(Equal([]any{"startedAt", "stoppedAt", "updatedAt", "state", "profile", "principal", "eventCount"}))
		Expect(openAPIParameter(list, "limit").Schema.Default).To(Equal(50))

		Expect(spec.Components.ClickyFilters["sessions-restartOf"]).To(And(
			HaveField("Label", "Restart of"), HaveField("Type", "value"), HaveField("Multi", false)))
		Expect(spec.Components.ClickyFilters["sessions-label-target"]).To(And(
			HaveField("Label", "target"), HaveField("Type", "multi-filter"), HaveField("Multi", true)))
	})

	ginkgo.It("emits the detail and the stop, extend and restart actions", func() {
		detail := spec.Paths["/api/v1/sessions/{id}"]["get"]
		Expect(detail.OperationID).To(Equal("get-session"))
		Expect(*detail.Clicky).To(Equal(rpc.ClickyOperationMeta{Command: "sessions", Surface: "sessions", Verb: "get", Scope: "entity", IDParam: "id"}))

		Expect(spec.Paths["/api/v1/sessions/{id}/stop"]["post"].OperationID).To(Equal("stop-session"))
		extend := spec.Paths["/api/v1/sessions/{id}/extend"]["post"]
		Expect(extend.OperationID).To(Equal("extend-session"))
		Expect(openAPIParameter(extend, "duration").Required).To(BeTrue())
		restart := spec.Paths["/api/v1/sessions/{id}/restart"]["post"]
		Expect(restart.OperationID).To(Equal("restart-session"))
		Expect(openAPIParameter(restart, "duration").Required).To(BeFalse())
		Expect(restart.Responses).To(HaveKey("201"))
		Expect(restart.Responses).ToNot(HaveKey("200"))
	})
})

var _ = ginkgo.Describe("the session start each profile's OpenAPI document offers", func() {
	var (
		service *Service
		spec    *rpc.OpenAPISpec
	)
	region := query.ParamDef{Name: "region", Type: query.ParamTypeEnum, Options: []string{"US", "EU"}}
	profile := func(name string, shape func(*query.Profile)) query.Profile {
		p := query.Profile{Name: name, Provider: query.ProviderConfig{Type: "session-exec-mock"}, Query: "q", Params: []query.ParamDef{region}}
		shape(&p)
		return p
	}

	ginkgo.BeforeEach(func() {
		store, err := profilepkg.NewFileStore(ginkgo.GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		for _, p := range []query.Profile{
			profile("declared trace", func(p *query.Profile) { p.Trace = &query.TraceSpec{} }),
			profile("declared top", func(p *query.Profile) { p.Top = &query.TopSpec{} }),
			profile("answers once", func(*query.Profile) {}),
		} {
			Expect(store.Save(context.Background(), p)).To(Succeed())
		}
		service, err = New(Options{
			Profiles: func() (profilestore.Store, error) { return store, nil },
			Context:  func() dbcontext.Context { return dbcontext.New() },
			Registry: query.NewSessionRegistry(query.RegistryOptions{}),
		})
		Expect(err).ToNot(HaveOccurred())
		spec = &rpc.OpenAPISpec{Paths: map[string]rpc.OpenAPIPath{
			"/api/v1/profile/profile-removed/sessions": {"post": {OperationID: "start-profile-removed-session"}},
		}}
		Expect(service.AddProfileSessionsOpenAPI(context.Background(), spec)).To(Succeed())
	})

	start := func(name string) (rpc.OpenAPIOperation, bool) {
		operation, ok := spec.Paths["/api/v1/profile/"+profilepkg.SurfaceKey(name)+"/sessions"]["post"]
		return operation, ok
	}

	ginkgo.It("starts a declared trace with its params and a duration, never a follow", func() {
		operation, ok := start("declared trace")
		Expect(ok).To(BeTrue())
		Expect(operation.OperationID).To(Equal("start-profile-declared-trace-session"))
		Expect(operation.Description).To(ContainSubstring("POST /api/v1/sessions/{id}/stop"))
		Expect(openAPIParameterNames(operation)).To(Equal([]string{"region", "duration"}))
		Expect(openAPIParameter(operation, "region").Schema.Enum).To(Equal([]any{"US", "EU"}))
	})

	ginkgo.It("starts a declared top with an interval override", func() {
		operation, ok := start("declared top")
		Expect(ok).To(BeTrue())
		Expect(openAPIParameterNames(operation)).To(Equal([]string{"region", "interval", "duration"}))
	})

	ginkgo.It("offers a query profile no session start: following it is the profile read's ?follow", func() {
		_, ok := start("answers once")
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("drops the start of a profile the store no longer holds", func() {
		Expect(spec.Paths).ToNot(HaveKey("/api/v1/profile/profile-removed/sessions"))
	})
})
