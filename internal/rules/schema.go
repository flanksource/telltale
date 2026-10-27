package rules

import "github.com/flanksource/clicky/rpc"

func Schema() *rpc.OpenAPISchema {
	schema := rpc.SchemaForStruct(Config{})
	schema.Required = []string{"version", "rules"}
	schema.Properties["version"].Enum = []any{1}
	rule := schema.Properties["rules"].Items
	rule.Required = []string{"name", "groupBy", "match", "for", "recoverFor", "steps"}
	rule.Properties["groupBy"].Enum = []any{"tree", "application", "repo", "worktree", "session", "host"}
	rule.Properties["match"].Description = "CEL boolean trigger; true sustained for 'for' opens an incident"
	rule.Properties["recover"].Description = "CEL boolean recovery condition; defaults to !match"
	hook := rule.Properties["steps"].Items.Properties["hook"]
	hook.Required = []string{"type", "targets"}
	hook.Properties["type"].Enum = []any{"renice", "signal", "session-interrupt", "exec"}
	hook.Properties["targets"].Description = "CEL list of process or session references from the current group"
	return schema
}
func Bindings() map[string]string {
	return map[string]string{"cores": "Consumed logical cores (double); unknown until CPU is rated", "cpu": "Percent of host capacity: 100 * cores / host.cores", "rss": "Resident bytes (int)", "mem": "Percent of host physical memory occupied by RSS", "group": "Typed identity, context, members and sessions", "host": "Host capacity: cores and memoryTotalBytes", "now": "Snapshot timestamp", "incident": "Current incident, available in targets expressions"}
}
