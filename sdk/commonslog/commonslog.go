package commonslog

import (
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/properties"
	telltale "github.com/flanksource/telltale/sdk"
)

type capture struct {
	options telltale.LogOptions
	min     slog.Level
	sink    telltale.Sink
}
type baseline struct {
	property string
	exists   bool
	level    string
}

var state = struct {
	sync.Mutex
	captures map[*capture]struct{}
	baseline map[string]baseline
	pending  map[string]int
	version  uint64
}{captures: make(map[*capture]struct{}), baseline: make(map[string]baseline), pending: make(map[string]int)}

func init() {
	telltale.RegisterLogAdapter(arm)
	logger.ObserveCreated(applyCreated)
}

func arm(options telltale.LogOptions, sink telltale.Sink) (func() error, error) {
	c := &capture{options: options, min: slog.Level(-100), sink: sink}
	if options.MinLevel != "" {
		if err := c.min.UnmarshalText([]byte(options.MinLevel)); err != nil {
			return nil, fmt.Errorf("commons logger minLevel: %w", err)
		}
	}
	state.Lock()
	state.captures[c] = struct{}{}
	state.version++
	state.Unlock()
	for name := range logger.GetNamedLoggingLevels() {
		apply(name)
	}
	releaseObserver := logger.Observe(func(record logger.ObservedLog) {
		if record.Level >= c.min && strings.Contains(record.Message, c.options.Match) {
			c.sink.TryEmit(struct {
				FirstObserved time.Time `json:"firstObserved"`
				Message       string    `json:"message"`
				Severity      string    `json:"severity"`
				Logger        string    `json:"logger,omitempty"`
			}{record.Time, record.Message, record.Level.String(), record.Name})
		}
	})
	return func() error {
		releaseObserver()
		state.Lock()
		delete(state.captures, c)
		state.version++
		names := make([]string, 0, len(state.baseline))
		for name := range state.baseline {
			names = append(names, name)
		}
		state.Unlock()
		for _, name := range names {
			apply(name)
		}
		return nil
	}, nil
}

func applyCreated(name string) { apply(name) }

func apply(name string) {
	state.Lock()
	version := state.version
	var selected string
	var minimum slog.Level
	for c := range state.captures {
		for pattern, value := range c.options.Levels {
			matches, _ := path.Match(pattern, name)
			if !matches {
				continue
			}
			var level slog.Level
			if err := level.UnmarshalText([]byte(value)); err != nil {
				panic(err)
			}
			if selected == "" || level < minimum {
				selected, minimum = value, level
			}
		}
	}
	key := "log.level." + name
	if selected == "" {
		old, exists := state.baseline[name]
		if exists {
			state.pending[name]++
		}
		state.Unlock()
		if exists {
			if old.exists {
				properties.Set(key, old.property)
			} else {
				properties.Unset(key)
				logger.GetLogger(name).SetLogLevel(old.level)
			}
			state.Lock()
			state.pending[name]--
			if state.version == version && state.pending[name] == 0 {
				delete(state.baseline, name)
				delete(state.pending, name)
			}
			changed := state.version != version
			state.Unlock()
			if changed {
				apply(name)
			}
		}
		return
	}
	if _, exists := state.baseline[name]; !exists {
		value, found := properties.Global.GetAll()[key]
		state.baseline[name] = baseline{property: value, exists: found, level: logger.GetNamedLoggingLevels()[name]}
	}
	state.pending[name]++
	state.Unlock()
	properties.Set(key, selected)
	state.Lock()
	state.pending[name]--
	changed := state.version != version
	state.Unlock()
	if changed {
		apply(name)
	}
}
