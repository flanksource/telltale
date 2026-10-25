// Package traces is a run's trace host: one persistent trace store per run,
// at .telltale/traces/<runID>, serving every trace kind a fixture
// can arm (`traces:`) and the run-wide process capture behind the Processes
// tab. The store outlives the run so a saved snapshot can reopen it; Prune
// bounds how many runs are kept.
package traces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/pkg/httpx"
	"github.com/flanksource/telltale/pkg/traceserve"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	tracehttp "github.com/flanksource/telltale/pkg/tracing/http"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/pgproxy"
	"github.com/flanksource/telltale/pkg/tracing/process"
	tracesql "github.com/flanksource/telltale/pkg/tracing/sql"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	"github.com/flanksource/telltale/pkg/tracing/terminal"
)

// Dir is where a work dir keeps its runs' trace stores, one directory each.
const Dir = ".telltale/traces"

const (
	// pollEvery is how often a capture's records are committed, and so how
	// stale a read of a running capture can be. Mark commits on demand.
	pollEvery = 500 * time.Millisecond
	// maxDuration outlasts any run: a capture ends when its run stops it.
	maxDuration = 24 * time.Hour
	// retention keeps a stored row long enough for any saved run to reopen;
	// Prune, not the row TTL, decides how many runs are kept.
	retention = 30 * 24 * time.Hour
)

// Host is one run's trace store and the runtime that starts captures into it.
type Host struct {
	runID   string
	dir     string
	runtime *tracing.Runtime
	dumpers *stacks.Dumpers
}

// RunPrefix and RunTimestampLayout name a run: run-<UTC start>, filesystem
// safe (no colons) and chronologically sortable. A run's trace store and its
// per-run snapshot (snapshots.SavePerRun) are both named so.
const (
	RunPrefix          = "run-"
	RunTimestampLayout = "2006-01-02T15-04-05Z"
)

// RunID names a run started at started.
func RunID(started time.Time) string {
	return RunPrefix + started.UTC().Format(RunTimestampLayout)
}

// Open creates the run's store under workDir/.telltale/traces. A runID already
// taken by an earlier run in the same second gets a -2, -3, … suffix, so a
// run never writes into another's store; RunID reports the name used.
func Open(workDir, runID string) (*Host, error) {
	if workDir == "" || runID == "" {
		return nil, fmt.Errorf("open trace store: a work dir and a run id are required (got %q, %q)", workDir, runID)
	}
	root := filepath.Join(workDir, Dir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("open trace store: %w", err)
	}
	dir, name, err := claimRunDir(root, runID)
	if err != nil {
		return nil, err
	}
	dumpers, err := NewDumpers()
	if err != nil {
		return nil, err
	}
	runtime, _, err := openStore(dir, dumpers)
	if err != nil {
		return nil, err
	}
	runtime.PollEvery = pollEvery
	runtime.Sessions = query.NewSessionRegistry(query.RegistryOptions{
		MaxDuration: maxDuration, BeforeRead: runtime.Results.Registry.BeforeRead,
	})
	return &Host{runID: name, dir: dir, runtime: runtime, dumpers: dumpers}, nil
}

// RegisterDumper adds a stack transport the run's stacks are dumped over,
// such as a sigquit dumper for the Go test binaries a host supervises.
func (h *Host) RegisterDumper(dumper stacks.Dumper) error {
	return h.dumpers.Register(dumper)
}

// StoreDir is where the run runID of workDir keeps its trace store.
func StoreDir(workDir, runID string) string {
	return filepath.Join(workDir, Dir, runID)
}

// Reopen opens the store a finished run left in dir, such as for a saved
// snapshot to serve, with the close that stops using it. A store Prune
// removed is os.ErrNotExist.
func Reopen(dir string) (*tracing.Runtime, func() error, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("reopen trace store: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("reopen trace store: %s is not a directory", dir)
	}
	dumpers, err := NewDumpers()
	if err != nil {
		return nil, nil, err
	}
	return openStore(dir, dumpers)
}

// openStore opens the store in dir with every kind of the catalog, the
// way query serves a store.
func openStore(dir string, dumpers *stacks.Dumpers) (*tracing.Runtime, func() error, error) {
	kinds, err := kindsWith(dumpers)
	if err != nil {
		return nil, nil, err
	}
	return traceserve.Open(traceserve.OpenOptions{Dir: dir, Kinds: kinds, Retention: retention})
}

// claimRunDir creates root/runID, or the first free suffixed name.
func claimRunDir(root, runID string) (string, string, error) {
	for i := 1; i < 1000; i++ {
		name := runID
		if i > 1 {
			name = fmt.Sprintf("%s-%d", runID, i)
		}
		dir := filepath.Join(root, name)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return dir, name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", "", fmt.Errorf("open trace store: %w", err)
		}
	}
	return "", "", fmt.Errorf("open trace store: no free directory for run %s in %s", runID, root)
}

// NewDumpers are the stack transports every run dumps over: the in-process
// one for this process and the OS's native sampler for any process. A run
// that supervises Go test binaries adds a sigquit dumper (RegisterDumper).
func NewDumpers() (*stacks.Dumpers, error) {
	return stacks.NewDumpers(stacks.InProcess(), stacks.Native(stacks.NativeOptions{}))
}

// Kinds is the catalog every trace store serves, its stacks dumped over
// NewDumpers.
func Kinds() (*tracing.Kinds, error) {
	dumpers, err := NewDumpers()
	if err != nil {
		return nil, err
	}
	return kindsWith(dumpers)
}

// kindsWith is the catalog over dumpers: the kinds a fixture can arm and the
// process kinds the run-wide capture uses, sharing one process sampler and
// the dumpers. The `http` and `sql_statements` kinds default to telltale's own
// traffic: its HTTP feature and its own database. goprof reads profile files
// only: the host knows no pprof or gops address.
func kindsWith(dumpers *stacks.Dumpers) (*tracing.Kinds, error) {
	kinds := tracing.NewKinds()
	sampler := process.NewSampler()
	for name, plugin := range map[string]tracing.TracePlugin{
		"http":              tracehttp.Kind().WithDefaults(tracehttp.Params{Features: []string{httpx.Feature}}),
		httpproxy.Kind:      httpproxy.NewKind(),
		pgproxy.Kind:        pgproxy.NewKind(),
		tracesql.RowKind:    tracesql.Kind().WithDefaults(tracesql.Params{Connections: []string{"self"}}),
		terminal.Kind:       terminal.NewKind(),
		process.TreeKind:    process.NewTreeKind(sampler, process.TreeOptions{Dumpers: dumpers}),
		process.SamplesKind: process.NewSamplesKind(sampler),
		stacks.Kind:         stacks.NewKind(dumpers),
		goprof.Kind:         goprof.NewKind(nil),
	} {
		if err := kinds.RegisterKind(name, plugin); err != nil {
			return nil, err
		}
	}
	return kinds, nil
}

// Validate refuses a kind the catalog does not serve, naming those it does,
// and params the kind would refuse at start.
func Validate(kinds *tracing.Kinds, kind string, params json.RawMessage) error {
	plugin, err := Lookup(kinds, kind)
	if err != nil {
		return err
	}
	if err := plugin.ValidateParams(params); err != nil {
		return fmt.Errorf("trace %s params: %w", kind, err)
	}
	return nil
}

// Lookup is the catalog's plugin for kind, refusing one it does not serve by
// naming those it does.
func Lookup(kinds *tracing.Kinds, kind string) (tracing.TracePlugin, error) {
	plugin, ok := kinds.Get(kind)
	if !ok {
		return nil, fmt.Errorf("unknown trace kind %q (registered: %s)", kind, strings.Join(KindNames(kinds), ", "))
	}
	return plugin, nil
}

// KindNames are the catalog's kind names, sorted.
func KindNames(kinds *tracing.Kinds) []string {
	infos, err := kinds.List()
	if err != nil {
		panic(fmt.Sprintf("describe trace kinds: %v", err))
	}
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Name)
	}
	slices.Sort(names)
	return names
}

// RunID is the name the run's store was opened under.
func (h *Host) RunID() string { return h.runID }

// Dir is the run's store directory.
func (h *Host) Dir() string { return h.dir }

// Kinds is the catalog the host serves.
func (h *Host) Kinds() *tracing.Kinds { return h.runtime.Kinds }

// Runtime serves the run's store and its running captures, such as through
// traceserve for the test UI.
func (h *Host) Runtime() *tracing.Runtime { return h.runtime }

// Start starts a capture of kind with params into a new stream of the store.
func (h *Host) Start(kind string, params json.RawMessage) (*Capture, error) {
	if err := Validate(h.runtime.Kinds, kind, params); err != nil {
		return nil, err
	}
	managed, err := h.runtime.Start(dbcontext.New(), tracing.StartRequest{Kind: kind, Params: params})
	if err != nil {
		return nil, fmt.Errorf("start %s trace: %w", kind, err)
	}
	return &Capture{host: h, kind: kind, managed: managed}, nil
}

// Summarize is the kind's summary of ref's window; it always has `count`.
func (h *Host) Summarize(ctx context.Context, ref tracing.TraceRef) (map[string]any, error) {
	return h.runtime.Summarize(ctx, ref)
}

// Close stops every capture still running and closes the store, which stays
// on disk for the run's snapshot to reopen.
func (h *Host) Close(ctx context.Context) error {
	return errors.Join(h.runtime.Sessions.StopAll(ctx), h.runtime.Results.Close())
}

// highSeq is the seq stream has committed through: 0 for one that never
// committed a row.
func (h *Host) highSeq(ctx context.Context, stream string) (int64, error) {
	meta, err := h.runtime.Results.Backend.Meta(ctx, stream)
	if errors.Is(err, recordstore.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return meta.HighSeq, nil
}
