package plancache

import (
	"fmt"

	dbquery "github.com/flanksource/commons-db/query"
	"gorm.io/gorm"
)

type planRangeFilter struct {
	value       string
	expression  string
	kind        dbquery.ColumnFilterKind
	scale       float64
	includeNull bool
}

func applyPlanRange(db *gorm.DB, selection planRangeFilter) (*gorm.DB, error) {
	if selection.value == "" {
		return db, nil
	}
	parsed, err := (dbquery.ColumnFilterBinding{
		Kind: selection.kind, Unit: "ms", DefaultOperator: ">",
	}).ParseSelection(selection.value)
	if err != nil {
		return nil, fmt.Errorf("%s filter: %w", selection.expression, err)
	}
	if parsed.Range == nil {
		return nil, fmt.Errorf("%s filter: a comparison is required", selection.expression)
	}
	for _, edge := range []struct {
		bound *dbquery.FilterBound
		lower bool
	}{{parsed.Range.Min, true}, {parsed.Range.Max, false}} {
		if edge.bound == nil {
			continue
		}
		operator := ">"
		if !edge.lower {
			operator = "<"
		}
		if edge.bound.Inclusive {
			operator += "="
		}
		predicate := selection.expression + " " + operator + " ?"
		if selection.includeNull && edge.lower {
			predicate = "(qs.last_execution_time IS NULL OR " + predicate + ")"
		}
		db = db.Where(predicate, edge.bound.Value.(float64)*selection.scale)
	}
	return db, nil
}
