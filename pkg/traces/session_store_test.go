package traces

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/clicky/cache"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/traceserve"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("persistent run sessions", func() {
	It("serves only sessions retained by the registry when persistence is unconfigured", func(ctx SpecContext) {
		host, err := Open(OpenOptions{DataDir: GinkgoT().TempDir(), RunID: "run-local"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(host.Close, context.Background())
		for range 51 {
			session, err := host.Runtime().Sessions.Track(ctx, query.TrackOptions{
				Profile: "traces/example", Kind: query.KindCapture, Role: query.SessionRoleCapture,
			})
			Expect(err).NotTo(HaveOccurred())
			session.Finish(query.FinishUpdate{})
		}
		handler, err := traceserve.Handler(host.Runtime())
		Expect(err).NotTo(HaveOccurred())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Header().Get("X-Total-Count")).To(Equal("50"))
	})

	It("shares each run's sessions with other readers and confines its sweep to that run", func(ctx SpecContext) {
		root := GinkgoT().TempDir()
		backend := cache.NewMemory()
		firstProcess := sessionStores(backend, "data")
		secondProcess := sessionStores(backend, "data")
		start := func(runID string, stores SessionStores) (*Host, *query.Session) {
			host, err := Open(OpenOptions{DataDir: root, RunID: runID, SessionStores: stores})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(host.Close, context.Background())
			session, err := host.Runtime().Sessions.Track(ctx, query.TrackOptions{Profile: "traces/example", Kind: query.KindCapture})
			Expect(err).NotTo(HaveOccurred())
			session.OnStop(func(string) { session.Finish(query.FinishUpdate{}) })
			Expect(session.Running(query.RunningUpdate{})).To(Succeed())
			return host, session
		}
		first, firstSession := start("run-first", firstProcess)
		second, secondSession := start("run-second", secondProcess)
		for _, run := range []struct {
			host    *Host
			session *query.Session
			other   SessionStores
		}{{first, firstSession, secondProcess}, {second, secondSession, firstProcess}} {
			Expect(run.host.Runtime().Sessions.Sweep(ctx)).To(Succeed())
			store, err := run.other(run.host.RunID())
			Expect(err).NotTo(HaveOccurred())
			page, err := store.List(ctx, query.SessionFilter{})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Items).To(HaveExactElements(And(HaveField("ID", run.session.ID()), HaveField("State", query.SessionRunning))))
		}
	})

	It("lists a dead writer's interrupted session after the next owner reopens the run", func(ctx SpecContext) {
		root := GinkgoT().TempDir()
		host, err := Open(OpenOptions{DataDir: root, RunID: "run-previous"})
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Close(ctx)).To(Succeed())
		backing := sessionStores(cache.NewMemory(), "data")
		store, err := backing(host.RunID())
		Expect(err).NotTo(HaveOccurred())
		now := time.Now().UTC()
		current, err := os.Hostname()
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Begin(ctx, query.SessionRecord{
			SessionStart: query.SessionStart{ID: "lost", Profile: "traces/example", Kind: query.KindCapture,
				Role: query.SessionRoleCapture, StartedAt: now, Owner: query.SessionOwner{Host: current, PID: 1 << 30, Boot: "earlier-process"}},
			SessionStatus: query.SessionStatus{State: query.SessionRunning, HeartbeatAt: now, UpdatedAt: now},
		})).To(Succeed())
		runtime, closeStore, err := Reopen(host.Dir(), store)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(closeStore)
		handler, err := traceserve.Handler(runtime)
		Expect(err).NotTo(HaveOccurred())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		var page query.SessionPage
		Expect(json.Unmarshal(response.Body.Bytes(), &page)).To(Succeed())
		Expect(page.Items).To(HaveExactElements(And(HaveField("ID", "lost"), HaveField("State", query.SessionInterrupted),
			HaveField("StopReason", query.StopReasonOwnerRestart))))
	})

	It("uses one identity for aliases of a data directory and distinct identities for different directories", func(ctx SpecContext) {
		root := GinkgoT().TempDir()
		dir, alias := filepath.Join(root, "data"), filepath.Join(root, "alias")
		Expect(os.Mkdir(dir, 0700)).To(Succeed())
		Expect(os.Symlink(dir, alias)).To(Succeed())
		id, err := sessionDataDirID(ctx, dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(sessionDataDirID(ctx, alias)).To(Equal(id))
		Expect(sessionDataDirID(ctx, root)).NotTo(Equal(id))
	})
})
