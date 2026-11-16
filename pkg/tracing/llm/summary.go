package llm

import "iter"

func (capture) Summarize(rows iter.Seq[LLMCall]) (map[string]any, error) {
	count, failed := 0, 0
	duration, cost := 0.0, 0.0
	models := map[string]int{}
	input, output := 0, 0
	for row := range rows {
		count++
		duration += row.DurationMs
		cost += row.Cost
		input += row.Usage.InputTokens
		output += row.Usage.OutputTokens
		models[row.Model]++
		if row.Error != "" {
			failed++
		}
	}
	return map[string]any{"calls": count, "failures": failed, "durationMs": duration, "cost": cost, "models": models, "inputTokens": input, "outputTokens": output}, nil
}
