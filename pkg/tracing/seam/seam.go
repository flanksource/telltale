// Package seam connects trace kinds to SDK probes.
package seam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sync"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	telltale "github.com/flanksource/telltale/sdk"
)

type AgentSeam[P, R any] struct {
	Probe        string
	Dir          string
	Registry     *agentclient.Registry
	Target       func(P) target.Target
	ArmParams    func(P) (json.RawMessage, error)
	Mutates      func(P) bool
	DecodeRecord func(json.RawMessage) (R, error)
	DecodeBlob   func(agentclient.Blob, map[string]string) ([]R, error)
}

type stateKey struct{}
type capture struct {
	mu          sync.Mutex
	sessions    map[*agentclient.Agent]*agentclient.Session
	dropped     map[*agentclient.Agent]uint64
	joins       <-chan *agentclient.Agent
	unsubscribe func()
	local       *telltale.Agent
	localClient *agentclient.Agent
	report      map[string]any
}

type agentFailure struct {
	agent    *agentclient.Agent
	err      error
	departed bool
}

func (s AgentSeam[P, R]) registry() *agentclient.Registry {
	if s.Registry != nil {
		return s.Registry
	}
	return agentclient.Default
}

// Available discovers local agents and checks whether the target advertises the probe.
func (s AgentSeam[P, R]) Available(probe string, selection target.Target) (bool, error) {
	registry := s.registry()
	if selection.PID != 0 && registry.HasProbe(selection.PID, probe) {
		return true, nil
	}
	if selection.Agent != "" {
		agent := registry.ByID(selection.Agent)
		if agent != nil && agent.HasProbe(probe) {
			return true, nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := registry.Discover(ctx, s.Dir); err != nil {
		return false, err
	}
	if selection.PID != 0 {
		return registry.HasProbe(selection.PID, probe), nil
	}
	if selection.Agent != "" {
		agent := registry.ByID(selection.Agent)
		return agent != nil && agent.HasProbe(probe), nil
	}
	for _, agent := range registry.Match(selection.Selector) {
		if agent.HasProbe(probe) {
			return true, nil
		}
	}
	return false, nil
}

// Prepare arms the selected agents before a capture starts.
func (s AgentSeam[P, R]) Prepare(ctx dbcontext.Context, params P, out tracing.Emitter[R]) (dbcontext.Context, func() error, error) {
	selection := s.Target(params)
	state := &capture{sessions: make(map[*agentclient.Agent]*agentclient.Session), dropped: make(map[*agentclient.Agent]uint64), report: make(map[string]any)}
	var agents []*agentclient.Agent
	if selection.Seam == target.Self {
		state.local = telltale.Loopback()
		client, err := agentclient.Connect(ctx, state.local.Connect(), "")
		if err != nil {
			return ctx, nil, errors.Join(err, state.local.Close())
		}
		state.localClient = client
		agents = []*agentclient.Agent{client}
	} else {
		registry := s.registry()
		state.joins, state.unsubscribe = registry.Subscribe()
		switch {
		case selection.PID != 0:
			if a := registry.ByPID(selection.PID); a != nil {
				agents = append(agents, a)
			}
		case selection.Agent != "":
			if a := registry.ByID(selection.Agent); a != nil {
				agents = append(agents, a)
			}
		default:
			agents = registry.Match(selection.Selector)
		}
	}
	var armErrs []error
	for _, agent := range agents {
		if !agent.HasProbe(s.Probe) {
			continue
		}
		if err := s.arm(ctx, state, agent, params); err != nil {
			if len(selection.Selector) > 0 {
				out.Warn(err.Error())
				armErrs = append(armErrs, err)
				continue
			}
			return ctx, nil, errors.Join(err, s.release(state))
		}
	}
	if len(state.sessions) == 0 && selection.Wait {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for len(state.sessions) == 0 {
			select {
			case <-ctx.Done():
				return ctx, nil, errors.Join(ctx.Err(), s.release(state))
			case <-ticker.C:
				if err := s.registry().Discover(ctx, s.Dir); err != nil {
					return ctx, nil, errors.Join(err, s.release(state))
				}
			case agent := <-state.joins:
				if matches(agent, selection.Selector) && agent.HasProbe(s.Probe) {
					if err := s.arm(ctx, state, agent, params); err != nil {
						out.Warn(err.Error())
						continue
					}
				}
			}
		}
	}
	if len(state.sessions) == 0 {
		return ctx, nil, errors.Join(fmt.Errorf("no agent armed probe %s", s.Probe), errors.Join(armErrs...), s.release(state))
	}
	out.Describe("agent " + s.Probe)
	return ctx.WithValue(stateKey{}, state), func() error { return s.release(state) }, nil
}

func (s AgentSeam[P, R]) arm(ctx context.Context, state *capture, agent *agentclient.Agent, params P) error {
	data := json.RawMessage(`{}`)
	var err error
	if s.ArmParams != nil {
		data, err = s.ArmParams(params)
		if err != nil {
			return err
		}
	}
	mutates := s.Mutates != nil && s.Mutates(params)
	session, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: s.Probe, Params: data, Mutates: mutates, AllowMutation: s.Target(params).AllowMutation})
	if err != nil {
		return fmt.Errorf("arm %s on %s: %w", s.Probe, agent.Hello.AgentID, err)
	}
	state.mu.Lock()
	state.sessions[agent] = session
	state.mu.Unlock()
	return nil
}

func (s AgentSeam[P, R]) release(state *capture) error {
	if state.unsubscribe != nil {
		state.unsubscribe()
	}
	state.mu.Lock()
	sessions := maps.Clone(state.sessions)
	state.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var errs []error
	for _, session := range sessions {
		errs = append(errs, session.Disarm(ctx))
	}
	if state.localClient != nil {
		errs = append(errs, state.localClient.Close())
	}
	if state.local != nil {
		errs = append(errs, state.local.Close())
	}
	return errors.Join(errs...)
}

func (s AgentSeam[P, R]) Checkpoint(ctx context.Context, raw json.RawMessage, out tracing.Emitter[R]) error {
	state, ok := ctx.Value(stateKey{}).(*capture)
	if !ok {
		return errors.New("agent seam is not prepared")
	}
	state.mu.Lock()
	sessions := maps.Clone(state.sessions)
	state.mu.Unlock()
	for agent, session := range sessions {
		result, err := session.Checkpoint(ctx, raw)
		if err != nil {
			return err
		}
		for _, batch := range result.Records {
			state.reportDropped(agent, batch.Dropped, out.Report)
			for _, item := range batch.Items {
				if err := s.emit(ctx, item, agent.Hello.Resource, out); err != nil {
					return err
				}
			}
		}
		for _, blob := range result.Blobs {
			if err := s.blob(ctx, blob, agent.Hello.Resource, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// Window arms a temporary probe for a CPU profile window.
func (s AgentSeam[P, R]) Window(ctx context.Context, params P, raw json.RawMessage, out tracing.Emitter[R]) error {
	state, ok := ctx.Value(stateKey{}).(*capture)
	if !ok {
		return errors.New("agent seam is not prepared")
	}
	state.mu.Lock()
	sessions := maps.Clone(state.sessions)
	state.mu.Unlock()
	for agent := range sessions {
		session, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: s.Probe, Params: raw, AllowMutation: s.Target(params).AllowMutation})
		if err != nil {
			return err
		}
		select {
		case blob := <-session.Blobs:
			blob.CheckpointParams = raw
			err = s.blob(ctx, blob, agent.Hello.Resource, out)
		case ended := <-session.Ended:
			err = fmt.Errorf("agent profile ended: %s", ended.Error)
		case <-ctx.Done():
			err = ctx.Err()
		}
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = errors.Join(err, session.Disarm(releaseCtx))
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s AgentSeam[P, R]) blob(ctx context.Context, blob agentclient.Blob, resource map[string]string, out tracing.Emitter[R]) error {
	if s.DecodeBlob == nil {
		return fmt.Errorf("probe %s has no blob decoder", s.Probe)
	}
	rows, err := s.DecodeBlob(blob, resource)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := out.Emit(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (s AgentSeam[P, R]) emit(ctx context.Context, raw json.RawMessage, resource map[string]string, out tracing.Emitter[R]) error {
	var row R
	var err error
	if s.DecodeRecord != nil {
		row, err = s.DecodeRecord(raw)
	} else {
		err = json.Unmarshal(raw, &row)
	}
	if err != nil {
		return fmt.Errorf("decode %s record: %w", s.Probe, err)
	}
	value := reflect.ValueOf(&row).Elem()
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return errors.New("agent record is nil")
		}
		value = value.Elem()
	}
	if value.Kind() == reflect.Struct {
		field := value.FieldByName("Resource")
		if field.IsValid() && field.CanSet() && field.Type() == reflect.TypeFor[map[string]string]() {
			merged := make(map[string]string, len(resource))
			for key, item := range resource {
				merged[key] = item
			}
			if !field.IsNil() {
				for key, item := range field.Interface().(map[string]string) {
					merged[key] = item
				}
			}
			field.Set(reflect.ValueOf(merged))
		}
	}
	return out.Emit(ctx, row)
}

// Handle delivers live frames until the capture ends.
func (s AgentSeam[P, R]) Handle(ctx dbcontext.Context, params P, out tracing.Emitter[R]) error {
	state, ok := ctx.Value(stateKey{}).(*capture)
	if !ok {
		return errors.New("agent seam is not prepared")
	}
	errs := make(chan agentFailure, 64)
	readCtx, cancelReads := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelReads()
	start := func(agent *agentclient.Agent, session *agentclient.Session) {
		go s.read(readCtx, state, agent, session, out, errs)
	}
	state.mu.Lock()
	for agent, session := range state.sessions {
		start(agent, session)
	}
	state.mu.Unlock()
	var tick <-chan time.Time
	if len(s.Target(params).Selector) > 0 {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		tick = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			state.mu.Lock()
			sessions := maps.Clone(state.sessions)
			state.mu.Unlock()
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			var stopErr error
			for _, session := range sessions {
				stopErr = errors.Join(stopErr, session.Disarm(stopCtx))
			}
			for range sessions {
				select {
				case failure := <-errs:
					if !failure.departed {
						stopErr = errors.Join(stopErr, failure.err)
					}
				case <-stopCtx.Done():
					return errors.Join(stopErr, stopCtx.Err())
				}
			}
			return stopErr
		case <-tick:
			if err := s.registry().Discover(ctx, s.Dir); err != nil {
				return fmt.Errorf("discover late agents: %w", err)
			}
		case failure := <-errs:
			if !failure.departed || len(s.Target(params).Selector) == 0 {
				return failure.err
			}
			state.mu.Lock()
			delete(state.sessions, failure.agent)
			state.mu.Unlock()
			out.Warn(failure.err.Error())
		case agent, ok := <-state.joins:
			if !ok {
				state.joins = nil
				continue
			}
			selection := s.Target(params)
			if len(selection.Selector) == 0 || !agent.HasProbe(s.Probe) {
				continue
			}
			matched := matches(agent, selection.Selector)
			state.mu.Lock()
			_, exists := state.sessions[agent]
			state.mu.Unlock()
			if !matched || exists {
				continue
			}
			if err := s.arm(ctx, state, agent, params); err != nil {
				out.Warn(err.Error())
				continue
			}
			state.mu.Lock()
			start(agent, state.sessions[agent])
			state.mu.Unlock()
		}
	}
}

func matches(agent *agentclient.Agent, selector map[string]string) bool {
	for key, value := range selector {
		if agent.Hello.Resource[key] != value && agent.Hello.Labels[key] != value {
			return false
		}
	}
	return true
}

func (s AgentSeam[P, R]) read(ctx context.Context, state *capture, agent *agentclient.Agent, session *agentclient.Session, out tracing.Emitter[R], errs chan<- agentFailure) {
	fail := func(err error, departed bool) {
		select {
		case errs <- agentFailure{agent: agent, err: err, departed: departed}:
		case <-ctx.Done():
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case batch := <-session.Records:
			for _, item := range batch.Items {
				if err := s.emit(ctx, item, agent.Hello.Resource, out); err != nil {
					fail(err, false)
					return
				}
			}
			state.reportDropped(agent, batch.Dropped, out.Report)
		case blob := <-session.Blobs:
			if err := s.blob(ctx, blob, agent.Hello.Resource, out); err != nil {
				fail(err, false)
				return
			}
		case warning := <-session.Warns:
			out.Warn(warning.Message)
		case report := <-session.Reports:
			if err := state.mergeReport(report.Summary, out.Report); err != nil {
				fail(err, false)
				return
			}
		case end := <-session.Ended:
			for {
				select {
				case batch := <-session.Records:
					for _, item := range batch.Items {
						if err := s.emit(ctx, item, agent.Hello.Resource, out); err != nil {
							fail(err, false)
							return
						}
					}
					state.reportDropped(agent, batch.Dropped, out.Report)
				default:
					goto drained
				}
			}
		drained:
			state.reportDropped(agent, end.Dropped, out.Report)
			if end.Error != "" {
				fail(fmt.Errorf("agent %s ended: %s", agent.Hello.AgentID, end.Error), true)
			} else {
				fail(fmt.Errorf("agent %s ended unexpectedly", agent.Hello.AgentID), true)
			}
			return
		}
	}
}

func (state *capture) reportDropped(agent *agentclient.Agent, count uint64, report func(any)) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if count > state.dropped[agent] {
		state.dropped[agent] = count
	}
	state.emitReport(report)
}

func (state *capture) mergeReport(summary json.RawMessage, report func(any)) error {
	var values map[string]any
	if err := json.Unmarshal(summary, &values); err != nil {
		return fmt.Errorf("decode agent report: %w", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	maps.Copy(state.report, values)
	state.emitReport(report)
	return nil
}

func (state *capture) emitReport(report func(any)) {
	values := maps.Clone(state.report)
	var total uint64
	for _, value := range state.dropped {
		total += value
	}
	if previous, ok := values["dropped"].(float64); ok {
		values["dropped"] = previous + float64(total)
	} else {
		values["dropped"] = total
	}
	report(values)
}
