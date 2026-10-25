package groups

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/captain/pkg/ai/history"
	"github.com/flanksource/captain/pkg/claude"
	"github.com/flanksource/captain/pkg/cmux"
	"github.com/flanksource/captain/pkg/session"
)

// namesEvery is how often a session's title, and cmux's titles, are read
// again: an agent names its session as it goes, and cmux's titles follow
// what its surfaces run.
const namesEvery = time.Minute

// names resolves the ids of a context to what people call them: an agent
// session's title, from its transcript, and a cmux surface's and its
// workspace's titles, from cmux. A transcript runs to megabytes, so titles
// are read off the tick, one transcript at a time, and a tick shows the
// title last read.
type names struct {
	mu       sync.Mutex
	sessions map[string]sessionTitle
	wanted   map[string]sessionRef
	reading  bool
	// codex and codexAt are the rollouts last listed, by the reader alone.
	codex      []string
	codexAt    time.Time
	surfaces   map[string]cmux.Surface
	surfacesAt time.Time
	surfaceErr error
}

type sessionRef struct{ agent, id string }

// sessionTitle is a session's title as last read, why it could not be, and
// when its transcript was last written then.
type sessionTitle struct {
	title     string
	err       error
	readAt    time.Time
	writtenAt time.Time
}

func newNames() *names {
	return &names{sessions: map[string]sessionTitle{}, wanted: map[string]sessionRef{}}
}

// fill sets context's session, surface and workspace titles from its ids.
func (n *names) fill(context *Context) error {
	var errs []error
	if context.Session != "" {
		title, err := n.session(context.Agent, context.Session)
		context.SessionTitle = title
		errs = append(errs, err)
	}
	if context.Surface != "" {
		surface, err := n.surface(context.Surface)
		context.SurfaceTitle, context.WorkspaceTitle = surface.Title, surface.Workspace
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// session is the title captain gives an agent's session, as last read: the
// one the agent named it, its slug, or its opening prompt's; "" until it is
// first read, or while it has no transcript. One not read in namesEvery is
// read again, off the tick.
func (n *names) session(agent, id string) (string, error) {
	key := agent + ":" + id
	n.mu.Lock()
	defer n.mu.Unlock()
	cached, ok := n.sessions[key]
	if !ok || time.Since(cached.readAt) >= namesEvery {
		n.wanted[key] = sessionRef{agent: agent, id: id}
		if !n.reading {
			n.reading = true
			go n.readWanted()
		}
	}
	return cached.title, cached.err
}

// readWanted reads the titles of the sessions wanted, one at a time, until
// none is.
func (n *names) readWanted() {
	for {
		n.mu.Lock()
		var key string
		var ref sessionRef
		for key, ref = range n.wanted {
			break
		}
		if key == "" {
			n.reading = false
			n.mu.Unlock()
			return
		}
		delete(n.wanted, key)
		previous := n.sessions[key]
		n.mu.Unlock()
		read := n.readSession(ref, previous)
		n.mu.Lock()
		n.sessions[key] = read
		n.mu.Unlock()
	}
}

// readSession reads a session's title again, unless its transcript was not
// written since previous was read.
func (n *names) readSession(ref sessionRef, previous sessionTitle) sessionTitle {
	read := sessionTitle{readAt: time.Now()}
	path, writtenAt, err := n.transcript(ref.agent, ref.id)
	if err != nil || path == "" {
		read.err = err
		return read
	}
	read.writtenAt = writtenAt
	if previous.err == nil && writtenAt.Equal(previous.writtenAt) {
		read.title = previous.title
		return read
	}
	var built *session.Session
	switch ref.agent {
	case "claude":
		built, err = session.BuildClaudeFile(path)
	case "codex":
		built, err = session.BuildCodexFile(path)
		if errors.Is(err, session.ErrCodexEmpty) {
			built, err = &session.Session{}, nil
		}
	default:
		err = fmt.Errorf("agent %q is not claude or codex", ref.agent)
	}
	if err != nil {
		read.err = fmt.Errorf("%s session %s: %w", ref.agent, ref.id, err)
		return read
	}
	read.title = built.Title
	return read
}

// transcript is the file an agent writes a session to, the latest written
// when it is in more than one, and when it was written; "" when there is
// none yet.
func (n *names) transcript(agent, id string) (string, time.Time, error) {
	var candidates []string
	switch agent {
	case "claude":
		matches, err := filepath.Glob(filepath.Join(claude.GetProjectsDir(), "*", id+".jsonl"))
		if err != nil {
			return "", time.Time{}, fmt.Errorf("find claude session %s: %w", id, err)
		}
		candidates = matches
	case "codex":
		if n.codex == nil || time.Since(n.codexAt) >= namesEvery {
			files, err := history.FindCodexSessionFiles()
			if err != nil {
				return "", time.Time{}, fmt.Errorf("find codex session %s: %w", id, err)
			}
			n.codex, n.codexAt = files, time.Now()
		}
		for _, file := range n.codex {
			if strings.HasSuffix(file, "-"+id+".jsonl") {
				candidates = append(candidates, file)
			}
		}
	}
	return latestWritten(candidates)
}

func latestWritten(paths []string) (string, time.Time, error) {
	latest, latestAt := "", time.Time{}
	for _, path := range slices.Sorted(slices.Values(paths)) {
		info, err := os.Stat(path)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("session transcript: %w", err)
		}
		if latest == "" || info.ModTime().After(latestAt) {
			latest, latestAt = path, info.ModTime()
		}
	}
	return latest, latestAt, nil
}

// surface is cmux's surface of this id, with its workspace's title; a closed
// one has none.
func (n *names) surface(id string) (cmux.Surface, error) {
	if n.surfacesAt.IsZero() || time.Since(n.surfacesAt) >= namesEvery {
		n.surfaces, n.surfaceErr = cmux.Surfaces()
		n.surfacesAt = time.Now()
		if n.surfaceErr != nil {
			n.surfaceErr = fmt.Errorf("cmux surfaces: %w", n.surfaceErr)
		}
	}
	return n.surfaces[id], n.surfaceErr
}
