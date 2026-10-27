package rules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type compiledRule struct {
	rule           Rule
	match, recover *expression
	targets        []*expression
	hold, clear    time.Duration
	waits          []time.Duration
}
type Program struct {
	Config   Config `json:"config"`
	Revision string `json:"revision"`
	rules    []compiledRule
}

func Load(path string) (*Program, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Compile(data)
}
func Compile(data []byte) (*Program, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("rules: expected one YAML document")
	}
	if config.Version != 1 || len(config.Rules) == 0 {
		return nil, fmt.Errorf("rules: version 1 and at least one rule are required")
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	program := &Program{Config: config, Revision: hex.EncodeToString(digest[:])}
	names := map[string]bool{}
	for _, rule := range config.Rules {
		if strings.TrimSpace(rule.Name) == "" || names[rule.Name] {
			return nil, fmt.Errorf("rule name %q is empty or duplicated", rule.Name)
		}
		names[rule.Name] = true
		compiled, err := compileRule(rule)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", rule.Name, err)
		}
		program.rules = append(program.rules, compiled)
	}
	return program, nil
}
func compileRule(rule Rule) (compiledRule, error) {
	c := compiledRule{rule: rule}
	if !slices.Contains([]string{"tree", "application", "repo", "worktree", "session", "host"}, rule.GroupBy) {
		return c, fmt.Errorf("invalid groupBy %q", rule.GroupBy)
	}
	var err error
	if c.hold, err = duration(rule.For); err != nil {
		return c, fmt.Errorf("for: %w", err)
	}
	if c.clear, err = duration(rule.RecoverFor); err != nil {
		return c, fmt.Errorf("recoverFor: %w", err)
	}
	if c.match, err = compileExpression(rule.Match, "bool", false); err != nil {
		return c, fmt.Errorf("match: %w", err)
	}
	recovery := rule.Recover
	if recovery == "" {
		recovery = "!(" + rule.Match + ")"
	}
	if c.recover, err = compileExpression(recovery, "bool", false); err != nil {
		return c, fmt.Errorf("recover: %w", err)
	}
	if len(rule.Steps) == 0 {
		return c, fmt.Errorf("steps are required")
	}
	for i, step := range rule.Steps {
		wait, err := duration(step.Wait)
		if err != nil {
			return c, fmt.Errorf("steps[%d].wait: %w", i, err)
		}
		if err = validateHook(step.Hook); err != nil {
			return c, fmt.Errorf("steps[%d].hook: %w", i, err)
		}
		kind := "process"
		if step.Hook.Type == "session-interrupt" {
			kind = "session"
		}
		if step.Hook.Type == "exec" {
			kind = "either"
		}
		targets, err := compileExpression(step.Hook.Targets, kind, true)
		if err != nil {
			return c, fmt.Errorf("steps[%d].targets: %w", i, err)
		}
		c.waits = append(c.waits, wait)
		c.targets = append(c.targets, targets)
	}
	return c, nil
}
func duration(text string) (time.Duration, error) {
	value, err := time.ParseDuration(text)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("expected a nonnegative duration, got %q", text)
	}
	return value, nil
}
func validateHook(h HookSpec) error {
	if h.Timeout != "" {
		value, err := duration(h.Timeout)
		if err != nil || value == 0 {
			return fmt.Errorf("timeout must be positive")
		}
	}
	switch h.Type {
	case "renice":
		if h.Nice == nil || *h.Nice < 0 || *h.Nice > 19 {
			return fmt.Errorf("renice needs nice 0..19")
		}
	case "signal":
		if !slices.Contains([]string{"SIGHUP", "SIGINT", "SIGTERM", "SIGKILL"}, h.Signal) {
			return fmt.Errorf("invalid signal %q", h.Signal)
		}
	case "session-interrupt":
		switch h.Via {
		case "cmux":
			if h.Key == "" {
				return fmt.Errorf("cmux requires key")
			}
		case "captain":
			u, err := url.Parse(h.Endpoint)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("captain requires an HTTP endpoint")
			}
		default:
			return fmt.Errorf("via must be captain or cmux")
		}
	case "exec":
		if len(h.Command) == 0 || strings.TrimSpace(h.Command[0]) == "" {
			return fmt.Errorf("exec requires command arguments")
		}
	default:
		return fmt.Errorf("unknown hook %q", h.Type)
	}
	if h.Type != "renice" && h.Nice != nil || h.Type != "signal" && h.Signal != "" || h.Type != "exec" && len(h.Command) > 0 || h.Type != "session-interrupt" && (h.Via != "" || h.Key != "" || h.Endpoint != "") {
		return fmt.Errorf("parameters do not belong to hook %s", h.Type)
	}
	if h.Via == "captain" && h.Key != "" || h.Via == "cmux" && h.Endpoint != "" {
		return fmt.Errorf("parameters do not belong to controller %s", h.Via)
	}
	return nil
}
