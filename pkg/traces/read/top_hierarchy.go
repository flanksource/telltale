package read

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/tracing"
)

func topHierarchy(rows []query.Row, spec *tracing.TopSpec, metric string) (*sampleframe.Hierarchy, error) {
	if spec.Hierarchy != nil {
		return spec.Hierarchy(rows, metric)
	}
	if spec.Stack == nil {
		return nil, nil
	}
	if len(spec.Metrics) == 0 && metric == "" {
		return nil, nil
	}
	index := slices.IndexFunc(spec.Metrics, func(m tracing.TopMetric) bool { return m.Key == metric })
	if index < 0 {
		return nil, fmt.Errorf("unknown profile metric %q", metric)
	}
	stacks := make([]sampleframe.WeightedStack, 0, len(rows))
	for _, row := range rows {
		data, err := json.Marshal(row["value"])
		if err != nil {
			return nil, err
		}
		var value float64
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, fmt.Errorf("profile weight: %w", err)
		}
		frames, err := stackFrames(row[spec.Stack.Column], *spec.Stack)
		if err != nil {
			return nil, err
		}
		if spec.Stack.LeafFirst {
			slices.Reverse(frames)
		}
		stacks = append(stacks, sampleframe.WeightedStack{Frames: frames, Value: value})
	}
	root, err := sampleframe.BuildHierarchy(stacks)
	if err != nil {
		return nil, err
	}
	return &sampleframe.Hierarchy{Metric: metric, Unit: spec.Metrics[index].Unit, Root: root}, nil
}

func stackFrames(raw any, spec tracing.TopStack) ([]string, error) {
	if spec.Separator != "" {
		stack, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("profile stack must be a string, got %T", raw)
		}
		if stack == "" {
			return nil, nil
		}
		return strings.Split(stack, spec.Separator), nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var frames []map[string]any
	if err := json.Unmarshal(data, &frames); err != nil {
		return nil, err
	}
	names := make([]string, len(frames))
	for i, frame := range frames {
		name, ok := frame[spec.FrameKey].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("profile frame has no %q", spec.FrameKey)
		}
		file, _ := frame["file"].(string)
		if file != "" {
			name += fmt.Sprintf(" (%s:%v)", file, frame["line"])
		}
		names[i] = name
	}
	return names, nil
}

func selectMetric(rows []query.Row, spec *tracing.TopSpec, requested string) (string, error) {
	if spec.Stack == nil && spec.Hierarchy == nil {
		if requested != "" {
			return "", fmt.Errorf("kind does not offer profile metrics")
		}
		return "", nil
	}
	if len(spec.Metrics) == 0 && requested == "" {
		return "", nil
	}
	for _, metric := range spec.Metrics {
		if requested != "" && metric.Key != requested {
			continue
		}
		if spec.Hierarchy != nil || requested != "" || len(rows) == 0 || slices.ContainsFunc(rows, func(row query.Row) bool { return row[spec.Stack.MetricColumn] == metric.Key }) {
			return metric.Key, nil
		}
	}
	return "", fmt.Errorf("profile metric %q unavailable in selected sample", requested)
}
