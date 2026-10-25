package groups

import (
	"cmp"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	sessionprocess "github.com/flanksource/captain/pkg/session/process"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

// otherPrefix keys the KindOther groups: other:<scope> holds a scope's
// unmatched processes, other:<scope>:<program> those of one program.
const otherPrefix = "other:"

// appsKey keys the KindApp group the app groups no tool holds sit under;
// appPrefix keys an app's group, app:<bundle path>.
const (
	appsKey   = "apps"
	appPrefix = "app:"
)

// appBundle is the outermost .app bundle of a program in an Applications
// folder — the system's, the host's or a user's, or a folder in one — and
// its name.
var appBundle = regexp.MustCompile(`^((?:/System|/Users/[^/]+)?/Applications/(?:[^/]+/)*?([^/]+)\.app)/`)

// appOf is the bundle path and name of the app command runs, or "".
func appOf(command string) (string, string) {
	match := appBundle.FindStringSubmatch(command)
	if match == nil {
		return "", ""
	}
	return match[1], match[2]
}

// Group is a tool's processes at one listing: the process a rule matched, its
// Anchor, and every process under it that no other group took. A KindOther
// group holds the processes of one program, of one scope, that are in no
// tool's group, under its scope's KindOther group, which holds none itself.
type Group struct {
	// Key is the anchor's process key, or other:<scope>[:<program>].
	Key string
	// ParentKey is the group of the anchor's nearest ancestor in one.
	ParentKey string
	Rule      Rule
	Anchor    process.Observed
	// Origin is who runs the anchor and what started it.
	Origin Origin
	// Target is what the group runs against, read off its anchor as its rule
	// says.
	Target  string
	Members []process.Observed
}

// ClassifyOptions are what Classify matches a listing against.
type ClassifyOptions struct {
	Catalog Catalog
	// Origins are each process's, by key; one without counts as the user's.
	Origins map[string]Origin
	// Self is the pid of the telltale reading the listing, whose processes
	// form the SelfGroup tool group, or 0.
	Self int
}

// SelfGroup names the group of the telltale reading the host, and what it runs.
const SelfGroup = "telltale"

// Classify sorts listing into groups: a process a rule matches, a brew
// service no rule matches, or the Self process, starts a group under the
// group of its nearest ancestor in one, unless that group is of the same
// rule, which it joins, as a test binary joins its go test; any other
// process joins its nearest ancestor's group, or its scope's program's
// KindOther group. Groups come as a tree, parents first and siblings by
// anchor pid, then the KindOther groups by key.
func Classify(listing []process.Observed, options ClassifyOptions) []Group {
	c := classifier{catalog: options.Catalog, origins: options.Origins, self: options.Self, byPID: map[int]process.Observed{}, owners: map[int]string{}, groups: map[string]*Group{}}
	pids := make([]int, 0, len(listing))
	for _, p := range listing {
		c.byPID[p.PID] = p
		pids = append(pids, p.PID)
	}
	slices.Sort(pids)
	for _, pid := range pids {
		p := c.byPID[pid]
		key := c.owner(pid)
		if key == "" {
			key = c.other(p)
		}
		c.groups[key].Members = append(c.groups[key].Members, p)
	}
	return c.tree()
}

type classifier struct {
	catalog Catalog
	origins map[string]Origin
	self    int
	byPID   map[int]process.Observed
	owners  map[int]string
	groups  map[string]*Group
}

// other is the key of the KindOther group of p's scope and program, made
// with its scope's group the first time.
func (c *classifier) other(p process.Observed) string {
	origin := c.origins[keyOf(p)]
	scope := origin.Scope
	if scope == "" {
		scope = ScopeUser
	}
	parent := otherPrefix + scope
	if c.groups[parent] == nil {
		name := "other"
		if scope == ScopeSystem {
			name = ScopeSystem
		}
		c.groups[parent] = &Group{Key: parent, Rule: Rule{Name: name, Kind: KindOther}, Origin: Origin{Scope: scope}}
	}
	name := program(strings.Fields(p.Command))
	key := parent + ":" + name
	if c.groups[key] == nil {
		c.groups[key] = &Group{Key: key, ParentKey: parent, Rule: Rule{Name: name, Kind: KindOther}, Anchor: p, Origin: origin}
	}
	return key
}

// owner is the key of the ruled group pid belongs to, or "" when none.
func (c *classifier) owner(pid int) string {
	if key, ok := c.owners[pid]; ok {
		return key
	}
	p, ok := c.byPID[pid]
	if !ok {
		return ""
	}
	c.owners[pid] = "" // a ppid cycle ends here
	parent := ""
	if p.PPID != p.PID {
		parent = c.owner(p.PPID)
	}
	key := parent
	origin := c.origins[keyOf(p)]
	rule, match, ok := c.catalog.match(p.Command)
	switch {
	case c.self != 0 && pid == c.self:
		rule, match, ok = Rule{Name: SelfGroup, Kind: KindTool}, Match{}, true
	case !ok && origin.Source == SourceBrewService:
		rule, match, ok = Rule{Name: origin.Service, Kind: KindService}, Match{}, true
	}
	if bundle, name := appOf(p.Command); !ok && bundle != "" && (parent == "" || c.groups[parent].Rule.Kind == KindApp) {
		key = c.app(bundle, name, parent, p, origin)
		c.owners[pid] = key
		return key
	}
	if ok && (parent == "" || c.groups[parent].Rule.Name != rule.Name) {
		key = process.Key(p.Process)
		c.groups[key] = &Group{Key: key, ParentKey: parent, Rule: rule, Anchor: p, Origin: origin, Target: target(rule, match, p.Command)}
	}
	c.owners[pid] = key
	return key
}

// app is the key of bundle's group, which every process of the app joins
// wherever it runs, made the first time under parent's group, or under
// applications.
func (c *classifier) app(bundle, name, parent string, p process.Observed, origin Origin) string {
	key := appPrefix + bundle
	if c.groups[key] != nil {
		return key
	}
	if parent == "" {
		if c.groups[appsKey] == nil {
			c.groups[appsKey] = &Group{Key: appsKey, Rule: Rule{Name: "applications", Kind: KindApp}}
		}
		parent = appsKey
	}
	c.groups[key] = &Group{Key: key, ParentKey: parent, Rule: Rule{Name: name, Kind: KindApp}, Anchor: p, Origin: origin}
	return key
}

func (c *classifier) tree() []Group {
	children := map[string][]*Group{}
	for _, group := range c.groups {
		children[group.ParentKey] = append(children[group.ParentKey], group)
	}
	result := make([]Group, 0, len(c.groups))
	var walk func(parent string)
	walk = func(parent string) {
		siblings := children[parent]
		// Tools first, then applications, then other and system.
		rank := func(group *Group) int {
			switch {
			case group.Rule.Kind == KindOther:
				return 2
			case group.Key == appsKey:
				return 1
			}
			return 0
		}
		slices.SortFunc(siblings, func(a, b *Group) int {
			if order := cmp.Compare(rank(a), rank(b)); order != 0 {
				return order
			}
			if rank(a) == 2 {
				return cmp.Compare(a.Key, b.Key)
			}
			return cmp.Compare(a.Anchor.PID, b.Anchor.PID)
		})
		for _, group := range siblings {
			result = append(result, *group)
			walk(group.Key)
		}
	}
	walk("")
	return result
}

// match is the first rule, and its match, that command meets.
func (c Catalog) match(command string) (Rule, Match, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return Rule{}, Match{}, false
	}
	for _, rule := range c.Rules {
		for _, match := range rule.Match {
			if match.matches(command, fields) {
				return rule, match, true
			}
		}
	}
	return Rule{}, Match{}, false
}

func (m Match) matches(command string, fields []string) bool {
	if slices.ContainsFunc(m.Exclude, func(text string) bool { return strings.Contains(command, text) }) {
		return false
	}
	if len(m.Exec) > 0 {
		exec := program(fields)
		if !slices.ContainsFunc(m.Exec, func(glob string) bool { matched, _ := path.Match(glob, exec); return matched }) {
			return false
		}
	}
	if len(m.Contains) > 0 && !slices.ContainsFunc(m.Contains, func(text string) bool { return strings.Contains(command, text) }) {
		return false
	}
	if m.Source != "" && sessionprocess.Source(command) != m.Source {
		return false
	}
	_, ok := argsAfter(fields[1:], m.Args)
	return ok
}

// argsAfter is args without the first occurrence of each of want, in order,
// and whether they all occur.
func argsAfter(args, want []string) ([]string, bool) {
	rest := slices.Clone(args)
	from := 0
	for _, arg := range want {
		index := slices.Index(rest[from:], arg)
		if index < 0 {
			return nil, false
		}
		rest = slices.Delete(rest, from+index, from+index+1)
		from += index
	}
	return rest, true
}

// target reads what a group runs against off its anchor's command.
func target(rule Rule, match Match, command string) string {
	fields := strings.Fields(command)
	switch rule.Target {
	case "":
		return ""
	case TargetSession:
		return sessionprocess.SessionIDFromCommand(command)
	case TargetPositional:
		rest, _ := argsAfter(fields[1:], match.Args)
		return strings.Join(slices.DeleteFunc(rest, func(arg string) bool { return strings.HasPrefix(arg, "-") }), " ")
	}
	for index, field := range fields {
		if field == rule.Target && index+1 < len(fields) {
			return fields[index+1]
		}
		if value, ok := strings.CutPrefix(field, rule.Target+"="); ok {
			return value
		}
	}
	return ""
}

// program is the base name of the program a command runs.
func program(fields []string) string {
	if len(fields) == 0 {
		return "unknown"
	}
	return strings.Trim(filepath.Base(fields[0]), `"'():`)
}
