package groups

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	clickyprocess "github.com/flanksource/clicky/process"
)

// The scopes a process runs in, by its owner.
const (
	ScopeUser = "user"
	// ScopeSystem are root's and the role accounts' processes, such as
	// darwin's _name daemons.
	ScopeSystem = "system"
)

// The sources that started a process.
const (
	// SourceBrewService is a `brew services` job (homebrew.mxcl.<name>).
	SourceBrewService = "brew-service"
	// SourceBrew is a program brew installed that something else ran.
	SourceBrew = "brew"
	// SourceApp is an application launchd opened (application.<bundle id>).
	SourceApp = "app"
	// SourceApple is a macOS built-in: an Apple launchd job, or a daemon
	// launchd started from the system's own directories.
	SourceApple = "apple"
	// SourceLaunchd is any other launchd agent or daemon.
	SourceLaunchd = "launchd"
)

// The launchd domains a job runs in.
const (
	// DomainSystem jobs are daemons: the host's, started at boot.
	DomainSystem = "system"
	// DomainUser jobs are the logged-in user's agents (gui/<uid>).
	DomainUser = "gui"
)

// Origin is who runs a process and what started it.
type Origin struct {
	Owner  string `json:"owner,omitempty"`
	Scope  string `json:"scope,omitempty"`
	Source string `json:"source,omitempty"`
	// Domain, Label and Service name the launchd job that started it.
	Domain  string `json:"domain,omitempty"`
	Label   string `json:"label,omitempty"`
	Service string `json:"service,omitempty"`
}

// Job is a running launchd job.
type Job struct {
	Label, Domain string
}

// Jobs are the running launchd jobs, by pid.
type Jobs map[int]Job

// ParseLaunchctlPrint reads the running services of `launchctl print
// <domain>`: the rows of its services block whose pid is not 0.
func ParseLaunchctlPrint(r io.Reader, domain string) (Jobs, error) {
	jobs := Jobs{}
	scanner := bufio.NewScanner(r)
	inServices := false
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.TrimSpace(line) == "services = {" && strings.Count(line, "\t") == 1:
			inServices = true
			continue
		case !inServices:
			continue
		case strings.TrimSpace(line) == "}":
			return jobs, scanner.Err()
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("launchctl %s service row %q is not <pid> <status> <label>", domain, line)
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("launchctl %s service row %q: %w", domain, line, err)
		}
		if pid > 0 {
			jobs[pid] = Job{Label: fields[2], Domain: domain}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read launchctl %s: %w", domain, err)
	}
	if inServices {
		return nil, fmt.Errorf("launchctl %s services block does not end", domain)
	}
	return jobs, nil
}

// appleDirs hold the programs macOS ships.
var appleDirs = []string{"/System/", "/usr/libexec/", "/usr/sbin/", "/sbin/"}

// OriginOf is who runs p and what started it: its launchd job when jobs
// has one, else a daemon launchd started from the system's directories, or a
// program under one of brew's prefixes.
func OriginOf(p clickyprocess.Process, jobs Jobs, brewPrefixes []string) Origin {
	origin := Origin{Owner: p.User, Scope: scopeOf(p.UID)}
	command := strings.TrimSpace(p.Command)
	if job, ok := jobs[p.PID]; ok {
		origin.Domain, origin.Label, origin.Service = job.Domain, job.Label, job.Label
		switch {
		case strings.HasPrefix(job.Label, "homebrew.mxcl."):
			origin.Source, origin.Service = SourceBrewService, strings.TrimPrefix(job.Label, "homebrew.mxcl.")
		case strings.HasPrefix(job.Label, "application."):
			origin.Source, origin.Service = SourceApp, bundleOf(job.Label)
		case strings.HasPrefix(job.Label, "com.apple."):
			origin.Source = SourceApple
		default:
			origin.Source = SourceLaunchd
		}
		return origin
	}
	for _, prefix := range brewPrefixes {
		if strings.HasPrefix(command, prefix+"/") {
			origin.Source = SourceBrew
			return origin
		}
	}
	if p.PID == 1 || p.PPID == 1 {
		for _, dir := range appleDirs {
			if strings.HasPrefix(command, dir) {
				origin.Source = SourceApple
				return origin
			}
		}
	}
	return origin
}

// bundleOf is the bundle id of an application.<bundle id>.<n>.<n> label.
func bundleOf(label string) string {
	parts := strings.Split(strings.TrimPrefix(label, "application."), ".")
	for len(parts) > 1 {
		if _, err := strconv.ParseUint(parts[len(parts)-1], 10, 64); err != nil {
			break
		}
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, ".")
}

// firstUserUID is the lowest uid a login user gets: below it are root and
// the role accounts daemons run as.
var firstUserUID = map[bool]int{true: 500, false: 1000}[runtime.GOOS == "darwin"]

func scopeOf(uid int) string {
	if uid < firstUserUID {
		return ScopeSystem
	}
	return ScopeUser
}

// BrewPrefixes are the directories brew's programs run from: its prefix,
// $HOMEBREW_PREFIX or whichever default holds a Cellar, but under
// /usr/local, which holds programs brew did not install, only its Cellar
// and opt links.
func BrewPrefixes() []string {
	if prefix := os.Getenv("HOMEBREW_PREFIX"); prefix != "" {
		return []string{prefix}
	}
	var prefixes []string
	for _, prefix := range []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"} {
		info, err := os.Stat(prefix + "/Cellar")
		switch {
		case err != nil || !info.IsDir():
		case prefix == "/usr/local":
			prefixes = append(prefixes, prefix+"/Cellar", prefix+"/opt")
		default:
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}
