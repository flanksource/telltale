package sessions

import (
	"context"
	"net/http"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	profilepkg "github.com/flanksource/commons-db/cmd/query/profiles"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
)

var _ = ginkgo.Describe("a session start asked to follow", func() {
	const profileName = "plain-follow"
	var (
		handler  *sessionHandler
		registry *query.SessionRegistry
	)

	ginkgo.BeforeEach(func() {
		query.RegisterProvider(&sessionStreamMock{typ: "sess-api-follow", rows: []query.Row{{"n": 1.0}}, block: true})
		store, err := profilepkg.NewFileStore(ginkgo.GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		p := execProfile(profileName)
		p.Provider.Type = "sess-api-follow"
		Expect(store.Save(context.Background(), p)).To(Succeed())
		registry = query.NewSessionRegistry(query.RegistryOptions{MaxSessions: 5})
		ginkgo.DeferCleanup(registry.StopAll)
		handler = newSessionHandler(sessionHandlerOptions{
			Prefix: "/api/v1", Ctx: dbcontext.New(), Store: store, Registry: registry, Next: &nextMarker{},
		})
	})

	ginkgo.DescribeTable("is refused 400 naming the query follow endpoint, starting nothing",
		func(query string) {
			response := doReq(handler, http.MethodPost, "/api/v1/profile/"+profileName+"/sessions?"+query)
			Expect(response.Code).To(Equal(http.StatusBadRequest), response.Body.String())
			Expect(response.Body.String()).To(ContainSubstring("GET /api/v1/profile/" + profileName + "?follow=true"))
			Expect(registry.List()).To(BeEmpty())
		},
		ginkgo.Entry("bare flag", "follow"),
		ginkgo.Entry("true", "follow=true"),
		ginkgo.Entry("with an interval", "follow=true&interval=1s"),
		ginkgo.Entry("false still names a removed control", "follow=false"),
	)
})
