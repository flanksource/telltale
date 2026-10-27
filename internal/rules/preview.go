package rules

import (
	"context"
	"github.com/flanksource/telltale/pkg/tracing/groups"
)

type Preview struct {
	Rule    string        `json:"rule"`
	Group   MonitorGroup  `json:"group"`
	Matched bool          `json:"matched"`
	Known   bool          `json:"known"`
	Error   string        `json:"error,omitempty"`
	Steps   []PreviewStep `json:"steps"`
}
type PreviewStep struct {
	Hook    HookSpec `json:"hook"`
	Targets Targets  `json:"targets"`
	Error   string   `json:"error,omitempty"`
}

func (p *Program) Preview(ctx context.Context, snapshot groups.Snapshot) ([]Preview, error) {
	result := []Preview{}
	for _, rule := range p.rules {
		resolved, host, err := Resolve(snapshot, rule.rule.GroupBy)
		if err != nil {
			return nil, err
		}
		if rule.rule.GroupBy != "tree" {
			unattributed, err := unattributedGroups(snapshot, rule.rule.GroupBy)
			if err != nil {
				return nil, err
			}
			for _, group := range unattributed {
				result = append(result, Preview{Rule: rule.rule.Name, Group: group, Error: "missing " + rule.rule.GroupBy + " attribution", Steps: []PreviewStep{}})
			}
		}
		for _, group := range resolved {
			item := Preview{Rule: rule.rule.Name, Group: group, Steps: []PreviewStep{}}
			matched, known, err := rule.match.predicate(ctx, group, host, snapshot.Frame.SampledAt, Incident{})
			item.Matched, item.Known = matched, known
			if err != nil {
				item.Error = err.Error()
			}
			if err == nil && known && matched {
				for i, step := range rule.rule.Steps {
					targets, err := rule.targets[i].targets(ctx, group, host, snapshot.Frame.SampledAt, Incident{Rule: rule.rule.Name, Group: group.Key, Step: i})
					preview := PreviewStep{Hook: step.Hook, Targets: targets}
					if err != nil {
						preview.Error = err.Error()
					}
					item.Steps = append(item.Steps, preview)
				}
			}
			result = append(result, item)
		}
	}
	return result, nil
}
