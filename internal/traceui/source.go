package traceui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
)

func (m Model) load() tea.Cmd {
	m.tabs = slices.Clone(m.tabs)
	m.options.Runs = slices.Clone(m.options.Runs)
	t := m.tabs[m.active]
	return func() tea.Msg {
		result := pageMsg{generation: m.generation}
		if t.file != "" {
			result.output, result.err = m.readOutput(t.file)
			return result
		}
		refs := slices.Clone(t.refs)
		identities := map[string]query.Row{}
		if t.name == processcapture.Kind {
			refs = nil
			for _, ref := range t.refs {
				switch ref.Kind {
				case processcapture.MetricsTable:
					refs = append(refs, ref)
				case processcapture.Kind:
					if err := ref.Runtime.Read(m.ctx, ref.TraceRef, func(_ int64, row recordstore.Row) error {
						key, ok := row["key"].(string)
						if !ok {
							return fmt.Errorf("process identity has no key")
						}
						identities[key] = query.Row(row)
						return nil
					}); err != nil {
						result.err = err
						return result
					}
				}
			}
			result.columns = []query.ColumnDef{{Name: "pid", Label: "PID"}, {Name: "name", Label: "Process"}, {Name: "cpuPercent", Label: "CPU %"}, {Name: "rssBytes", Label: "RSS"}, {Name: "openFiles", Label: "Files"}, {Name: "receivingSockets", Label: "Listeners"}, {Name: "connectionsIn", Label: "Peers in"}, {Name: "connectionsOut", Label: "Peers out"}, {Name: "connectionsUnknown", Label: "Peers ?"}}
		}
		var observations int64
		for _, ref := range refs {
			plugin, ok := ref.Runtime.Kinds.Get(ref.Kind)
			if !ok {
				result.err = fmt.Errorf("unknown trace kind %s", ref.Kind)
				return result
			}
			schema, err := plugin.Schema(ref.Kind)
			if err != nil {
				result.err = err
				return result
			}
			if len(result.columns) == 0 {
				for _, column := range schema.Columns {
					if !column.Hidden && column.Type != "json" {
						result.columns = append(result.columns, column)
					}
				}
			}
			meta, err := ref.Runtime.Results.Backend.Meta(m.ctx, ref.Stream)
			if err != nil {
				result.err = err
				return result
			}
			result.watermarks = append(result.watermarks, meta.HighSeq)
			observations += meta.Total
		}
		result.monitor = t.monitor
		if t.name == processcapture.Kind {
			result.monitor = fmt.Sprintf("%d processes · %d numeric observations · resource counts from committed deltas", len(identities), observations)
		} else if !slices.Equal(t.watermarks, result.watermarks) || t.monitor == "" {
			result.monitor = ""
			for _, ref := range t.refs {
				summary, err := ref.Runtime.Summarize(m.ctx, ref.TraceRef)
				if err != nil {
					result.err = err
					return result
				}
				result.monitor += ref.Stream + " · " + summaryText(summary) + "\n"
			}
		}
		if !m.options.Replay && !t.browse && t.cursor == "" {
			for i := range refs {
				refs[i].AfterSeq = max(refs[i].AfterSeq, result.watermarks[i]-100)
			}
		}
		result.page, result.err = read.FeedPage(m.ctx, refs, read.FeedQuery{Sort: m.options.Sort, Query: read.Query{Cursor: t.cursor}})
		if t.name == processcapture.Kind && result.err == nil {
			for i := range result.page.Rows {
				row := &result.page.Rows[i]
				key, _ := row.Record["entity"].(string)
				identity, ok := identities[key]
				if !ok {
					result.err = fmt.Errorf("metrics reference unknown process %q", key)
					return result
				}
				row.Record["name"], row.Record["pid"] = identity["name"], identity["pid"]
			}
		}
		return result
	}
}

func (m Model) readOutput(channel string) (string, error) {
	if len(m.options.Runs) == 0 {
		return "Output unavailable: no command recording.", nil
	}
	run := m.options.Runs[m.run]
	if run.Output == nil {
		if channel == "stderr" {
			return "Separate stderr unavailable in this recording.", nil
		}
		path := filepath.Join(run.Dir, telltale.OutputFile)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return "Output unavailable: process output was not recorded.", nil
		} else if err != nil {
			return "", err
		}
		return outputWindow(path, m.tabs[m.active].outputOffset)
	}
	name := run.Output.Stdout
	if channel == "stderr" {
		name = run.Output.Stderr
	}
	if name == "" || filepath.Base(name) != name {
		return "", fmt.Errorf("invalid recorded %s path %q", channel, name)
	}
	return outputWindow(filepath.Join(run.Dir, name), m.tabs[m.active].outputOffset)
}

func outputWindow(path string, offset int64) (_ string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	const limit = 1 << 20
	start := offset
	if start < 0 {
		start = max(int64(0), info.Size()+offset*limit)
	}
	start = min(start, max(int64(0), info.Size()-limit))
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return "", err
	}
	text := string(data)
	if info.Size() > limit {
		text = fmt.Sprintf("[bytes %d..%d of %d · Home/End start/tail · n/p blocks]\n", start, start+int64(len(data)), info.Size()) + text
	}
	return text, nil
}

func summaryText(summary map[string]any) string {
	keys := make([]string, 0, len(summary))
	for key := range summary {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := []string{}
	for _, key := range keys {
		switch value := summary[key].(type) {
		case map[string]any:
			parts = append(parts, key+" {"+summaryText(value)+"}")
		case map[string]int:
			nested := map[string]any{}
			for k, v := range value {
				nested[k] = v
			}
			parts = append(parts, key+" {"+summaryText(nested)+"}")
		case map[string]int64:
			nested := map[string]any{}
			for k, v := range value {
				nested[k] = v
			}
			parts = append(parts, key+" {"+summaryText(nested)+"}")
		case []map[string]any:
			if key != "requests" && key != "queries" {
				for _, v := range value {
					parts = append(parts, summaryText(v))
				}
			}
		case string, bool, int, int64, uint64, float64, float32:
			parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		}
	}
	return strings.Join(parts, " · ")
}
