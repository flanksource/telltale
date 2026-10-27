package rules

import "github.com/flanksource/telltale/pkg/tracing/groups"

func unattributedGroups(snapshot groups.Snapshot, by string) ([]MonitorGroup, error) {
	contexts, err := groups.ResolveContexts(snapshot.Frame.Groups)
	if err != nil {
		return nil, err
	}
	applications, err := groups.Applications(snapshot.Processes)
	if err != nil {
		return nil, err
	}
	attributed := map[string]bool{}
	for _, p := range snapshot.Processes {
		if applications[p.Key] != "" {
			attributed[p.GroupKey] = true
		}
	}
	result := []MonitorGroup{}
	for _, group := range snapshot.Frame.Groups {
		if group.PID == 0 {
			continue
		}
		c := contexts[group.Key]
		missing := false
		switch by {
		case "repo":
			missing = c.GitCommonDir == ""
		case "worktree":
			missing = c.Repo == ""
		case "session":
			missing = c.Agent == "" || c.Session == ""
		case "application":
			missing = !attributed[group.Key]
		}
		if missing {
			result = append(result, MonitorGroup{Key: group.Key, Name: group.Name, Kind: group.Kind, Context: c, Members: []ProcessRef{}, Sessions: []SessionRef{}})
		}
	}
	return result, nil
}
