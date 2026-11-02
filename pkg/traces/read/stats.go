package read

import (
	"encoding/json"
	"fmt"
	"iter"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"

	"github.com/flanksource/commons-db/query"
)

const ReservoirSize = 8192
const MaxGroups = 1024
const MaxColumns = 64

type metric struct {
	count         int64
	min, max, avg float64
	values        []float64
}

func (m *metric) add(value float64, rng *rand.Rand) {
	m.count++
	if m.count == 1 {
		m.min = value
		m.max = value
		m.values = make([]float64, 0, ReservoirSize)
	}
	m.min = math.Min(m.min, value)
	m.max = math.Max(m.max, value)
	m.avg += (value - m.avg) / float64(m.count)
	if len(m.values) < ReservoirSize {
		m.values = append(m.values, value)
	} else if index := rng.Int64N(m.count); index < ReservoirSize {
		m.values[index] = value
	}
}

func (m *metric) row() query.Row {
	row := query.Row{"count": m.count, "min": nil, "avg": nil, "max": nil, "p50": nil, "p90": nil, "p95": nil, "p99": nil, "approximate": m.count > ReservoirSize}
	if m.count == 0 {
		return row
	}
	row["min"], row["avg"], row["max"] = m.min, m.avg, m.max
	slices.Sort(m.values)
	for _, p := range []int{50, 90, 95, 99} {
		row[fmt.Sprintf("p%d", p)] = m.values[int(math.Ceil(float64(p)*float64(len(m.values))/100))-1]
	}
	return row
}

type statsGroup struct {
	value   any
	metrics []metric
	count   int64
}

// Stats keeps exact counts/extrema/averages and nearest-rank percentiles over
// an 8192-value reservoir per column/group (exact below that). At most 1024
// groups, 64 columns and 1024 column/group pairs are accepted (64 MiB of
// reservoir values); exceeding a bound is an error.
// Nulls are skipped; missing columns and nonnumeric values are errors.
func Stats(rows iter.Seq2[query.Row, error], cols []string, groupBy string) ([]query.Row, error) {
	if len(cols) > MaxColumns {
		return nil, fmt.Errorf("stats supports at most %d columns", MaxColumns)
	}
	groups := map[string]*statsGroup{}
	var order []string
	rng := rand.New(rand.NewPCG(1, 2))
	for row, err := range rows {
		if err != nil {
			return nil, err
		}
		value, exists := row[groupBy]
		if groupBy != "" && !exists {
			return nil, fmt.Errorf("group-by column %q is absent", groupBy)
		}
		keyBytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("group-by %q: %w", groupBy, err)
		}
		key := string(keyBytes)
		group := groups[key]
		if group == nil {
			if len(groups) == MaxGroups || (len(groups)+1)*len(cols) > 1024 {
				return nil, fmt.Errorf("stats exceeds %d groups or 1024 column/group pairs", MaxGroups)
			}
			group = &statsGroup{value: value, metrics: make([]metric, len(cols))}
			groups[key] = group
			order = append(order, key)
		}
		group.count++
		for i, col := range cols {
			raw, exists := row[col]
			if !exists {
				return nil, fmt.Errorf("stats column %q is absent", col)
			}
			if raw == nil {
				continue
			}
			number, err := numeric(raw)
			if err != nil {
				return nil, fmt.Errorf("stats column %s: %w", col, err)
			}
			group.metrics[i].add(number, rng)
		}
	}
	result := []query.Row{}
	for _, key := range order {
		group := groups[key]
		if len(cols) == 0 {
			result = append(result, query.Row{groupBy: group.value, "count": group.count})
			continue
		}
		for i, col := range cols {
			row := group.metrics[i].row()
			row["column"] = col
			if groupBy != "" {
				row[groupBy] = group.value
			}
			result = append(result, row)
		}
	}
	return result, nil
}

func numeric(value any) (float64, error) {
	var number float64
	if n, ok := value.(json.Number); ok {
		var err error
		number, err = n.Float64()
		if err != nil {
			return 0, err
		}
	} else {
		r := reflect.ValueOf(value)
		switch r.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			number = float64(r.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			number = float64(r.Uint())
		case reflect.Float32, reflect.Float64:
			number = r.Float()
		default:
			return 0, fmt.Errorf("expected number, got %T", value)
		}
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("nonfinite number %v", number)
	}
	return number, nil
}
