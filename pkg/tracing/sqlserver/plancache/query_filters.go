package plancache

import (
	"fmt"
	"strings"

	"github.com/flanksource/clicky"
	"gorm.io/gorm"
)

func normalizePatterns(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func validatePatterns(values []string) error {
	for _, pattern := range normalizePatterns(values) {
		if pattern == "!" {
			return fmt.Errorf("pattern %q excludes no value", pattern)
		}
	}
	return nil
}

func textFilterPatterns(values []string) []string {
	patterns := normalizePatterns(values)
	for i, pattern := range patterns {
		value := strings.TrimPrefix(pattern, "!")
		if !strings.Contains(value, "*") {
			patterns[i] = strings.Replace(pattern, value, "*"+value+"*", 1)
		}
	}
	return patterns
}

func whereMatchItems(db *gorm.DB, column string, values []string) *gorm.DB {
	var positives, negatives []string
	var positiveArgs, negativeArgs []any
	matchAll := false
	for _, pattern := range normalizePatterns(values) {
		exclude := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		if pattern == "*" {
			if exclude {
				return db.Where("1 = 0")
			}
			matchAll = true
			continue
		}
		clause, arg := matchItemSQL(column, pattern, exclude)
		if exclude {
			negatives = append(negatives, clause)
			negativeArgs = append(negativeArgs, arg)
		} else {
			positives = append(positives, clause)
			positiveArgs = append(positiveArgs, arg)
		}
	}
	if !matchAll && len(positives) > 0 {
		db = db.Where("("+strings.Join(positives, " OR ")+")", positiveArgs...)
	}
	if len(negatives) > 0 {
		db = db.Where(strings.Join(negatives, " AND "), negativeArgs...)
	}
	return db
}

func matchItemSQL(column, pattern string, exclude bool) (string, string) {
	normalized := "LOWER(" + column + ")"
	pattern = strings.ToLower(pattern)
	wildcard := strings.HasPrefix(pattern, "*") || strings.HasSuffix(pattern, "*")
	operator := "="
	if wildcard {
		operator = "LIKE"
		value := strings.TrimSuffix(strings.TrimPrefix(pattern, "*"), "*")
		value = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`, "[", `\[`).Replace(value)
		if strings.HasPrefix(pattern, "*") {
			value = "%" + value
		}
		if strings.HasSuffix(pattern, "*") {
			value += "%"
		}
		pattern = value
	}
	if exclude {
		if wildcard {
			operator = "NOT LIKE"
		} else {
			operator = "<>"
		}
	}
	clause := normalized + " " + operator + " ?"
	if wildcard {
		clause += ` ESCAPE '\'`
	}
	if exclude {
		clause = "(" + column + " IS NULL OR " + clause + ")"
	}
	return clause, pattern
}

type sortSpec struct {
	Default     clicky.SortOptions
	Columns     map[string]string
	TieBreakers []string
}

func (s sortSpec) Apply(db *gorm.DB, options clicky.SortOptions) (*gorm.DB, error) {
	if options.Key == "" {
		if options.Direction != "" {
			return nil, fmt.Errorf("sort direction requires a key")
		}
		options = s.Default
	}
	expression, ok := s.Columns[options.Key]
	if !ok {
		return nil, fmt.Errorf("unsupported sort key %q", options.Key)
	}
	if options.Direction == "" {
		options.Direction = clicky.SortDirectionAsc
	}
	if options.Direction != clicky.SortDirectionAsc && options.Direction != clicky.SortDirectionDesc {
		return nil, fmt.Errorf("unsupported sort direction %q", options.Direction)
	}
	db = db.Order(expression + " " + strings.ToUpper(string(options.Direction)))
	for _, column := range s.TieBreakers {
		db = db.Order(column + " ASC")
	}
	return db, nil
}
