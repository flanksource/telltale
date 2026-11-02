package telltale

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// enabledKey is the switch each capture's object in a StartRequest carries
// beside its kind's params.
const enabledKey = "enabled"

// StartRequest is a run as a client asks for it: what to run (a shell
// command, a pid, or neither for a host run) and what to trace, either a
// stored trace definition by name or, per kind, whether to arm it and its
// params (the shape StartSchema describes), never both.
type StartRequest struct {
	// Definition names the stored trace definition whose captures the run
	// arms.
	Definition string                     `json:"definition,omitempty"`
	PID        int                        `json:"pid,omitempty"`
	Duration   string                     `json:"duration,omitempty"`
	Every      string                     `json:"every,omitempty"`
	Command    string                     `json:"command"`
	Tree       traces.TreeOptions         `json:"tree,omitzero"`
	Traces     map[string]json.RawMessage `json:"traces,omitempty"`
}

// FieldError is one refused field of a StartRequest, at its JSON pointer.
type FieldError struct {
	InstancePath string `json:"instancePath"`
	Message      string `json:"message"`
}

// ValidationError is a StartRequest refused field by field.
type ValidationError struct {
	Fields []FieldError `json:"errors"`
}

func (e *ValidationError) Error() string {
	messages := make([]string, 0, len(e.Fields))
	for _, field := range e.Fields {
		messages = append(messages, field.InstancePath+": "+field.Message)
	}
	return "invalid start request: " + strings.Join(messages, "; ")
}

// Definitions are the trace definitions a Handler serves at
// <prefix>/definitions and arms the runs it starts with, such as
// definition.NewService.
type Definitions interface {
	// ServeHTTP serves the definitions routes with the handler's prefix
	// stripped: /definitions and below.
	http.Handler
	// Arm is options with request's captures armed: the stored definition
	// it names, else its ad-hoc traces compiled as an inline definition. A
	// refusal is a *ValidationError.
	Arm(request StartRequest, options StartOptions) (StartOptions, error)
}

// RequestedCapture is one enabled capture of a StartRequest's traces.
type RequestedCapture struct {
	Kind string
	// Params are the kind's params as the request gives them, without the
	// enabled switch and the target.
	Params map[string]any
	Target target.Target
	// Seam is the seam the capture is aimed at: its target's, an agent's
	// when it names one, else its kind's default.
	Seam target.Seam
}

// Captures are the request's enabled captures by kind, and the fields of
// those it cannot read.
func (r StartRequest) Captures() ([]RequestedCapture, []FieldError) {
	var captures []RequestedCapture
	var fields []FieldError
	for _, kind := range slices.Sorted(maps.Keys(r.Traces)) {
		capture, enabled, err := requestedCapture(kind, r.Traces[kind])
		if err != nil {
			fields = append(fields, FieldError{InstancePath: "/traces/" + kind, Message: err.Error()})
		} else if enabled {
			captures = append(captures, capture)
		}
	}
	return captures, fields
}

func requestedCapture(kind string, raw json.RawMessage) (RequestedCapture, bool, error) {
	capture := RequestedCapture{Kind: kind}
	if err := json.Unmarshal(raw, &capture.Params); err != nil || capture.Params == nil {
		return capture, false, fmt.Errorf("want an object: %v", err)
	}
	enabled, ok := capture.Params[enabledKey].(bool)
	if _, set := capture.Params[enabledKey]; set && !ok {
		return capture, false, fmt.Errorf("%s: want a boolean", enabledKey)
	}
	delete(capture.Params, enabledKey)
	if !enabled {
		return capture, false, nil
	}
	if selection, set := capture.Params["target"]; set {
		data, err := json.Marshal(selection)
		if err == nil {
			err = json.Unmarshal(data, &capture.Target)
		}
		if err != nil {
			return capture, false, fmt.Errorf("target: %w", err)
		}
		delete(capture.Params, "target")
	}
	registration, err := traces.Registered(kind)
	if err != nil {
		return capture, false, err
	}
	switch {
	case capture.Target.Seam != "":
		capture.Seam = capture.Target.Seam
	case capture.Target.Agent != "" || len(capture.Target.Selector) > 0:
		capture.Seam = target.Agent
	default:
		capture.Seam = registration.DefaultSeam
	}
	return capture, true, nil
}

// Options are the StartOptions the request asks for, over base (its data
// dir and connections): its command run with `sh -c` in the server's working
// directory and traced from its own root (TraceRootCommand), its captures
// armed by definitions. A refused request is a *ValidationError naming each
// field.
func (r StartRequest) Options(base StartOptions, definitions Definitions) (StartOptions, error) {
	options, fields := r.execution(base)
	if r.Definition != "" && len(r.Traces) > 0 {
		fields = append(fields, FieldError{InstancePath: "/definition", Message: "start a stored definition or ad-hoc traces, not both"})
		return StartOptions{}, &ValidationError{Fields: fields}
	}
	// The fields of unreadable captures are definitions.Arm's to refuse, as it
	// compiles the same captures.
	captures, _ := r.Captures()
	needsCommand := r.Definition == "" && len(captures) == 0
	for _, capture := range captures {
		needsCommand = needsCommand || capture.Seam == target.Proxy || capture.Seam == target.Process
	}
	if options.host() && needsCommand {
		fields = append(fields, FieldError{InstancePath: "/command", Message: "a command is required"})
	}
	armed, err := definitions.Arm(r, options)
	var invalid *ValidationError
	switch {
	case errors.As(err, &invalid):
		fields = append(fields, invalid.Fields...)
	case err != nil:
		return StartOptions{}, err
	}
	if len(fields) > 0 {
		return StartOptions{}, &ValidationError{Fields: fields}
	}
	if err := armed.validate(); err != nil {
		return StartOptions{}, &ValidationError{Fields: []FieldError{{InstancePath: "/command", Message: err.Error()}}}
	}
	if _, err := aimedSpecs(armed); err != nil {
		pointer := "/traces"
		switch {
		case armed.host():
			pointer = "/command"
		case r.Definition != "":
			pointer = "/definition"
		}
		return StartOptions{}, &ValidationError{Fields: []FieldError{{InstancePath: pointer, Message: err.Error()}}}
	}
	return armed, nil
}

// execution is base with what the request runs and for how long.
func (r StartRequest) execution(base StartOptions) (StartOptions, []FieldError) {
	options := base
	options.PID, options.Tree, options.TraceRoot = r.PID, r.Tree, TraceRootCommand
	if strings.TrimSpace(r.Command) != "" {
		options.Command = []string{"sh", "-c", r.Command}
	}
	var fields []FieldError
	for _, field := range []struct {
		name, value string
		dest        *time.Duration
	}{{"duration", r.Duration, &options.Duration}, {"every", r.Every, &options.Every}} {
		if field.value == "" {
			continue
		}
		value, err := time.ParseDuration(field.value)
		if err != nil || value < 0 {
			fields = append(fields, FieldError{InstancePath: "/" + field.name, Message: "must be a nonnegative duration"})
			continue
		}
		*field.dest = value
	}
	return options, fields
}
