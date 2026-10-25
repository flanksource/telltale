// Specs for serving a trace store: a host mounts the handler under a path of
// its own and reads, exports and checkpoints the captures in it, but cannot
// start or stop one.
package traceserve_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/pkg/traceserve"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	"github.com/flanksource/telltale/pkg/tracing/terminal"
)

const (
	mount      = "/traces"
	castStream = "cast-1"
)

// castEvents are a terminal session of three output chunks and its exit, as
// seqs 1 to 4.
func castEvents(started time.Time) []terminal.Event {
	exit := 0
	return []terminal.Event{
		{At: started, Offset: 0, Kind: terminal.KindOutput, Data: "one\r\n", Width: 80, Height: 24},
		{At: started.Add(time.Second), Offset: 1, Kind: terminal.KindOutput, Data: "two\r\n", Width: 80, Height: 24},
		{At: started.Add(2 * time.Second), Offset: 2, Kind: terminal.KindOutput, Data: "three\r\n", Width: 80, Height: 24},
		{At: started.Add(3 * time.Second), Offset: 3, Kind: terminal.KindExit, ExitCode: &exit, Width: 80, Height: 24, Command: "echo"},
	}
}

func storeKinds() *tracing.Kinds {
	kinds := tracing.NewKinds()
	dumpers, err := stacks.NewDumpers(stacks.InProcess())
	Expect(err).ToNot(HaveOccurred())
	Expect(kinds.RegisterKind(terminal.Kind, terminal.NewKind())).To(Succeed())
	Expect(kinds.RegisterKind(stacks.Kind, stacks.NewKind(dumpers))).To(Succeed())
	return kinds
}

type mounted struct {
	runtime *tracing.Runtime
	server  *httptest.Server
	close   func()
}

// serve opens the store in dir and mounts its handler under mount.
func serve(dir string) mounted {
	runtime, closeStore, err := traceserve.Open(traceserve.OpenOptions{Dir: dir, Kinds: storeKinds(), Retention: time.Hour})
	Expect(err).ToNot(HaveOccurred())
	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			Expect(runtime.Sessions.StopAll(context.Background())).To(Succeed())
			Expect(closeStore()).To(Succeed())
		})
	}
	DeferCleanup(closeAll)
	handler, err := traceserve.Handler(runtime)
	Expect(err).ToNot(HaveOccurred())
	server := httptest.NewServer(http.StripPrefix(mount, handler))
	DeferCleanup(server.Close)
	return mounted{runtime: runtime, server: server, close: closeAll}
}

func (m mounted) do(method, path string) (int, string, http.Header) {
	request, err := http.NewRequest(method, m.server.URL+mount+path, nil)
	Expect(err).ToNot(HaveOccurred())
	request.Header.Set("Accept", "application/json")
	response, err := m.server.Client().Do(request)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	Expect(err).ToNot(HaveOccurred())
	return response.StatusCode, string(body), response.Header
}

// records exports kind's window as NDJSON: the records as the stream holds them.
func (m mounted) records(kind, query string) []map[string]any {
	status, body, _ := m.do(http.MethodGet, "/api/v1/traces/"+kind+"/export?format=ndjson&"+query)
	Expect(status).To(Equal(http.StatusOK), body)
	var records []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		var record map[string]any
		Expect(json.Unmarshal(scanner.Bytes(), &record)).To(Succeed(), scanner.Text())
		records = append(records, record)
	}
	Expect(scanner.Err()).ToNot(HaveOccurred())
	return records
}

func column(rows []map[string]any, name string) []any {
	values := make([]any, 0, len(rows))
	for _, row := range rows {
		values = append(values, row[name])
	}
	return values
}

var _ = Describe("a served trace store", func() {
	var dir string
	var store mounted

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		store = serve(dir)
		_, err := recordstore.AppendTyped(context.Background(), store.runtime.Results.Backend, castStream, terminal.Kind, castEvents(time.Now().UTC()))
		Expect(err).ToNot(HaveOccurred())
	})

	It("exports a window of a stream with the columns a profile hides", func() {
		records := store.records(terminal.Kind, "stream="+castStream+"&afterSeq=1&toSeq=3")
		Expect(column(records, "data")).To(Equal([]any{"two\r\n", "three\r\n"}))
	})

	It("exports a window whose records are older than a day, with no time range named", func() {
		const oldStream = "cast-old"
		_, err := recordstore.AppendTyped(context.Background(), store.runtime.Results.Backend, oldStream, terminal.Kind, castEvents(time.Now().UTC().Add(-72*time.Hour)))
		Expect(err).ToNot(HaveOccurred())
		Expect(column(store.records(terminal.Kind, "stream="+oldStream+"&toSeq=3"), "offset")).To(Equal([]any{float64(0), float64(1), float64(2)}))
	})

	It("downloads a window in the kind's own format", func() {
		status, body, header := store.do(http.MethodGet, "/api/v1/traces/terminal/export?format=cast&stream="+castStream+"&afterSeq=1")
		Expect(status).To(Equal(http.StatusOK), body)
		Expect(header.Get("Content-Disposition")).To(Equal(`attachment; filename="terminal-` + castStream + `.cast"`))
		lines := strings.Split(strings.TrimSpace(body), "\n")
		var castHeader map[string]any
		Expect(json.Unmarshal([]byte(lines[0]), &castHeader)).To(Succeed(), lines[0])
		Expect(castHeader).To(HaveKeyWithValue("version", float64(2)))
		events := strings.Join(lines[1:], "\n")
		Expect(events).To(ContainSubstring("two"))
		Expect(events).ToNot(ContainSubstring("one"), "seq 1 is outside the window")
	})

	It("answers the seq a stream committed through, and 0 for one that committed nothing", func() {
		status, body, _ := store.do(http.MethodGet, "/api/v1/traces/terminal/seq?stream="+castStream)
		Expect(status).To(Equal(http.StatusOK), body)
		Expect(body).To(MatchJSON(`{"seq": 4}`))

		status, body, _ = store.do(http.MethodGet, "/api/v1/traces/terminal/seq?stream=never-written")
		Expect(status).To(Equal(http.StatusOK), body)
		Expect(body).To(MatchJSON(`{"seq": 0}`))

		status, _, _ = store.do(http.MethodGet, "/api/v1/traces/nosuchkind/seq?stream="+castStream)
		Expect(status).To(Equal(http.StatusNotFound))
	})

	It("lists the kinds it serves, with their export formats", func() {
		status, body, _ := store.do(http.MethodGet, "/api/v1/traces/kinds")
		Expect(status).To(Equal(http.StatusOK), body)
		var kinds []map[string]any
		Expect(json.Unmarshal([]byte(body), &kinds)).To(Succeed(), body)
		Expect(column(kinds, "name")).To(ConsistOf(terminal.Kind, stacks.Kind))
	})

	It("serves nothing but its trace routes", func() {
		for _, path := range []string{"/", "/api/openapi.json", "/api/v1/profiles", "/api/v1/sessions"} {
			status, body, _ := store.do(http.MethodGet, path)
			Expect(status).To(Equal(http.StatusNotFound), path)
			Expect(body).ToNot(ContainSubstring("<html"), path)
		}
	})

	It("refuses to start or stop a capture, but checkpoints a running one into its records", func() {
		status, _, _ := store.do(http.MethodPost, "/api/v1/traces/"+stacks.Kind+"/sessions")
		Expect(status).To(Equal(http.StatusNotFound))

		managed, err := store.runtime.Start(dbcontext.New(), tracing.StartRequest{
			Kind: stacks.Kind, Params: json.RawMessage(fmt.Sprintf(`{"rootPid": %d}`, os.Getpid())),
		})
		Expect(err).ToNot(HaveOccurred())
		session := managed.Session()
		status, body, _ := store.do(http.MethodPost, "/api/v1/sessions/"+session.ID()+"/checkpoint")
		Expect(status).To(Equal(http.StatusNoContent), body)
		status, _, _ = store.do(http.MethodPost, "/api/v1/sessions/"+session.ID()+"/stop")
		Expect(status).To(Equal(http.StatusNotFound))

		records := store.records(stacks.Kind, "stream="+session.Snapshot().Events.Stream)
		Expect(records).To(ContainElement(And(
			HaveKeyWithValue("transport", stacks.TransportInProcess), HaveKeyWithValue("raw", HavePrefix("goroutine ")),
		)), "the checkpoint dumped the host's goroutines")
	})

	It("answers a checkpoint of a session it runs no capture of as a conflict", func() {
		status, body, _ := store.do(http.MethodPost, "/api/v1/sessions/no-such-session/checkpoint")
		Expect(status).To(Equal(http.StatusConflict), body)
	})

	It("reopens the store a closed host left, with its records", func() {
		store.close()
		handler, closeStore, err := traceserve.Reopen(traceserve.ReopenOptions{Dir: dir, Kinds: storeKinds()})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(closeStore)
		server := httptest.NewServer(http.StripPrefix(mount, handler))
		DeferCleanup(server.Close)
		reopened := mounted{server: server}
		Expect(reopened.records(terminal.Kind, "stream="+castStream)).To(HaveLen(4))
	})
})

var _ = Describe("reopening a trace store directory", func() {
	It("is os.ErrNotExist for a store that is gone, and creates nothing", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "pruned")
		_, _, err := traceserve.Reopen(traceserve.ReopenOptions{Dir: dir, Kinds: storeKinds()})
		Expect(err).To(MatchError(os.ErrNotExist))
		Expect(dir).ToNot(BeADirectory())
	})

	It("refuses a path that is not a directory, or no kinds", func() {
		file := filepath.Join(GinkgoT().TempDir(), "store")
		Expect(os.WriteFile(file, []byte("not a store"), 0o600)).To(Succeed())
		_, _, err := traceserve.Reopen(traceserve.ReopenOptions{Dir: file, Kinds: storeKinds()})
		Expect(err).To(MatchError(ContainSubstring("is not a directory")))

		_, _, err = traceserve.Reopen(traceserve.ReopenOptions{Dir: GinkgoT().TempDir()})
		Expect(err).To(MatchError(ContainSubstring("kinds")))
	})
})
