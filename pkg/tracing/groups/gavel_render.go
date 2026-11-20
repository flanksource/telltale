package groups

import (
	"cmp"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/gavel/sdk/status"
)

// GavelText is a gavel process's progress in a line: each task group's and
// its tests' finished of total and failures, then the slots it holds (●)
// and waits for (⧗).
func GavelText(s *status.Status) string {
	if s == nil {
		return ""
	}
	var parts []string
	for _, group := range s.Groups {
		if group.Summary.Total > 0 {
			parts = append(parts, summaryText(cmp.Or(group.Kind, group.Name), group.Summary))
		}
	}
	if s.Tests != nil && s.Tests.Total > 0 {
		parts = append(parts, summaryText("tests", *s.Tests))
	}
	if slots := SlotGlyphs(s); slots != "" {
		parts = append(parts, slots)
	}
	return strings.Join(parts, " · ")
}

func leaseGlyph(lease status.Lease) string {
	if lease.State == status.LeaseWaiting {
		return "⧗"
	}
	return "●"
}

// rowBar is the width of a progress bar on a tree row.
const rowBar = 8

// barPart is n of a bar's total, drawn as glyph.
type barPart struct {
	n     int
	glyph string
}

// progressBar draws parts of total across width cells, in order, leaving the
// rest ░. Each part ends where its running sum rounds to, so the parts never
// overflow the bar, but a part that has any takes at least a cell: one
// failure in hundreds still shows.
func progressBar(width, total int, parts ...barPart) string {
	var b strings.Builder
	b.WriteString("[")
	drawn, sum := 0, 0
	for _, part := range parts {
		sum += part.n
		end := width
		if total > 0 {
			end = min(width, (sum*width+total/2)/total)
		}
		if part.n > 0 && end <= drawn && drawn < width {
			end = drawn + 1
		}
		if end > drawn {
			b.WriteString(strings.Repeat(part.glyph, end-drawn))
			drawn = end
		}
	}
	b.WriteString(strings.Repeat("░", width-drawn) + "]")
	return b.String()
}

// summaryBar is a summary as a bar: █ finished, ▓ failed, ▒ running, ░ pending.
func summaryBar(width int, s status.Summary) string {
	return progressBar(width, s.Total, barPart{s.Done() - s.Failed, "█"}, barPart{s.Failed, "▓"}, barPart{s.Running, "▒"})
}

func summaryText(label string, s status.Summary) string {
	text := fmt.Sprintf("%s %s %d/%d", label, summaryBar(rowBar, s), s.Done(), s.Total)
	if s.Failed > 0 {
		text += fmt.Sprintf(" ✗%d", s.Failed)
	}
	return text
}

// poolLabel is "global", or a project pool's repository name.
func poolLabel(pool, project string) string {
	if pool == "global" || project == "" {
		return pool
	}
	if filepath.Base(project) == ".git" {
		project = filepath.Dir(project)
	}
	return strings.TrimSuffix(filepath.Base(project), ".git")
}

// SlotsText is each slot pool's held of limit and how many wait for it.
func SlotsText(pools []SlotPool) string {
	var parts []string
	for _, pool := range pools {
		text := fmt.Sprintf("%s %s %d/%d", poolLabel(pool.Name, pool.Project), progressBar(rowBar, pool.Limit, barPart{pool.Held, "█"}), pool.Held, pool.Limit)
		if pool.Waiting > 0 {
			text += fmt.Sprintf(" +%d waiting", pool.Waiting)
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " · ")
}

// SlotGlyphs are the slots a gavel process holds (●) and waits for (⧗).
func SlotGlyphs(s *status.Status) string {
	if s == nil {
		return ""
	}
	var leases []string
	for _, lease := range s.Slots {
		leases = append(leases, leaseGlyph(lease)+poolLabel(lease.Pool, lease.Project))
	}
	return strings.Join(leases, " ")
}

// TaskText is what a row WithGavelTasks added says at now: a task group's
// progress, or a running task, how long it has run and its own progress.
func TaskText(group GroupSample, now time.Time) string {
	task := group.Task
	switch {
	case task == nil:
		return ""
	case task.Summary != nil:
		return summaryText(group.Name, *task.Summary)
	}
	item := task.Item
	text := fmt.Sprintf("▶ %s %s", item.Name, age(now, item.Started))
	if item.Total > 0 {
		text += fmt.Sprintf(" %s %d/%d", progressBar(rowBar, item.Total, barPart{item.Done, "█"}), item.Done, item.Total)
	}
	if item.Description != "" {
		text += " · " + item.Description
	}
	return text
}

func age(now, since time.Time) string {
	if since.IsZero() {
		return "-"
	}
	return now.Sub(since).Round(time.Second).String()
}
