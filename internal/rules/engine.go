package rules

import (
	"context"
	"fmt"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/google/uuid"
	"reflect"
	"slices"
	"strings"
	"time"
)

type Event struct {
	Kind     string      `json:"kind"`
	At       time.Time   `json:"at"`
	Incident Incident    `json:"incident"`
	Decision *Decision   `json:"decision,omitempty"`
	Result   *HookResult `json:"result,omitempty"`
	Mode     string      `json:"mode,omitempty"`
}
type Engine struct {
	program  *Program
	every    time.Duration
	last     time.Time
	states   map[string]Incident
	inflight map[string]bool
}

func NewEngine(program *Program, every time.Duration) *Engine {
	return &Engine{program: program, every: every, states: map[string]Incident{}, inflight: map[string]bool{}}
}
func stateKey(rule, group string) string { return rule + "\x00" + group }
func (e *Engine) Advance(ctx context.Context, snapshot groups.Snapshot) ([]Decision, []Event, error) {
	at := snapshot.Frame.SampledAt
	if at.IsZero() || !e.last.IsZero() && !at.After(e.last) {
		return nil, nil, fmt.Errorf("snapshot time must advance")
	}
	gap := !e.last.IsZero() && at.Sub(e.last) > 2*e.every
	e.last = at
	decisions := []Decision{}
	events := []Event{}
	alive := map[string]bool{}
	for _, rule := range e.program.rules {
		resolved, host, err := Resolve(snapshot, rule.rule.GroupBy)
		if err != nil {
			return nil, nil, err
		}
		for _, group := range resolved {
			key := stateKey(rule.rule.Name, group.Key)
			alive[key] = true
			previous := e.states[key]
			decision, state, err := e.advance(ctx, rule, group, host, at, gap)
			if err != nil {
				state.Error = err.Error()
				state.Since = time.Time{}
				state.ReadyAt = time.Time{}
				state.RecoveringAt = time.Time{}
			}
			if state.ID == "" {
				continue
			}
			if !reflect.DeepEqual(previous, state) {
				state.UpdatedAt = at
				e.states[key] = state
				events = append(events, Event{Kind: "state", At: at, Incident: state})
			}
			if decision != nil {
				decision.Incident = state
				decisions = append(decisions, *decision)
			}
		}
	}
	for key, state := range e.states {
		if state.State == "closed" && !e.inflight[state.ID] {
			delete(e.states, key)
			continue
		}
		if !alive[key] && state.State != "closed" {
			state.State = "closed"
			state.Error = "group disappeared"
			state.UpdatedAt = at
			e.states[key] = state
			events = append(events, Event{Kind: "state", At: at, Incident: state})
		}
	}
	return decisions, events, nil
}
func (e *Engine) advance(ctx context.Context, r compiledRule, g MonitorGroup, h Host, at time.Time, gap bool) (*Decision, Incident, error) {
	state := e.states[stateKey(r.rule.Name, g.Key)]
	matched, known, err := r.match.predicate(ctx, g, h, at, state)
	if err != nil && state.ID == "" {
		state = Incident{ID: uuid.NewString(), Rule: r.rule.Name, Group: g.Key, Revision: e.program.Revision, State: "pending"}
	}
	if err != nil {
		return nil, state, err
	}
	if !known {
		state.Since = time.Time{}
		state.ReadyAt = time.Time{}
		state.RecoveringAt = time.Time{}
		return nil, state, nil
	}
	if gap {
		state.Since = time.Time{}
		state.ReadyAt = time.Time{}
		state.RecoveringAt = time.Time{}
	}
	if !matched {
		return e.recover(ctx, r, g, h, at, state)
	}
	if state.ID == "" || state.State == "closed" {
		state = Incident{ID: uuid.NewString(), Rule: r.rule.Name, Group: g.Key, Revision: e.program.Revision, State: "pending", Since: at}
	}
	state.Error = ""
	state.RecoveringAt = time.Time{}
	if gap {
		state.Since = time.Time{}
		state.ReadyAt = time.Time{}
	}
	if state.Since.IsZero() {
		state.Since = at
	}
	if state.State == "pending" {
		if at.Sub(state.Since) < r.hold {
			return nil, state, nil
		}
		state.State = "breached"
	}
	if state.State == "recovering" {
		state.State = "breached"
	}
	if state.Step >= len(r.waits) || e.inflight[state.ID] {
		return nil, state, nil
	}
	if state.ReadyAt.IsZero() {
		state.ReadyAt = at.Add(r.waits[state.Step])
	}
	if at.Before(state.ReadyAt) {
		return nil, state, nil
	}
	targets, err := r.targets[state.Step].targets(ctx, g, h, at, state)
	if err != nil {
		return nil, state, err
	}
	return &Decision{Incident: state, Group: g, Host: h, Hook: r.rule.Steps[state.Step].Hook, Targets: targets, SampledAt: at}, state, nil
}
func (e *Engine) recover(ctx context.Context, r compiledRule, g MonitorGroup, h Host, at time.Time, state Incident) (*Decision, Incident, error) {
	if state.ID == "" || state.State == "closed" {
		return nil, state, nil
	}
	state.Since = time.Time{}
	state.ReadyAt = time.Time{}
	if state.State == "pending" {
		state.State = "closed"
		return nil, state, nil
	}
	recovered, known, err := r.recover.predicate(ctx, g, h, at, state)
	if err != nil {
		return nil, state, err
	}
	if !known || !recovered {
		state.RecoveringAt = time.Time{}
		return nil, state, nil
	}
	state.State = "recovering"
	if state.RecoveringAt.IsZero() {
		state.RecoveringAt = at
	}
	if at.Sub(state.RecoveringAt) >= r.clear {
		state.State = "closed"
	}
	return nil, state, nil
}
func (e *Engine) Reserve(d Decision) { e.inflight[d.Incident.ID] = true }
func (e *Engine) BreakContinuity() {
	for key, state := range e.states {
		state.Since = time.Time{}
		state.ReadyAt = time.Time{}
		state.RecoveringAt = time.Time{}
		e.states[key] = state
	}
}
func (e *Engine) Complete(d Decision, at time.Time, result HookResult) Event {
	key := stateKey(d.Incident.Rule, d.Incident.Group)
	state := e.states[key]
	delete(e.inflight, d.Incident.ID)
	if state.ID != d.Incident.ID {
		state = d.Incident
		state.State = "closed"
		state.Step++
		state.UpdatedAt = at
		return Event{Kind: "result", At: at, Incident: state, Decision: &d, Result: &result}
	}
	if state.ID == d.Incident.ID {
		state.Step++
		state.UpdatedAt = at
		state.ReadyAt = time.Time{}
		for _, rule := range e.program.rules {
			if rule.rule.Name == state.Rule && state.Step < len(rule.waits) && state.State == "breached" && !state.Since.IsZero() {
				state.ReadyAt = at.Add(rule.waits[state.Step])
			}
		}
		e.states[key] = state
	}
	return Event{Kind: "result", At: at, Incident: state, Decision: &d, Result: &result}
}
func (e *Engine) States() []Incident {
	states := make([]Incident, 0, len(e.states))
	for _, state := range e.states {
		states = append(states, state)
	}
	slices.SortFunc(states, func(a, b Incident) int { return strings.Compare(stateKey(a.Rule, a.Group), stateKey(b.Rule, b.Group)) })
	return states
}
func (e *Engine) Restore(events []Event) {
	unresolved := map[string]Event{}
	for _, event := range events {
		state := event.Incident
		if state.Revision != e.program.Revision {
			continue
		}
		if event.Kind == "intent" {
			unresolved[state.ID] = event
		}
		if event.Kind == "result" {
			delete(unresolved, state.ID)
			if current := e.states[stateKey(state.Rule, state.Group)]; current.ID != "" && current.ID != state.ID {
				continue
			}
		}
		e.states[stateKey(state.Rule, state.Group)] = state
	}
	for _, event := range unresolved {
		state := e.states[stateKey(event.Incident.Rule, event.Incident.Group)]
		if state.ID != event.Incident.ID || state.State == "closed" {
			continue
		}
		state.Step++
		state.Error = "previous hook attempt has unknown outcome"
		e.states[stateKey(state.Rule, state.Group)] = state
	}
	for key, state := range e.states {
		state.Since = time.Time{}
		state.ReadyAt = time.Time{}
		state.RecoveringAt = time.Time{}
		e.states[key] = state
	}
}
