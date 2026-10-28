package telltale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

type profileParams struct {
	Profile string `json:"profile"`
	Seconds int    `json:"seconds"`
	Mutex   bool   `json:"mutex,omitempty"`
	Block   bool   `json:"block,omitempty"`
}
type goProfileProbe struct{}

var profileMu sync.Mutex
var mutexArmed bool
var blockSessions int
var blockBaseline int

func init() { RegisterProbe(goProfileProbe{}); RegisterProbe(stacksProbe{}) }

func (goProfileProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: "goprof", Title: "Go profiles", Checkpoints: true, Mutates: true, Reverts: true}
}

func (goProfileProbe) Mutating(raw json.RawMessage) (bool, error) {
	var params profileParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return false, fmt.Errorf("goprof params: %w", err)
	}
	return params.Profile == "mutex" || params.Profile == "block" || params.Mutex || params.Block, nil
}

func (goProfileProbe) Arm(ctx context.Context, raw json.RawMessage, sink Sink) (func() error, error) {
	var params profileParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("goprof params: %w", err)
	}
	switch params.Profile {
	case "heap", "allocs", "goroutine":
		return armRates(params)
	case "mutex":
		return armMutex()
	case "block":
		return armBlock()
	case "cpu":
		return armCPU(ctx, params.Seconds, sink)
	default:
		return nil, fmt.Errorf("goprof profile %q: expected cpu, heap, allocs, goroutine, mutex or block", params.Profile)
	}
}

func armRates(params profileParams) (func() error, error) {
	var releases []func() error
	for _, request := range []struct {
		enabled bool
		arm     func() (func() error, error)
	}{{params.Mutex, armMutex}, {params.Block, armBlock}} {
		if !request.enabled {
			continue
		}
		release, err := request.arm()
		if err != nil {
			for _, undo := range releases {
				err = errors.Join(err, undo())
			}
			return nil, err
		}
		releases = append(releases, release)
	}
	return func() error {
		var errs []error
		for _, release := range releases {
			errs = append(errs, release())
		}
		return errors.Join(errs...)
	}, nil
}

func armMutex() (func() error, error) {
	profileMu.Lock()
	defer profileMu.Unlock()
	if mutexArmed {
		return nil, fmt.Errorf("goprof mutex already armed")
	}
	mutexArmed = true
	previous := runtime.SetMutexProfileFraction(1)
	return func() error {
		profileMu.Lock()
		defer profileMu.Unlock()
		runtime.SetMutexProfileFraction(previous)
		mutexArmed = false
		return nil
	}, nil
}

func armBlock() (func() error, error) {
	profileMu.Lock()
	if blockSessions == 0 {
		runtime.SetBlockProfileRate(1)
	}
	blockSessions++
	profileMu.Unlock()
	return func() error {
		profileMu.Lock()
		defer profileMu.Unlock()
		blockSessions--
		if blockSessions == 0 {
			runtime.SetBlockProfileRate(blockBaseline)
		}
		return nil
	}, nil
}

func armCPU(ctx context.Context, seconds int, sink Sink) (func() error, error) {
	if seconds <= 0 {
		return nil, fmt.Errorf("goprof cpu seconds must be positive")
	}
	var data bytes.Buffer
	if err := pprof.StartCPUProfile(&data); err != nil {
		return nil, fmt.Errorf("goprof cpu already armed: %w", err)
	}
	finished := make(chan error, 1)
	go func() {
		defer func() {
			if value := recover(); value != nil {
				err := panicError(value)
				finished <- err
				if target, ok := sink.(interface{ Fail(error) }); ok {
					target.Fail(err)
				}
			}
		}()
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
		pprof.StopCPUProfile()
		if sink != nil {
			finished <- sink.Blob("cpu", protocol.ContentTypePprof, data.Bytes(), nil)
		} else {
			finished <- nil
		}
	}()
	return func() error { return <-finished }, nil
}

func (goProfileProbe) Checkpoint(_ context.Context, raw json.RawMessage, sink Sink) error {
	var params profileParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return fmt.Errorf("goprof checkpoint params: %w", err)
	}
	if params.Profile != "heap" && params.Profile != "allocs" && params.Profile != "goroutine" && params.Profile != "mutex" && params.Profile != "block" {
		return fmt.Errorf("goprof checkpoint profile %q is unsupported", params.Profile)
	}
	var data bytes.Buffer
	if err := pprof.Lookup(params.Profile).WriteTo(&data, 0); err != nil {
		return err
	}
	return sink.Blob(params.Profile, protocol.ContentTypePprof, data.Bytes(), nil)
}

type stacksProbe struct{}

func (stacksProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: "stacks", Title: "Goroutine stacks", Checkpoints: true}
}
func (stacksProbe) Arm(context.Context, json.RawMessage, Sink) (func() error, error) {
	return func() error { return nil }, nil
}
func (stacksProbe) Checkpoint(_ context.Context, _ json.RawMessage, sink Sink) error {
	var data bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&data, 2); err != nil {
		return err
	}
	return sink.Blob("stacks", "text/plain", data.Bytes(), nil)
}
