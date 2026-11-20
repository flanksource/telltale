package groups

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/flanksource/gavel/sdk/status"
)

type GroupBy string

const (
	GroupTree          GroupBy = "tree"
	GroupRepo          GroupBy = "repo"
	GroupWorktree      GroupBy = "worktree"
	GroupClaudeSession GroupBy = "claude-session"
	// GroupSlots buckets by the gavel slot pool a group's gavel process holds.
	GroupSlots GroupBy = "slots"
)

const (
	KindBucket = "bucket"
	// KindGavelTask and KindGavelItem are the rows WithGavelTasks adds: a
	// gavel task group and one of its running tasks.
	KindGavelTask = "gavel-task"
	KindGavelItem = "gavel-item"
)

var Groupings = []GroupBy{GroupTree, GroupRepo, GroupWorktree, GroupClaudeSession, GroupSlots}

func (by GroupBy) Validate() error {
	switch by {
	case "", GroupTree, GroupRepo, GroupWorktree, GroupClaudeSession, GroupSlots:
		return nil
	default:
		return fmt.Errorf("group-by %q: expected one of %v", by, Groupings)
	}
}

func (f Frame) Grouped(by GroupBy) (Frame, error) {
	if err := by.Validate(); err != nil {
		return Frame{}, err
	}
	if by == "" || by == GroupTree {
		return f, nil
	}
	contexts, err := ResolveContexts(f.Groups)
	if err != nil {
		return Frame{}, err
	}
	identities := map[string]bucket{}
	if by == GroupSlots {
		identities = slotBuckets(f.Groups)
	} else {
		for _, group := range f.Groups {
			identity, label := bucketIdentity(by, contexts[group.Key])
			identities[group.Key] = bucket{identity, label}
		}
	}
	buckets := map[string]string{}
	skipped := map[string]bool{}
	for _, group := range f.Groups {
		buckets[group.Key] = "bucket:" + string(by) + ":" + identities[group.Key].identity
		skipped[group.Key] = group.PID == 0 && (group.Kind == KindOther || group.Kind == KindApp)
	}
	var projected []GroupSample
	created := map[string]bool{}
	for _, group := range f.Groups {
		if skipped[group.Key] {
			continue
		}
		key := buckets[group.Key]
		if !created[key] {
			id := identities[group.Key]
			projected = append(projected, GroupSample{Key: key, Kind: KindBucket, Name: id.label, Target: id.identity, Command: id.identity, SampledAt: f.SampledAt, Tick: f.Tick})
			created[key] = true
		}
		if group.ParentKey == "" || buckets[group.ParentKey] != key || skipped[group.ParentKey] {
			group.ParentKey = key
		}
		group.Context, group.Total = contexts[group.Key], group.Self
		projected = append(projected, group)
	}
	f.Groups = Rollup(projected)
	return f, nil
}

// bucket is what a group is bucketed by, and the bucket's name.
type bucket struct{ identity, label string }

// slotBuckets puts each group in the slot pool its gavel process holds — a
// project pool before the global one — or, holding none, the pool it waits
// for. A group with no gavel status of its own takes its parent's bucket, so
// a gavel's tests share its pool.
func slotBuckets(groups []GroupSample) map[string]bucket {
	byKey := map[string]GroupSample{}
	for _, group := range groups {
		byKey[group.Key] = group
	}
	resolved := map[string]bucket{}
	// ResolveContexts has refused a cycle by now.
	var resolve func(key string) bucket
	resolve = func(key string) bucket {
		if found, ok := resolved[key]; ok {
			return found
		}
		group, ok := byKey[key]
		if !ok {
			return bucket{"", "Unattributed"}
		}
		found, own := slotBucket(group.Gavel)
		if !own {
			found = resolve(group.ParentKey)
		}
		resolved[key] = found
		return found
	}
	for _, group := range groups {
		resolve(group.Key)
	}
	return resolved
}

// slotBucket is the pool a gavel status's leases name, and whether it has any.
func slotBucket(s *status.Status) (bucket, bool) {
	if s == nil || len(s.Slots) == 0 {
		return bucket{}, false
	}
	best := s.Slots[0]
	rank := func(l status.Lease) int {
		score := 0
		if l.State != status.LeaseWaiting {
			score += 2
		}
		if l.Pool != "global" {
			score++
		}
		return score
	}
	for _, lease := range s.Slots[1:] {
		if rank(lease) > rank(best) {
			best = lease
		}
	}
	if best.State == status.LeaseWaiting {
		return bucket{best.Pool + ":waiting", poolLabel(best.Pool, best.Project) + " (waiting)"}, true
	}
	return bucket{best.Pool, poolLabel(best.Pool, best.Project)}, true
}

func bucketIdentity(by GroupBy, c Context) (string, string) {
	switch by {
	case GroupRepo:
		if c.GitCommonDir != "" {
			name := filepath.Base(c.GitCommonDir)
			if name == ".git" {
				name = filepath.Base(filepath.Dir(c.GitCommonDir))
			}
			return c.GitCommonDir, strings.TrimSuffix(name, ".git")
		}
	case GroupWorktree:
		if c.Repo != "" {
			return c.Repo, strings.TrimSuffix(filepath.Base(c.Repo)+"@"+c.Branch, "@")
		}
	case GroupClaudeSession:
		if c.Agent == "claude" && c.Session != "" {
			label := c.Session[:min(8, len(c.Session))]
			if c.SessionTitle != "" {
				label = c.SessionTitle + " [" + label + "]"
			}
			return c.Session, label
		}
	}
	return "", "Unattributed"
}

func ResolveContexts(groups []GroupSample) (map[string]Context, error) {
	byKey := map[string]GroupSample{}
	for _, group := range groups {
		if _, exists := byKey[group.Key]; exists || group.Key == "" {
			return nil, fmt.Errorf("grouping: empty or duplicate group key %q", group.Key)
		}
		if group.Kind == KindBucket {
			return nil, fmt.Errorf("grouping: expected raw groups, got bucket %q", group.Key)
		}
		byKey[group.Key] = group
	}
	resolved := map[string]Context{}
	visiting := map[string]bool{}
	var resolve func(string) (Context, error)
	resolve = func(key string) (Context, error) {
		if result, ok := resolved[key]; ok {
			return result, nil
		}
		group, ok := byKey[key]
		if !ok {
			return Context{}, nil
		}
		if visiting[key] {
			return Context{}, fmt.Errorf("grouping: cycle at %q", key)
		}
		visiting[key] = true
		parent, err := resolve(group.ParentKey)
		if err != nil {
			return Context{}, err
		}
		result := group.Context
		if result.Agent == "" && result.Session == "" {
			result.Agent, result.Session, result.SessionTitle = parent.Agent, parent.Session, parent.SessionTitle
		}
		resolved[key], visiting[key] = result, false
		return result, nil
	}
	for _, group := range groups {
		if _, err := resolve(group.Key); err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

// Rollup orders a complete group forest and recomputes totals from self usage.
func Rollup(groups []GroupSample) []GroupSample {
	children := map[string][]GroupSample{}
	present := map[string]bool{}
	for _, group := range groups {
		if group.Key == "" || present[group.Key] {
			panic(fmt.Sprintf("rollup: empty or duplicate key %q", group.Key))
		}
		present[group.Key] = true
		group.Total = group.Self
		children[group.ParentKey] = append(children[group.ParentKey], group)
	}
	var ordered []GroupSample
	var walk func(string)
	walk = func(parent string) {
		for _, group := range children[parent] {
			ordered = append(ordered, group)
			walk(group.Key)
		}
	}
	walk("")
	if len(ordered) != len(groups) {
		panic("rollup: expected a complete, acyclic group forest")
	}
	positions := map[string]int{}
	for index, group := range ordered {
		positions[group.Key] = index
	}
	for index := len(ordered) - 1; index >= 0; index-- {
		group := ordered[index]
		if parent, ok := positions[group.ParentKey]; ok {
			ordered[parent].Total.merge(group.Total)
		}
	}
	return ordered
}
