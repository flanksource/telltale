// Package groups sorts the host's processes into the tools that own them —
// agents such as claude and codex, test runners, browsers, builds — as a tree
// of groups, each with its context and its CPU, memory, disk and network use.
package groups

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// The kinds of tool a group is.
const (
	KindAgent   = "agent"
	KindBrowser = "browser"
	KindTest    = "test"
	KindBuild   = "build"
	KindTool    = "tool"
	KindService = "service"
	// KindApp is an app bundle in an Applications folder.
	KindApp = "app"
	// KindOther are the processes no rule matched, by program.
	KindOther = "other"
)

var kinds = []string{KindAgent, KindBrowser, KindTest, KindBuild, KindTool, KindService, KindApp}

// The targets a rule reads off its group's first process.
const (
	// TargetPositional is the arguments that are no flag, after the program
	// and the args its match named.
	TargetPositional = "positional"
	// TargetSession is an agent's session id, from --session-id or --resume.
	TargetSession = "session"
)

// The agents captain recognises a process as (Match.Source).
var sources = []string{"claude", "codex"}

// Rule is one tool: the processes it matches, the first of which in a tree
// starts a group of its kind.
type Rule struct {
	Name string `yaml:"name" json:"name"`
	Kind string `yaml:"kind,omitempty" json:"kind"`
	// Target names what the group runs against: TargetPositional,
	// TargetSession, or a flag such as --session whose value it is.
	Target string  `yaml:"target,omitempty" json:"target,omitempty"`
	Match  []Match `yaml:"match,omitempty" json:"match,omitempty"`
	// Disabled drops the built-in rule of this name.
	Disabled bool `yaml:"disabled,omitempty" json:"-"`
}

// Match is one way a process is a rule's: it meets every field given.
type Match struct {
	// Exec are globs over the base name of the program, any of which matches.
	Exec []string `yaml:"exec,omitempty" json:"exec,omitempty"`
	// Args must all appear among the arguments, in order.
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`
	// Contains are texts any of which the command holds.
	Contains []string `yaml:"contains,omitempty" json:"contains,omitempty"`
	// Source is the agent captain recognises the command as: claude or codex.
	Source string `yaml:"source,omitempty" json:"source,omitempty"`
	// Exclude are texts none of which the command may hold.
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}

// Catalog is the rules a process is matched against, in order.
type Catalog struct {
	Rules []Rule
}

//go:embed catalog.yaml
var builtinCatalog []byte

type catalogFile struct {
	Groups []Rule `yaml:"groups"`
}

// LoadCatalog is the built-in rules with each file's applied over them: a
// new rule goes ahead of the built-ins, one named like a built-in replaces it
// in place, and a disabled one drops it.
func LoadCatalog(paths ...string) (Catalog, error) {
	rules, err := parseRules(builtinCatalog, "built-in catalog")
	if err != nil {
		return Catalog{}, err
	}
	for _, file := range paths {
		data, err := os.ReadFile(file)
		if err != nil {
			return Catalog{}, fmt.Errorf("read groups catalog: %w", err)
		}
		overrides, err := parseRules(data, file)
		if err != nil {
			return Catalog{}, err
		}
		if rules, err = apply(rules, overrides, file); err != nil {
			return Catalog{}, err
		}
	}
	return Catalog{Rules: rules}, nil
}

func apply(rules, overrides []Rule, file string) ([]Rule, error) {
	var added []Rule
	for _, override := range overrides {
		index := slices.IndexFunc(rules, func(rule Rule) bool { return rule.Name == override.Name })
		switch {
		case override.Disabled && index < 0:
			return nil, fmt.Errorf("groups catalog %s disables %q, which is no built-in rule", file, override.Name)
		case override.Disabled:
			rules = slices.Delete(rules, index, index+1)
		case index >= 0:
			rules[index] = override
		default:
			added = append(added, override)
		}
	}
	return append(added, rules...), nil
}

func parseRules(data []byte, source string) ([]Rule, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var file catalogFile
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("groups catalog %s: %w", source, err)
	}
	var errs []error
	for index, rule := range file.Groups {
		if slices.ContainsFunc(file.Groups[:index], func(other Rule) bool { return other.Name == rule.Name }) {
			errs = append(errs, fmt.Errorf("rule %q twice", rule.Name))
			continue
		}
		errs = append(errs, rule.validate())
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("groups catalog %s: %w", source, err)
	}
	return file.Groups, nil
}

func (r Rule) validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("a rule needs a name")
	}
	if r.Disabled {
		return nil
	}
	var errs []error
	if !slices.Contains(kinds, r.Kind) {
		errs = append(errs, fmt.Errorf("kind %q: expected one of %v", r.Kind, kinds))
	}
	if r.Target != "" && r.Target != TargetPositional && r.Target != TargetSession && !strings.HasPrefix(r.Target, "-") {
		errs = append(errs, fmt.Errorf("target %q: expected %q, %q or a flag such as --session", r.Target, TargetPositional, TargetSession))
	}
	if len(r.Match) == 0 {
		errs = append(errs, errors.New("no match"))
	}
	for _, match := range r.Match {
		errs = append(errs, match.validate())
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("rule %q: %w", r.Name, err)
	}
	return nil
}

func (m Match) validate() error {
	if len(m.Exec) == 0 && len(m.Contains) == 0 && m.Source == "" {
		return errors.New("no match: give exec, contains or source")
	}
	if m.Source != "" && !slices.Contains(sources, m.Source) {
		return fmt.Errorf("source %q: expected one of %v", m.Source, sources)
	}
	for _, glob := range m.Exec {
		if _, err := path.Match(glob, ""); err != nil {
			return fmt.Errorf("exec %q: %w", glob, err)
		}
	}
	return nil
}

// Rule is the catalog's rule of this name.
func (c Catalog) Rule(name string) (Rule, bool) {
	index := slices.IndexFunc(c.Rules, func(rule Rule) bool { return rule.Name == name })
	if index < 0 {
		return Rule{}, false
	}
	return c.Rules[index], true
}
