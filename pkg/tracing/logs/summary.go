package logs

import "iter"

func (capture) Summarize(rows iter.Seq[Log]) (map[string]any, error) {
	levels := map[string]int{}
	for row := range rows {
		levels[row.Severity]++
	}
	return map[string]any{"levels": levels}, nil
}
