package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traces/read"
)

type evidence struct {
	Kind       string `json:"kind"`
	Count      int    `json:"count"`
	Meaningful bool   `json:"meaningful"`
}

func validateCoverage(kinds []string, rows map[string][]query.Row) ([]evidence, error) {
	var result []evidence
	var failures []error
	for _, kind := range kinds {
		item := evidence{Kind: kind, Count: len(rows[kind])}
		for _, row := range rows[kind] {
			item.Meaningful = item.Meaningful || meaningful(kind, row)
			if err := captureLoss(map[string]any(row)); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", kind, err))
			}
		}
		if kind == "terminal" {
			item.Meaningful = item.Meaningful && slices.ContainsFunc(rows[kind], func(row query.Row) bool { return row["kind"] == "exit" && number(row["exitCode"]) == 0 })
		}
		if kind == "llm" && (len(rows[kind]) != 2 || !slices.ContainsFunc(rows[kind], func(row query.Row) bool { return row["phase"] == "request" })) {
			failures = append(failures, fmt.Errorf("llm: expected exactly one request and response"))
		}
		if !item.Meaningful {
			failures = append(failures, fmt.Errorf("%s: %d rows, no successful workload evidence", kind, item.Count))
		}
		result = append(result, item)
	}
	return result, errors.Join(failures...)
}

func meaningful(kind string, row query.Row) bool {
	switch kind {
	case "exec":
		return text(row["name"]) != "" && number(row["exitCode"]) == 0 && text(row["error"]) == ""
	case "terminal":
		return row["kind"] == "o" && text(row["data"]) != ""
	case "goprof", "stacks":
		return text(row["frames"]) != "" && text(row["frames"]) != "[]"
	case "jvm_jfr":
		return strings.Contains(text(row["stack"]), "DemoWorkload") && number(row["value"]) > 0
	case "prometheus":
		return row["health"] == "up" && number(row["samples"]) > 0
	case "sql_plancache":
		return number(row["executions"]) > 0
	case "sql_deadlock":
		return number(row["victims"]) > 0 && strings.Contains(text(row["xml"]), "deadlock")
	case "sql_xevent", "sql":
		return text(row["sql"]) != "" && text(row["error"]) == ""
	case "llm":
		usage, ok := row["usage"].(map[string]any)
		return ok && row["phase"] == "response" && text(row["error"]) == "" && text(row["response"]) != "" &&
			number(usage["inputTokens"]) > 0 && number(usage["outputTokens"]) > 0 && number(row["cost"]) >= 0 && number(row["cost"]) <= llmBudget
	case "http":
		return number(row["status"]) == 200 && text(row["error"]) == ""
	case "logs":
		return text(row["message"]) != ""
	case "spans":
		return row["status"] == "ok" && text(row["operation"]) != ""
	case "query":
		values, ok := row["row"].(map[string]any)
		return ok && row["provider"] == "prometheus" && values["__name__"] == "demo_requests_total" && number(values["value"]) > 0
	case "process":
		return number(row["pid"]) > 0 && text(row["command"]) != "" && row["tags"] != nil
	default:
		return false
	}
}

func text(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Errorf("encode recorded value: %w", err))
	}
	return string(data)
}

func number(value any) float64 {
	value64, err := strconv.ParseFloat(fmt.Sprint(value), 64)
	if err != nil {
		return math.NaN()
	}
	return value64
}

func captureLoss(values map[string]any) error {
	for key, value := range values {
		switch key {
		case "dropped", "unencodable":
			if number(value) != 0 {
				return fmt.Errorf("%s records: %v", key, value)
			}
		case "truncated":
			if value != nil && text(value) != "false" && text(value) != "[]" && text(value) != "" {
				return fmt.Errorf("truncated records: %v", value)
			}
		}
		if nested, ok := value.(map[string]any); ok {
			if err := captureLoss(nested); err != nil {
				return err
			}
		}
	}
	return nil
}

type report struct {
	RunID         string            `json:"runId"`
	Directory     string            `json:"directory"`
	Coverage      []evidence        `json:"coverage"`
	Errors        []string          `json:"errors"`
	Sessions      []sessionEvidence `json:"sessions"`
	WorkloadError string            `json:"workloadError,omitempty"`
}

type verification struct {
	DataDir, RunID string
	Failure        error
	Sessions       []sessionEvidence
}

func verifyStored(ctx context.Context, v verification) error {
	dataDir, runID := v.DataDir, v.RunID
	info, err := telltale.Get(telltale.GetOptions{DataDir: dataDir, RunID: runID})
	if err != nil {
		return err
	}
	r := report{RunID: runID, Directory: info.Dir, Errors: []string{}, Sessions: v.Sessions}
	if v.Sessions == nil {
		previous, err := os.ReadFile(filepath.Join(info.Dir, "demo-report.json"))
		if err != nil {
			return fmt.Errorf("read original demo evidence: %w", err)
		}
		var original report
		if err := json.Unmarshal(previous, &original); err != nil {
			return err
		}
		r.WorkloadError, r.Sessions = original.WorkloadError, original.Sessions
	}
	if v.Failure != nil {
		r.WorkloadError = v.Failure.Error()
	}
	if r.WorkloadError != "" {
		r.Errors = append(r.Errors, r.WorkloadError)
	}
	if err := validateSessions(r.Sessions); err != nil {
		r.Errors = append(r.Errors, err.Error())
	}
	rows := map[string][]query.Row{}
	var kinds []string
	for _, registration := range traces.Registrations() {
		kinds = append(kinds, registration.Name)
		selected, err := read.Resolve(registration.Name, read.Scope{DataDir: dataDir, Run: runID})
		if err != nil {
			r.Errors = append(r.Errors, err.Error())
			continue
		}
		for row, readErr := range read.Rows(ctx, selected.Refs, read.Query{All: true}) {
			if readErr != nil {
				r.Errors = append(r.Errors, readErr.Error())
				break
			}
			rows[registration.Name] = append(rows[registration.Name], row)
		}
		if err := selected.Close(); err != nil {
			r.Errors = append(r.Errors, err.Error())
		}
	}
	r.Coverage, err = validateCoverage(kinds, rows)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
	}
	if info.Error != "" {
		r.Errors = append(r.Errors, info.Error)
	}
	r.Errors = append(r.Errors, info.CheckpointErrors...)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(info.Dir, "demo-report.json"), append(data, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Println(string(data))
	if len(r.Errors) != 0 {
		return fmt.Errorf("demo run %s failed verification; see %s/demo-report.json", runID, info.Dir)
	}
	return nil
}
