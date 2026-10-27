package rules

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/clicky/task"
	cctx "github.com/flanksource/commons/context"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

type Source interface {
	Snapshot(context.Context) (groups.Snapshot, error)
	Close()
}

var ErrNotReady = errors.New("resource monitoring is not ready")

type ServiceOptions struct {
	Program *Program
	Source  Source
	Journal Journal
	Runner  HookRunner
	Every   time.Duration
	Enforce bool
}
type Status struct {
	Enabled   bool       `json:"enabled"`
	Enforcing bool       `json:"enforcing"`
	Revision  string     `json:"revision"`
	SampledAt time.Time  `json:"sampledAt"`
	Error     string     `json:"error,omitempty"`
	Incidents []Incident `json:"incidents"`
}
type Service struct {
	mu           sync.Mutex
	options      ServiceOptions
	engine       *Engine
	latest       groups.Snapshot
	due          []Decision
	failure      string
	fatal        bool
	cancel       context.CancelFunc
	activeCancel context.CancelFunc
	activeID     string
	wake         chan struct{}
	sampleTask   task.TypedTask[bool]
	dispatchTask task.TypedTask[bool]
	unlock       func() error
}

func Open(workDir, path string, enforce bool) (*Service, error) {
	program, err := Load(path)
	if err != nil {
		return nil, err
	}
	catalogPath, err := groups.CatalogFile(workDir)
	if err != nil {
		return nil, err
	}
	options, err := (groups.Params{Catalog: catalogPath, Interval: groups.DefaultInterval.String()}).Options()
	if err != nil {
		return nil, err
	}
	source, err := groups.NewTop(context.Background(), process.NewSampler(), options)
	if err != nil {
		return nil, err
	}
	journal, err := OpenJournal(workDir)
	if err != nil {
		source.Close()
		return nil, err
	}
	service, err := NewService(ServiceOptions{Program: program, Source: source, Journal: journal, Runner: NewExecutor(), Every: groups.DefaultInterval, Enforce: enforce})
	if err != nil {
		source.Close()
		return nil, errors.Join(err, journal.Close())
	}
	return service, nil
}
func NewService(options ServiceOptions) (*Service, error) {
	if options.Program == nil || options.Source == nil || options.Journal == nil || options.Runner == nil || options.Every <= 0 {
		return nil, fmt.Errorf("program, source, journal, runner and positive interval are required")
	}
	service := &Service{options: options, engine: NewEngine(options.Program, options.Every), wake: make(chan struct{}, 1)}
	if options.Enforce {
		unlock, err := acquireLock()
		if err != nil {
			return nil, err
		}
		service.unlock = unlock
	}
	events, err := options.Journal.Events(context.Background())
	if err != nil {
		if service.unlock != nil {
			err = errors.Join(err, service.unlock())
		}
		return nil, err
	}
	mode := "observe"
	if options.Enforce {
		mode = "enforce"
	}
	restored := []Event{}
	for _, event := range events {
		if event.Mode == mode {
			restored = append(restored, event)
		}
	}
	service.engine.Restore(restored)
	return service, nil
}
func (s *Service) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.sampleTask = task.StartTask("Monitor resource rules", func(_ cctx.Context, _ *task.Task) (bool, error) { return true, s.sample(ctx) }, task.WithContext(ctx), task.WithCancellationDrain())
	s.dispatchTask = task.StartTask("Enforce resource rules", func(_ cctx.Context, _ *task.Task) (bool, error) { return true, s.dispatch(ctx) }, task.WithContext(ctx), task.WithCancellationDrain())
}
func (s *Service) sample(ctx context.Context) error {
	ticker := time.NewTicker(s.options.Every)
	defer ticker.Stop()
	for {
		snapshot, err := s.options.Source.Snapshot(ctx)
		s.observe(ctx, snapshot, err)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (s *Service) observe(ctx context.Context, snapshot groups.Snapshot, readErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fatal {
		return
	}
	if readErr != nil {
		s.engine.BreakContinuity()
		s.failure = readErr.Error()
		s.due = nil
		if s.activeCancel != nil {
			s.activeCancel()
		}
		return
	}
	decisions, events, err := s.engine.Advance(ctx, snapshot)
	if err != nil {
		s.engine.BreakContinuity()
		s.failure = err.Error()
		s.due = nil
		if s.activeCancel != nil {
			s.activeCancel()
		}
		return
	}
	s.latest = snapshot
	s.failure = ""
	for _, event := range events {
		if err := s.persist(ctx, event); err != nil {
			s.storageFailure(err)
			return
		}
	}
	s.due = decisions
	if s.activeCancel != nil {
		for _, state := range s.engine.states {
			if state.ID == s.activeID && (state.State == "closed" || state.Since.IsZero()) {
				s.activeCancel()
			}
		}
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) storageFailure(err error) {
	s.failure = "incident persistence failed: " + err.Error()
	s.fatal = true
	s.due = nil
	if s.activeCancel != nil {
		s.activeCancel()
	}
}
func (s *Service) dispatch(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.wake:
		}
		completed := map[string]HookResult{}
		for {
			decision, jobCtx, cancel, ok := s.next(ctx)
			if !ok {
				break
			}
			result := HookResult{Status: "observed"}
			if len(decision.Targets.Processes) == 0 && len(decision.Targets.Sessions) == 0 {
				result.Status = "no-targets"
			}
			identity := actionIdentity(decision)
			if previous, ok := completed[identity]; ok && decision.Hook.Type != "exec" {
				result = previous
				result.Coalesced = true
			} else if s.options.Enforce {
				result = s.options.Runner.Run(jobCtx, decision)
			}
			completed[identity] = result
			cancel()
			s.mu.Lock()
			s.activeCancel = nil
			s.activeID = ""
			event := s.engine.Complete(decision, time.Now().UTC(), result)
			if err := s.persist(context.WithoutCancel(ctx), event); err != nil {
				s.storageFailure(err)
			}
			s.mu.Unlock()
		}
	}
}
func (s *Service) next(ctx context.Context) (Decision, context.Context, context.CancelFunc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fatal || s.failure != "" || len(s.due) == 0 || ctx.Err() != nil {
		return Decision{}, nil, nil, false
	}
	d := s.due[0]
	s.due = s.due[1:]
	if time.Since(d.SampledAt) > 2*s.options.Every {
		return Decision{}, nil, nil, false
	}
	if err := s.persist(ctx, Event{Kind: "intent", At: time.Now().UTC(), Incident: d.Incident, Decision: &d}); err != nil {
		s.storageFailure(err)
		return Decision{}, nil, nil, false
	}
	s.engine.Reserve(d)
	jobCtx, cancel := context.WithCancel(ctx)
	s.activeCancel, s.activeID = cancel, d.Incident.ID
	return d, jobCtx, cancel, true
}
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{Enabled: true, Enforcing: s.options.Enforce, Revision: s.options.Program.Revision, SampledAt: s.latest.Frame.SampledAt, Error: s.failure, Incidents: s.engine.States()}
}
func (s *Service) Program() *Program { return s.options.Program }
func (s *Service) Preview(ctx context.Context) ([]Preview, error) {
	s.mu.Lock()
	snapshot := s.latest
	failure := s.failure
	s.mu.Unlock()
	if snapshot.Frame.SampledAt.IsZero() {
		return nil, fmt.Errorf("%w: waiting for first snapshot", ErrNotReady)
	}
	if failure != "" {
		return nil, fmt.Errorf("%w: %s", ErrNotReady, failure)
	}
	if time.Since(snapshot.Frame.SampledAt) > 2*s.options.Every {
		return nil, fmt.Errorf("%w: latest snapshot is stale", ErrNotReady)
	}
	return s.options.Program.Preview(ctx, snapshot)
}
func (s *Service) Events(ctx context.Context) ([]Event, error) { return s.options.Journal.Events(ctx) }
func (s *Service) persist(ctx context.Context, event Event) error {
	event.Mode = "observe"
	if s.options.Enforce {
		event.Mode = "enforce"
	}
	return s.options.Journal.Append(ctx, event)
}
func actionIdentity(d Decision) string {
	keys := []string{}
	for _, p := range d.Targets.Processes {
		keys = append(keys, "process:"+p.Key)
	}
	for _, session := range d.Targets.Sessions {
		keys = append(keys, "session:"+session.Key)
	}
	slices.Sort(keys)
	nice := ""
	if d.Hook.Nice != nil {
		nice = fmt.Sprint(*d.Hook.Nice)
	}
	return strings.Join(append([]string{d.SampledAt.Format(time.RFC3339Nano), d.Hook.Type, d.Hook.Signal, d.Hook.Via, d.Hook.Endpoint, d.Hook.Key, nice}, keys...), "\x00")
}
func (s *Service) Close() error {
	var err error
	if s.cancel != nil {
		s.cancel()
		_, sampleErr := s.sampleTask.GetResult()
		_, dispatchErr := s.dispatchTask.GetResult()
		if errors.Is(sampleErr, context.Canceled) {
			sampleErr = nil
		}
		if errors.Is(dispatchErr, context.Canceled) {
			dispatchErr = nil
		}
		err = errors.Join(sampleErr, dispatchErr)
	}
	s.options.Source.Close()
	err = errors.Join(err, s.options.Journal.Close())
	if s.unlock != nil {
		err = errors.Join(err, s.unlock())
	}
	return err
}
