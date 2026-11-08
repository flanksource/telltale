package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/captain/pkg/ai"
	"github.com/flanksource/captain/pkg/ai/observe"
	"github.com/flanksource/captain/pkg/ai/pricing"
	_ "github.com/flanksource/captain/pkg/ai/provider"
	"github.com/flanksource/captain/pkg/api"
)

const llmBudget = 0.05
const outputTokens = 128
const demoPrompt = "Reply with one short sentence confirming that the acme checkout demonstration is ready."

func prepareLLM(model string) (ai.Provider, error) {
	provider, err := ai.NewProvider(ai.Config{Model: api.Model{Name: model}, Budget: api.Budget{Cost: llmBudget, MaxTokens: outputTokens}, NoCache: true})
	if err != nil {
		return nil, err
	}
	if provider.GetRuntime().Mode != ai.ModeAPI {
		return nil, fmt.Errorf("demo requires an API runtime with a bounded output token limit")
	}
	pricing.EnsureLoaded(pricing.LoadOptions{})
	price, err := pricing.CalculateCost(provider.GetModel(), 1024, outputTokens, outputTokens, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("demo needs known model pricing: %w", err)
	}
	if price.TotalCost > llmBudget {
		return nil, fmt.Errorf("maximum demo request price $%.4f exceeds $%.2f", price.TotalCost, llmBudget)
	}
	return observe.Wrap(provider), nil
}

func recordLLM(ctx context.Context, provider ai.Provider) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	response, err := provider.Execute(ctx, ai.Request{Prompt: api.Prompt{User: demoPrompt}, Budget: api.Budget{Cost: llmBudget, MaxTokens: outputTokens, MaxTurns: 1}, Model: api.Model{Name: provider.GetModel(), NoCache: true}})
	if err != nil {
		return err
	}
	if response == nil || strings.TrimSpace(response.Text) == "" {
		return fmt.Errorf("LLM returned no text")
	}
	if response.Usage.TotalTokens() == 0 {
		return fmt.Errorf("LLM returned no measured token usage")
	}
	if response.CostUSD > llmBudget {
		return fmt.Errorf("LLM response cost $%.4f exceeded $%.2f", response.CostUSD, llmBudget)
	}
	return nil
}
