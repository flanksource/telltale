// The sessions API in the OpenAPI document: the list with its filters, window,
// sort and paging, one session and its actions, and the session start of each
// trace or top profile.

package sessions

import (
	"context"
	"fmt"
	"strings"

	"github.com/flanksource/clicky/entity"
	"github.com/flanksource/clicky/rpc"

	"github.com/flanksource/commons-db/cmd/query/profiles"
	"github.com/flanksource/commons-db/query"
)

const (
	sessionsSurface     = "sessions"
	sessionsPath        = "/api/v1/sessions"
	sessionPath         = sessionsPath + "/{id}"
	profileSessionsPath = "/api/v1/profile/"
)

// sessionFilter is one filter parameter of the sessions list.
type sessionFilter struct {
	name, label string
	value       bool // a single typed value rather than a multi-select
	enum        []string
}

func sessionFilters() []sessionFilter {
	states := []string{string(query.SessionStarting), string(query.SessionRunning), string(query.SessionStopping),
		string(query.SessionCompleted), string(query.SessionFailed), string(query.SessionStopped), string(query.SessionInterrupted)}
	filters := []sessionFilter{
		{name: "profile", label: "Profile"},
		{name: "kind", label: "Kind", enum: []string{string(query.KindTrace), string(query.KindTop), string(query.KindCapture)}},
		{name: "state", label: "State", enum: states},
		{name: "principal", label: "Principal"},
		{name: "restartOf", label: "Restart of", value: true},
	}
	for _, key := range []string{"target", "origin", "via", "runId", "plan", "step", "environment"} {
		filters = append(filters, sessionFilter{name: "label." + key, label: key})
	}
	return filters
}

// AddSessionsOpenAPI emits the sessions API — the list with its filters,
// window, sort and paging, one session, and its stop, extend and restart
// actions — as the "sessions" surface a clicky-ui OperationCatalog renders. It
// is a profiles.OpenAPIExtension and an rpc.Config extension alike.
func AddSessionsOpenAPI(spec *rpc.OpenAPISpec) {
	if spec.Paths == nil {
		spec.Paths = map[string]rpc.OpenAPIPath{}
	}
	if spec.Clicky == nil {
		spec.Clicky = &rpc.ClickySpecMeta{}
	}
	spec.Clicky.Surfaces = append(spec.Clicky.Surfaces, rpc.ClickySurface{Key: sessionsSurface, Entity: sessionsSurface, Title: "Sessions"})
	spec.Paths[sessionsPath] = rpc.OpenAPIPath{"get": sessionListOperation(spec)}
	spec.Paths[sessionPath] = rpc.OpenAPIPath{"get": {
		OperationID: "get-session", Summary: "Get a session", Parameters: []rpc.OpenAPIParameter{sessionIDParameter()},
		Responses: map[string]rpc.OpenAPIResponse{"200": {Description: "SessionInfo"}, "404": {Description: "No such session"}},
		Clicky:    &rpc.ClickyOperationMeta{Command: sessionsSurface, Surface: sessionsSurface, Verb: "get", Scope: "entity", IDParam: "id"},
	}}
	spec.Paths[sessionPath+"/stop"] = rpc.OpenAPIPath{"post": sessionAction("stop-session", "Stop a session", nil)}
	spec.Paths[sessionPath+"/extend"] = rpc.OpenAPIPath{"post": sessionAction("extend-session", "Move a session's deadline later",
		sessionDurationParameter(true, "Go duration added to stopAt, e.g. 900000ms or 15m; clamped to the registry's MaxDuration from the start"))}
	restart := sessionAction("restart-session", "Start a new session from an ended one's params",
		sessionDurationParameter(false, "Go duration for the new run, recorded as its params.durationMs; absent replays the ended "+
			"session's params.durationMs — the duration it was first requested for, not a run an extension lengthened"))
	restart.Responses["201"] = rpc.OpenAPIResponse{Description: "The new session (SessionInfo) with restartOf set"}
	restart.Responses["409"] = rpc.OpenAPIResponse{Description: "Not terminal, or not restartable here"}
	delete(restart.Responses, "200")
	spec.Paths[sessionPath+"/restart"] = rpc.OpenAPIPath{"post": restart}
}

// AddProfileSessionsOpenAPI emits POST /api/v1/profile/{key}/sessions for
// every trace or top profile the service's store holds now, replacing the
// starts an earlier document held. It is an rpc.Config request extension. A
// query profile gets none: live-tailing it is the profile read's ?follow.
func (s *Service) AddProfileSessionsOpenAPI(ctx context.Context, spec *rpc.OpenAPISpec) error {
	store, err := s.options.Profiles()
	if err != nil {
		return fmt.Errorf("profile session starts: %w", err)
	}
	saved, err := store.List(ctx)
	if err != nil {
		return fmt.Errorf("profile session starts: %w", err)
	}
	if spec.Paths == nil {
		spec.Paths = map[string]rpc.OpenAPIPath{}
	}
	for path := range spec.Paths {
		if strings.HasPrefix(path, profileSessionsPath) && strings.HasSuffix(path, "/sessions") {
			delete(spec.Paths, path)
		}
	}
	for _, p := range saved {
		resolved, err := profiles.ResolveWithoutTouch(ctx, store, p.Name)
		if err != nil {
			return fmt.Errorf("profile session start %q: %w", p.Name, err)
		}
		if resolved.Profile.Kind() == query.KindQuery {
			continue
		}
		key := profiles.SurfaceKey(resolved.Profile.Name)
		spec.Paths[profileSessionsPath+key+"/sessions"] = rpc.OpenAPIPath{"post": profileSessionStart(resolved.Profile, key)}
	}
	return nil
}

// profileSessionStart is the start operation of a trace or top profile: its
// params, a top's interval override and the duration bound.
func profileSessionStart(profile query.Profile, key string) rpc.OpenAPIOperation {
	parameters := make([]rpc.OpenAPIParameter, 0, len(profile.Params)+2)
	for _, param := range profile.Params {
		parameters = append(parameters, profileSessionParameter(param))
	}
	if profile.Kind() == query.KindTop {
		parameters = append(parameters, rpc.OpenAPIParameter{Name: "interval", In: "query",
			Description: "Go duration between samples, e.g. 5s; overrides the profile's", Schema: &rpc.OpenAPISchema{Type: "string"}})
	}
	parameters = append(parameters, sessionDurationParameter(false, "Go duration the session runs for, e.g. 15m; clamped to the registry's MaxDuration")...)
	return rpc.OpenAPIOperation{
		Summary: "Start a " + string(profile.Kind()) + " session for " + profile.Name,
		Description: "Start a live session; follow it via GET /api/v1/sessions/{id}/events (SSE) and stop it via POST /api/v1/sessions/{id}/stop. " +
			"Each event frame's data is a session event: a trace event's row is the raw row, and its clickyRow is the same row " +
			"exactly as this profile's application/json+clicky table page presents it (node.rows[n])",
		OperationID: "start-" + key + "-session",
		Parameters:  parameters,
		Responses: map[string]rpc.OpenAPIResponse{
			"201": {Description: "Session started (SessionInfo)"},
			"400": {Description: "Invalid params, interval or duration"},
			"409": {Description: "The registry's session cap is reached"},
		},
	}
}

func profileSessionParameter(param query.ParamDef) rpc.OpenAPIParameter {
	schema := &rpc.OpenAPISchema{Type: "string", Title: param.DisplayLabel(), Description: param.Description, Default: param.Default}
	switch param.Type {
	case query.ParamTypeNumber:
		schema.Type = "number"
	case query.ParamTypeBoolean:
		schema.Type = "boolean"
	case query.ParamTypeDate:
		schema.Format = "date"
	case query.ParamTypeDateTime:
		schema.Format = "date-time"
	}
	if param.Type != query.ParamTypeList && param.Type != query.ParamTypeLabels {
		for _, option := range param.Options {
			schema.Enum = append(schema.Enum, option)
		}
	}
	return rpc.OpenAPIParameter{Name: param.Name, In: "query", Description: param.Description, Required: param.Required, Schema: schema}
}

func sessionListOperation(spec *rpc.OpenAPISpec) rpc.OpenAPIOperation {
	var parameters []rpc.OpenAPIParameter
	for _, filter := range sessionFilters() {
		component := "sessions-" + strings.ReplaceAll(filter.name, ".", "-")
		shape := "multi-filter"
		if filter.value {
			shape = "value"
		}
		ensureFilterComponent(spec, entity.FilterSpec{Name: component, Label: filter.label, Type: shape, Multi: !filter.value})
		schema := &rpc.OpenAPISchema{Type: "string"}
		for _, value := range filter.enum {
			schema.Enum = append(schema.Enum, value)
		}
		parameters = append(parameters, rpc.OpenAPIParameter{
			Name: filter.name, In: "query", Schema: schema, Clicky: &rpc.ClickyParameterMeta{Role: "filter"},
			Description: "Include or exclude " + filter.label + " values: comma-separated, a leading ! excludes, * matches " +
				"any run of characters. Repeated keys (?state=running&state=stopping) are the same selection.",
			Lookup: &rpc.ClickyLookupMeta{
				Ref: "#/components/x-clicky-filters/" + component, URL: sessionsPath, Filter: filter.name,
				SearchParam: "__lookup_q", Multi: !filter.value,
			},
		})
	}
	return rpc.OpenAPIOperation{
		OperationID: "list-sessions", Summary: "List sessions",
		Description: "Capture sessions live in this process or persisted in the session store, authorized per profile " +
			"before totals, paging and lookups.",
		Parameters: append(parameters, sessionListPagingParameters()...),
		Responses: map[string]rpc.OpenAPIResponse{"200": {
			Description: "application/json: {items: SessionInfo[], total, shared}; application/json+clicky: a table document. " +
				"Paging travels in X-Total-Count / X-Page-Limit / X-Page-Offset, and X-Sessions-Shared says whether the store is shared.",
		}},
		Clicky: &rpc.ClickyOperationMeta{Command: sessionsSurface, Surface: sessionsSurface, Verb: "list", Scope: "collection"},
	}
}

func ensureFilterComponent(spec *rpc.OpenAPISpec, filter entity.FilterSpec) {
	if spec.Components == nil {
		spec.Components = &rpc.OpenAPIComponents{}
	}
	if spec.Components.ClickyFilters == nil {
		spec.Components.ClickyFilters = map[string]entity.FilterSpec{}
	}
	spec.Components.ClickyFilters[filter.Name] = filter
}

func sessionListPagingParameters() []rpc.OpenAPIParameter {
	param := func(name, role, description string, schema *rpc.OpenAPISchema) rpc.OpenAPIParameter {
		return rpc.OpenAPIParameter{Name: name, In: "query", Description: description, Schema: schema, Clicky: &rpc.ClickyParameterMeta{Role: role}}
	}
	sortColumns := make([]any, len(query.SessionSortFields))
	for i, field := range query.SessionSortFields {
		sortColumns[i] = field
	}
	return []rpc.OpenAPIParameter{
		param("from", "time-from", "Started from", &rpc.OpenAPISchema{Type: "string", Format: "date-time"}),
		param("to", "time-to", "Started until", &rpc.OpenAPISchema{Type: "string", Format: "date-time"}),
		param("sort", "sort", "Column to order by; id always breaks ties", &rpc.OpenAPISchema{Type: "string", Enum: sortColumns, Default: "startedAt"}),
		param("order", "order", "Direction for sort", &rpc.OpenAPISchema{Type: "string", Enum: []any{"asc", "desc"}, Default: "desc"}),
		param("limit", "limit", "Sessions per page (maximum 500)", &rpc.OpenAPISchema{Type: "integer", Default: 50}),
		param("offset", "offset", "Sessions to skip", &rpc.OpenAPISchema{Type: "integer", Default: 0}),
	}
}

func sessionIDParameter() rpc.OpenAPIParameter {
	return rpc.OpenAPIParameter{Name: "id", In: "path", Required: true, Schema: &rpc.OpenAPISchema{Type: "string"}}
}

func sessionDurationParameter(required bool, description string) []rpc.OpenAPIParameter {
	return []rpc.OpenAPIParameter{{Name: "duration", In: "query", Required: required, Description: description, Schema: &rpc.OpenAPISchema{Type: "string"}}}
}

func sessionAction(operationID, summary string, parameters []rpc.OpenAPIParameter) rpc.OpenAPIOperation {
	return rpc.OpenAPIOperation{
		OperationID: operationID, Summary: summary,
		Parameters: append([]rpc.OpenAPIParameter{sessionIDParameter()}, parameters...),
		Responses: map[string]rpc.OpenAPIResponse{
			"200": {Description: "The session as it stands after the request (SessionInfo)"},
			"403": {Description: "Authorize(r, profile, control) refused"},
			"404": {Description: "No such session"},
			"409": {Description: "The session's state does not allow this action"},
		},
	}
}
