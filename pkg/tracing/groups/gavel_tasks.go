package groups

import (
	"cmp"
	"math"
	"strconv"

	"github.com/flanksource/gavel/sdk/status"
)

// GavelTask is what a row WithGavelTasks adds shows: a task group's
// progress, or one running task. Order keeps the rows in gavel's order.
type GavelTask struct {
	Order   int
	Summary *status.Summary
	Item    *status.Item
}

// taskRank orders a gavel's task rows first, in gavel's order.
func taskRank(group GroupSample) int {
	if group.Task == nil {
		return math.MaxInt
	}
	return group.Task.Order
}

// WithGavelTasks nests what each gavel group publishes under it: a row per
// task group with progress, and under each a row per task it is running.
// A skip-listed gavel's tasks claim the group of the process each runs, by
// pid, wherever that group is (a detached process included); its other
// groups stay where ancestry put them. The frame is rolled up again, so a
// claimed group's use counts once, under its task.
func (f Frame) WithGavelTasks() Frame {
	byKey := map[string]GroupSample{}
	for _, group := range f.Groups {
		byKey[group.Key] = group
	}
	var added []GroupSample
	claims := map[int]string{}
	ancestors := map[int]map[string]bool{}
	for _, group := range f.Groups {
		if group.Gavel == nil || group.Kind == KindBucket {
			continue
		}
		rows, itemPIDs := gavelTaskRows(group)
		added = append(added, rows...)
		if !group.SkipAncestry {
			continue
		}
		lineage := map[string]bool{}
		for key := group.Key; key != ""; key = byKey[key].ParentKey {
			if lineage[key] {
				break
			}
			lineage[key] = true
		}
		for pid, key := range itemPIDs {
			claims[pid], ancestors[pid] = key, lineage
		}
	}
	if len(added) == 0 {
		return f
	}
	groups := make([]GroupSample, 0, len(f.Groups)+len(added))
	for _, group := range f.Groups {
		if key, ok := claims[group.PID]; ok && group.PID != 0 && group.Kind != KindBucket && !ancestors[group.PID][group.Key] {
			group.ParentKey = key
		}
		groups = append(groups, group)
	}
	f.Groups = Rollup(append(groups, added...))
	return f
}

// gavelTaskRows are the rows of gavel's task groups and running tasks, and
// the row of each task that runs a process, by its pid.
func gavelTaskRows(gavel GroupSample) ([]GroupSample, map[int]string) {
	row := func(key, parent, name, kind string, task *GavelTask) GroupSample {
		return GroupSample{
			SampledAt: gavel.SampledAt, Tick: gavel.Tick, Key: key, ParentKey: parent, Name: name, Kind: kind,
			Context: gavel.Context, Origin: gavel.Origin, Task: task,
		}
	}
	var rows []GroupSample
	byName := map[string]string{}
	for _, group := range gavel.Gavel.Groups {
		if group.Summary.Total == 0 {
			continue
		}
		name := cmp.Or(group.Kind, group.Name)
		key := gavel.Key + "/gavel/" + name
		summary := group.Summary
		rows = append(rows, row(key, gavel.Key, name, KindGavelTask, &GavelTask{Order: len(rows), Summary: &summary}))
		byName[group.Name] = key
	}
	if tests := gavel.Gavel.Tests; tests != nil && tests.Total > 0 {
		summary := *tests
		rows = append(rows, row(gavel.Key+"/gavel/tests", gavel.Key, "tests", KindGavelTask, &GavelTask{Order: len(rows), Summary: &summary}))
	}
	pids := map[int]string{}
	for index, item := range gavel.Gavel.Running {
		parent, ok := byName[item.Group]
		if !ok {
			parent = gavel.Key
		}
		key := parent + "/" + strconv.Itoa(index)
		running := item
		rows = append(rows, row(key, parent, item.Name, KindGavelItem, &GavelTask{Order: index, Item: &running}))
		if item.PID > 0 {
			pids[item.PID] = key
		}
	}
	return rows, pids
}
