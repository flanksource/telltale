package groups

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sessionprocess "github.com/flanksource/captain/pkg/session/process"
	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

// Context is where a group runs and on whose behalf: its anchor's working
// directory and checkout, the agent session and cmux surface it runs in.
type Context struct {
	CWD    string `json:"cwd,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
	// Agent and Session are the claude or codex session the anchor runs
	// under, from its environment, or that it is, from its command line;
	// SessionTitle is what the session is called.
	Agent        string `json:"agent,omitempty"`
	Session      string `json:"session,omitempty"`
	SessionTitle string `json:"sessionTitle,omitempty"`
	// Workspace and Surface are the cmux workspace and surface the anchor
	// runs in, and their titles what cmux shows them as.
	Workspace      string `json:"workspace,omitempty"`
	WorkspaceTitle string `json:"workspaceTitle,omitempty"`
	Surface        string `json:"surface,omitempty"`
	SurfaceTitle   string `json:"surfaceTitle,omitempty"`
	// Error is why some of it could not be read, such as another user's
	// process.
	Error string `json:"error,omitempty"`
}

// contextEnvironment is what is read of an anchor's environment.
var contextEnvironment = clickyprocess.EnvironmentOptions{
	Keys:     []string{"CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID"},
	Prefixes: []string{"CMUX_"},
}

// contexts reads each group's context once, as its anchor is first seen, and
// the names behind its ids as they change.
type contexts struct {
	byKey map[string]Context
	repos map[string]Repo
	names *names
}

func newContexts() *contexts {
	return &contexts{byKey: map[string]Context{}, repos: map[string]Repo{}, names: newNames()}
}

// of is the context of each ruled group, by key, reading those not yet read
// and forgetting those of groups gone. held are the descriptors its
// processes hold, by pid, in which a codex group's session is its open
// rollout: codex carries its thread id in neither its environment nor its
// command line.
func (c *contexts) of(ctx context.Context, classified []Group, held map[int][]clickyprocess.Descriptor) map[string]Context {
	read := c.read(ctx, classified)
	named := make(map[string]Context, len(read))
	for _, group := range classified {
		context, ok := read[group.Key]
		if !ok {
			continue
		}
		if context.Session == "" {
			thread, err := openRollout(group.Members, held)
			if err != nil {
				context.Error = strings.TrimPrefix(context.Error+"; "+err.Error(), "; ")
			}
			if thread != "" {
				context.Agent, context.Session = "codex", thread
			}
		}
		named[group.Key] = c.named(context)
	}
	return named
}

// rollout is the thread id a codex rollout file is named with.
var rollout = regexp.MustCompile(`/rollout-[^/]*-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

// openRollout is the thread of the codex rollout a codex process among
// members holds open, or "". A codex running sub-agents holds theirs open
// too: as captain's session ps does, its session is the one written last.
func openRollout(members []process.Observed, held map[int][]clickyprocess.Descriptor) (string, error) {
	var open []string
	for _, member := range members {
		if sessionprocess.Source(member.Command) != "codex" {
			continue
		}
		for _, descriptor := range held[member.PID] {
			if rollout.MatchString(descriptor.Name) {
				open = append(open, descriptor.Name)
			}
		}
	}
	if len(open) == 0 {
		return "", nil
	}
	latest, _, err := latestWritten(open)
	if err != nil {
		return "", fmt.Errorf("codex rollout: %w", err)
	}
	return rollout.FindStringSubmatch(latest)[1], nil
}

// named is read with the names behind its ids, and why any could not be
// read.
func (c *contexts) named(read Context) Context {
	err := c.names.fill(&read)
	if err == nil {
		return read
	}
	if read.Error != "" {
		read.Error += "; "
	}
	read.Error += err.Error()
	return read
}

// read is the context of each ruled group as its anchor was first seen.
func (c *contexts) read(ctx context.Context, classified []Group) map[string]Context {
	alive := map[string]bool{}
	var fresh []Group
	for _, group := range classified {
		if group.Rule.Kind == KindOther || group.Anchor.PID == 0 {
			continue
		}
		alive[group.Key] = true
		if _, ok := c.byKey[group.Key]; !ok {
			fresh = append(fresh, group)
		}
	}
	for key := range c.byKey {
		if !alive[key] {
			delete(c.byKey, key)
		}
	}
	if len(fresh) == 0 {
		return c.byKey
	}
	anchors := make([]clickyprocess.Process, 0, len(fresh))
	for _, group := range fresh {
		anchors = append(anchors, group.Anchor.Process)
	}
	for index, read := range c.anchors(ctx, anchors) {
		c.byKey[fresh[index].Key] = read
	}
	return c.byKey
}

// anchors are the contexts of processes, in order: their working directory
// and checkout, and the agent session and cmux surface they run under, or,
// for an agent, the session its command line names.
func (c *contexts) anchors(ctx context.Context, processes []clickyprocess.Process) []Context {
	pids := make([]int, 0, len(processes))
	for _, p := range processes {
		pids = append(pids, p.PID)
	}
	snapshot := clickyprocess.NewSnapshot(processes)
	snapshot.PopulateWorkingDirectories(ctx, pids)
	results := make([]Context, 0, len(processes))
	for _, p := range processes {
		anchor, _ := snapshot.Get(p.PID)
		result := Context{CWD: anchor.CWD}
		var errs []error
		if anchor.CWDError != "" {
			errs = append(errs, errors.New("cwd: "+anchor.CWDError))
		}
		if anchor.CWD != "" {
			repo, ok := c.repos[anchor.CWD]
			if !ok {
				repo = RepoOf(anchor.CWD)
				c.repos[anchor.CWD] = repo
			}
			result.Repo, result.Branch = repo.Root, repo.Branch
		}
		environment, err := clickyprocess.Environment(ctx, p.PID, contextEnvironment)
		if err != nil {
			errs = append(errs, errors.New("environment: "+err.Error()))
		}
		result.Agent, result.Session = sessionprocess.IdentityFromEnvironment(environment)
		if source := sessionprocess.Source(p.Command); result.Session == "" && source != "" {
			if id := sessionprocess.SessionIDFromCommand(p.Command); id != "" {
				result.Agent, result.Session = source, id
			}
		}
		if surface := sessionprocess.SurfaceFromEnvironment(environment); surface != nil {
			result.Workspace, result.Surface = surface.WorkspaceID, surface.SurfaceID
		}
		if err := errors.Join(errs...); err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	return results
}

// Repo is the git checkout a directory is in, and what it has checked out.
type Repo struct {
	Root   string `json:"root,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// RepoOf is the checkout holding dir, from the nearest .git above it: its
// branch, or the short commit of a detached head. A directory in no
// checkout, or one whose HEAD cannot be read, has none.
func RepoOf(dir string) Repo {
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		gitPath := filepath.Join(current, ".git")
		if info, err := os.Stat(gitPath); err == nil {
			return Repo{Root: current, Branch: headOf(gitPath, info.IsDir())}
		}
		if parent := filepath.Dir(current); parent == current {
			return Repo{}
		}
	}
}

func headOf(gitPath string, isDir bool) string {
	gitDir := gitPath
	if !isDir {
		pointer, err := os.ReadFile(gitPath)
		if err != nil {
			return ""
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(pointer)), "gitdir: ")
		if !ok {
			return ""
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(gitPath), target)
		}
		gitDir = target
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(head))
	if branch, ok := strings.CutPrefix(text, "ref: refs/heads/"); ok {
		return branch
	}
	if len(text) >= 7 {
		return text[:7]
	}
	return text
}
