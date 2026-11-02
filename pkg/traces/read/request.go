package read

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Aggregate struct {
	Columns []string
	GroupBy string
	View    string
	Follow  bool
}

func (a Aggregate) Enabled() bool { return len(a.Columns) > 0 || a.GroupBy != "" }

// ParseRequest accepts HTTP parameter names and their Cobra flag spellings.
// Presence of limit matters: an explicit zero retains the default page size.
func ParseRequest(values url.Values) (scope Scope, q Query, aggregate Aggregate, err error) {
	values, err = requestValues(values)
	if err != nil {
		return
	}
	scope = Scope{DataDir: values.Get("dataDir"), Run: values.Get("run"), Runs: requestList(values["runs"])}
	q = Query{Filters: values["filter"], Search: values.Get("q"), Where: values.Get("where"), Cursor: values.Get("cursor")}
	aggregate = Aggregate{Columns: requestList(values["stats"]), GroupBy: values.Get("groupBy"), View: values.Get("view")}
	if err = parseRequestTimes(values, &scope, &q); err != nil {
		return
	}
	for name, target := range map[string]*int64{"afterSeq": &q.AfterSeq, "toSeq": &q.ToSeq} {
		if values.Has(name) {
			*target, err = strconv.ParseInt(values.Get(name), 10, 64)
			if err != nil {
				err = fmt.Errorf("%s: %w", name, err)
				return
			}
		}
	}
	if values.Has("limit") {
		q.Limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil {
			err = fmt.Errorf("limit: %w", err)
			return
		}
	}
	for name, target := range map[string]*bool{"all": &q.All, "follow": &aggregate.Follow} {
		if values.Has(name) {
			*target, err = strconv.ParseBool(values.Get(name))
			if err != nil {
				err = fmt.Errorf("%s: %w", name, err)
				return
			}
		}
	}
	if aggregate.Enabled() && !values.Has("limit") {
		q.All = true
	}
	err = validateRequest(scope, q, aggregate, values.Has("limit"))
	return
}

func requestValues(values url.Values) (url.Values, error) {
	aliases := map[string]string{"data-dir": "dataDir", "after-seq": "afterSeq", "to-seq": "toSeq", "query": "q", "group-by": "groupBy"}
	normalized := url.Values{}
	for key, entries := range values {
		if alias, ok := aliases[key]; ok {
			key = alias
		}
		if _, exists := normalized[key]; exists {
			return nil, fmt.Errorf("%s specified more than once", key)
		}
		if key != "filter" && key != "stats" && key != "runs" && len(entries) != 1 {
			return nil, fmt.Errorf("%s requires a single value", key)
		}
		normalized[key] = entries
	}
	return normalized, nil
}

func requestList(values []string) []string {
	var result []string
	for _, value := range values {
		if value != "" {
			result = append(result, strings.Split(value, ",")...)
		}
	}
	return result
}

func parseRequestTimes(values url.Values, scope *Scope, q *Query) error {
	if since := values.Get("since"); since != "" {
		if duration, err := time.ParseDuration(since); err == nil {
			if duration <= 0 {
				return fmt.Errorf("--since must be positive")
			}
			scope.Since = time.Now().Add(-duration)
		} else {
			var err error
			scope.Since, err = time.Parse(time.RFC3339Nano, since)
			if err != nil {
				return fmt.Errorf("--since: expected duration or RFC3339: %w", err)
			}
		}
		q.From = scope.Since
	}
	for name, target := range map[string]*time.Time{"from": &q.From, "until": &q.Until} {
		if raw := values.Get(name); raw != "" {
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return fmt.Errorf("--%s: %w", name, err)
			}
			*target = parsed
		}
	}
	return nil
}

func validateRequest(scope Scope, q Query, aggregate Aggregate, limitSet bool) error {
	if scope.Run != "" && len(scope.Runs) > 0 {
		return fmt.Errorf("--run and --runs are mutually exclusive")
	}
	if aggregate.Follow && len(scope.Runs) > 1 {
		return fmt.Errorf("--follow requires a single run")
	}
	if err := q.Validate(); err != nil {
		return err
	}
	if aggregate.Follow && (aggregate.Enabled() || aggregate.View != "" || q.Cursor != "") {
		return fmt.Errorf("--follow cannot combine with --stats, --group-by, --view or --cursor")
	}
	if aggregate.View != "" && (aggregate.Enabled() || !q.From.IsZero() || !q.Until.IsZero() || len(q.Filters) > 0 || q.Search != "" || q.Where != "" || q.Cursor != "" || limitSet) {
		return fmt.Errorf("--view accepts a seq window, without row filters, time bounds, paging or --stats/--group-by")
	}
	return nil
}
