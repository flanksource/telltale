package processcapture

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
)

func ownershipHierarchy(rows []query.Row, metric string) (*sampleframe.Hierarchy, error) {
	if metric != "cpuPercent" && metric != "rssBytes" {
		return nil, fmt.Errorf("unknown process metric %q", metric)
	}
	stacks := []sampleframe.WeightedStack{}
	for _, row := range rows {
		if row[metric] == nil {
			continue
		}
		encoded, err := json.Marshal(row[metric])
		if err != nil {
			return nil, err
		}
		var value float64
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, err
		}
		encoded, err = json.Marshal(row["groups"])
		if err != nil {
			return nil, err
		}
		var owners []GroupInfo
		if err := json.Unmarshal(encoded, &owners); err != nil {
			return nil, err
		}
		stack := sampleframe.WeightedStack{Value: value}
		for _, owner := range slices.Backward(owners) {
			stack.Frames = append(stack.Frames, owner.Name)
			stack.IDs = append(stack.IDs, owner.Key)
		}
		stack.Frames = append(stack.Frames, fmt.Sprintf("%v (pid %v)", row["name"], row["pid"]))
		stack.IDs = append(stack.IDs, fmt.Sprint(row["entity"]))
		stacks = append(stacks, stack)
	}
	if len(stacks) == 0 {
		return nil, nil
	}
	root, err := sampleframe.BuildHierarchy(stacks)
	if err != nil {
		return nil, err
	}
	root.Label = "Recorded process usage"
	unit := "percent"
	if metric == "rssBytes" {
		unit = "bytes"
	}
	return &sampleframe.Hierarchy{Metric: metric, Unit: unit, Root: root}, nil
}
