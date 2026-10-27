package rules

import (
	"fmt"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"math"
	"reflect"
	"slices"
	"strings"
)

func Resolve(snapshot groups.Snapshot, by string) ([]MonitorGroup, Host, error) {
	raw := snapshot.Frame
	if raw.Host == nil || raw.Host.Cores <= 0 || raw.Host.MemoryTotalBytes == 0 || raw.Host.MemoryTotalBytes > math.MaxInt64 {
		return nil, Host{}, fmt.Errorf("valid host CPU and memory capacity are required")
	}
	host := Host{Cores: float64(raw.Host.Cores), MemoryTotalBytes: int64(raw.Host.MemoryTotalBytes)}
	contexts, err := groups.ResolveContexts(raw.Groups)
	if err != nil {
		return nil, host, err
	}
	samples := map[string]groups.GroupSample{}
	for _, g := range raw.Groups {
		samples[g.Key] = g
	}
	buckets := map[string]*MonitorGroup{}
	applications, err := groups.Applications(snapshot.Processes)
	if err != nil {
		return nil, host, err
	}
	anchors := sessionAnchors(snapshot, contexts)
	for _, p := range snapshot.Processes {
		source, ok := samples[p.GroupKey]
		if !ok {
			return nil, host, fmt.Errorf("process %s refers to missing group %s", p.Key, p.GroupKey)
		}
		context := contexts[source.Key]
		resources, err := resourceUsage(p.Usage, host)
		if err != nil {
			return nil, host, err
		}
		lineage := []string{}
		for current := source; current.Key != ""; current = samples[current.ParentKey] {
			lineage = append(lineage, current.Key)
		}
		member := ProcessRef{Key: p.Key, PID: p.PID, PPID: p.PPID, Name: p.Name, Command: p.Command, Context: context, Resources: resources, GroupName: source.Name, GroupRoot: p.PID == source.PID, GroupPath: lineage}
		keys := []string{}
		name, kind := source.Name, source.Kind
		switch by {
		case "host":
			keys = append(keys, "host")
			name, kind = "Host", "host"
		case "tree":
			keys = append(keys, lineage...)
		case "repo":
			if context.GitCommonDir != "" {
				keys = append(keys, "repo:"+context.GitCommonDir)
				name = context.GitCommonDir
			}
		case "worktree":
			if context.Repo != "" {
				keys = append(keys, "worktree:"+context.Repo)
				name = context.Repo
			}
		case "session":
			if context.Agent != "" && context.Session != "" {
				keys = append(keys, "session:"+context.Agent+":"+context.Session)
				name = context.SessionTitle
				kind = "session"
			}
		case "application":
			if app := applications[p.Key]; app != "" {
				keys = append(keys, "application:"+app)
				name = app
				kind = "application"
			}
		default:
			return nil, host, fmt.Errorf("invalid groupBy %q", by)
		}
		for _, key := range keys {
			bucket := buckets[key]
			if bucket == nil {
				bucket = &MonitorGroup{Key: key, Name: name, Kind: kind, Context: context, Members: []ProcessRef{}, Sessions: []SessionRef{}}
				if by == "tree" {
					anchor := samples[key]
					bucket.Name, bucket.Kind, bucket.Context = anchor.Name, anchor.Kind, contexts[key]
				}
				buckets[key] = bucket
			} else if by != "tree" {
				common := reflect.ValueOf(&bucket.Context).Elem()
				incoming := reflect.ValueOf(context)
				for field := 0; field < common.NumField(); field++ {
					if common.Field(field).String() != incoming.Field(field).String() {
						common.Field(field).SetString("")
					}
				}
			}
			bucket.Members = append(bucket.Members, member)
		}
	}
	result := []MonitorGroup{}
	for _, bucket := range buckets {
		slices.SortFunc(bucket.Members, func(a, b ProcessRef) int { return strings.Compare(a.Key, b.Key) })
		complete := true
		cores := 0.0
		sessions := map[string]SessionRef{}
		for _, p := range bucket.Members {
			if p.Cores == nil {
				complete = false
			} else {
				cores += *p.Cores
			}
			if p.RSS > math.MaxInt64-bucket.RSS {
				return nil, host, fmt.Errorf("group %s RSS overflow", bucket.Key)
			}
			bucket.RSS += p.RSS
			c := p.Context
			if c.Agent != "" && c.Session != "" {
				key := c.Agent + ":" + c.Session
				s := sessions[key]
				s.Key, s.Agent, s.ID = key, c.Agent, c.Session
				if anchor, ok := anchors[key]; ok {
					s = anchor
				} else {
					s.Error = "owning session has no observed agent anchor"
				}
				sessions[key] = s
			}
		}
		if complete {
			bucket.Cores = new(cores)
			bucket.CPU = new(100 * cores / host.Cores)
		}
		bucket.Mem = 100 * float64(bucket.RSS) / float64(host.MemoryTotalBytes)
		for _, session := range sessions {
			bucket.Sessions = append(bucket.Sessions, session)
		}
		slices.SortFunc(bucket.Sessions, func(a, b SessionRef) int { return strings.Compare(a.Key, b.Key) })
		result = append(result, *bucket)
	}
	slices.SortFunc(result, func(a, b MonitorGroup) int { return strings.Compare(a.Key, b.Key) })
	return result, host, nil
}
func sessionAnchors(snapshot groups.Snapshot, contexts map[string]groups.Context) map[string]SessionRef {
	result := map[string]SessionRef{}
	byPID := map[int]string{}
	for _, p := range snapshot.Processes {
		byPID[p.PID] = p.Key
	}
	for _, group := range snapshot.Frame.Groups {
		c := contexts[group.Key]
		if group.Kind != groups.KindAgent || c.Agent == "" || c.Session == "" {
			continue
		}
		key := c.Agent + ":" + c.Session
		s, exists := result[key]
		if exists && (s.Workspace != c.Workspace || s.Surface != c.Surface) {
			s.Error = "session spans ambiguous cmux locations"
		}
		s.Key, s.ID, s.Agent = key, c.Session, c.Agent
		s.Workspace, s.Surface = c.Workspace, c.Surface
		if anchor, ok := byPID[group.PID]; ok {
			s.Anchors = append(s.Anchors, anchor)
		}
		result[key] = s
	}
	return result
}
func resourceUsage(usage groups.Usage, host Host) (Resources, error) {
	if usage.RSSBytes > math.MaxInt64 {
		return Resources{}, fmt.Errorf("RSS exceeds signed byte range")
	}
	r := Resources{RSS: int64(usage.RSSBytes), Mem: 100 * float64(usage.RSSBytes) / float64(host.MemoryTotalBytes)}
	if usage.CPUPercent != nil {
		if math.IsNaN(*usage.CPUPercent) || math.IsInf(*usage.CPUPercent, 0) || *usage.CPUPercent < 0 {
			return r, fmt.Errorf("invalid CPU measurement")
		}
		r.Cores = new(*usage.CPUPercent / 100)
		r.CPU = new(*usage.CPUPercent / host.Cores)
	}
	return r, nil
}
