package httpdecode

import (
	"bytes"
	"encoding/json"
	"net/http"
)

type AI struct {
	Model        string `json:"model,omitempty"`
	ResponseID   string `json:"responseId,omitempty"`
	FinishReason string `json:"finishReason,omitempty"`
	Status       string `json:"status,omitempty"`
	Stream       bool   `json:"stream,omitempty"`
	Usage        *Usage `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens       *int64 `json:"inputTokens,omitempty"`
	OutputTokens      *int64 `json:"outputTokens,omitempty"`
	TotalTokens       *int64 `json:"totalTokens,omitempty"`
	CachedInputTokens *int64 `json:"cachedInputTokens,omitempty"`
	CacheWriteTokens  *int64 `json:"cacheWriteTokens,omitempty"`
}

type wireCount struct{ value *int64 }

func (c *wireCount) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		if masked(text) {
			return nil
		}
	}
	return json.Unmarshal(data, &c.value)
}

type wireUsage struct {
	Input        wireCount `json:"input_tokens"`
	Output       wireCount `json:"output_tokens"`
	Prompt       wireCount `json:"prompt_tokens"`
	Completion   wireCount `json:"completion_tokens"`
	Total        wireCount `json:"total_tokens"`
	CacheRead    wireCount `json:"cache_read_input_tokens"`
	CacheWrite   wireCount `json:"cache_creation_input_tokens"`
	InputDetails struct {
		Cached wireCount `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	PromptDetails struct {
		Cached wireCount `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type aiPayload struct {
	ID         string    `json:"id"`
	Model      string    `json:"model"`
	StopReason string    `json:"stop_reason"`
	Status     string    `json:"status"`
	Input      wireCount `json:"input_tokens"`
	Usage      wireUsage `json:"usage"`
	Choices    []struct {
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error struct {
		Code string `json:"code"`
		Type string `json:"type"`
	} `json:"error"`
}

func decodeAI(e exchange) Result {
	if e.req.Method != http.MethodPost {
		return Result{}
	}
	var result Result
	switch e.host + e.req.URL.EscapedPath() {
	case "api.anthropic.com/v1/messages":
		result.Protocol, result.Operation = "anthropic", "messages"
	case "api.anthropic.com/v1/messages/count_tokens":
		result.Protocol, result.Operation = "anthropic", "count_tokens"
	case "api.openai.com/v1/responses":
		result.Protocol, result.Operation = "openai", "responses"
	case "api.openai.com/v1/chat/completions":
		result.Protocol, result.Operation = "openai", "chat_completions"
	default:
		return Result{}
	}
	result.Details = &Details{AI: &AI{},
		RequestID: first(observed(e.resp.Header.Get("X-Request-Id")), observed(e.resp.Header.Get("Request-Id")))}
	decodeAIExchange(e, result)
	return result
}

func decodeAIExchange(e exchange, result Result) {
	d := result.Details
	var request struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if e.request.isJSON() && e.request.json(d, &request) {
		d.AI.Model, d.AI.Stream = request.Model, request.Stream
	}
	if e.response.media == "text/event-stream" {
		d.AI.Stream = true
		data := e.response.bytes(d)
		if len(data) > 0 {
			terminal := decodeEventStream(data, result)
			d.Partial = d.Partial || e.response.truncated || e.options.StreamOpen || !terminal
		}
		return
	}
	var response aiPayload
	if e.response.isJSON() && e.response.json(d, &response) {
		if result.Operation == "count_tokens" {
			response.Usage.Input = response.Input
		}
		d.applyAI(response)
	}
}

func (d *Details) applyAI(p aiPayload) {
	a := d.AI
	a.Model, a.ResponseID = first(p.Model, a.Model), first(p.ID, a.ResponseID)
	a.Status, a.FinishReason = first(p.Status, a.Status), first(p.StopReason, a.FinishReason)
	for _, choice := range p.Choices {
		if choice.Index == 0 && choice.FinishReason != "" {
			a.FinishReason = choice.FinishReason
		}
	}
	d.ErrorCode = first(p.Error.Code, p.Error.Type, d.ErrorCode)
	usage := a.Usage
	if usage == nil {
		usage = &Usage{}
	}
	u := p.Usage
	usage.InputTokens = latest(u.Input.value, u.Prompt.value, usage.InputTokens)
	usage.OutputTokens = latest(u.Output.value, u.Completion.value, usage.OutputTokens)
	usage.TotalTokens = latest(u.Total.value, usage.TotalTokens)
	usage.CachedInputTokens = latest(u.CacheRead.value, u.InputDetails.Cached.value, u.PromptDetails.Cached.value, usage.CachedInputTokens)
	usage.CacheWriteTokens = latest(u.CacheWrite.value, usage.CacheWriteTokens)
	if *usage != (Usage{}) {
		a.Usage = usage
	}
}

func latest(values ...*int64) *int64 {
	for _, value := range values {
		if value != nil && *value >= 0 {
			return value
		}
	}
	return nil
}

func decodeEventStream(data []byte, result Result) bool {
	var payload []byte
	terminal := false
	for len(data) > 0 {
		end := bytes.IndexAny(data, "\r\n")
		if end < 0 {
			break
		}
		line := data[:end]
		width := 1
		if data[end] == '\r' && end+1 < len(data) && data[end+1] == '\n' {
			width++
		}
		data = data[end+width:]
		if len(line) == 0 {
			if len(payload) > 0 {
				terminal = applyAIEvent(payload[:len(payload)-1], result) || terminal
				payload = payload[:0]
			}
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		if bytes.Equal(field, []byte("data")) {
			value = bytes.TrimPrefix(value, []byte(" "))
			payload = append(append(payload, value...), '\n')
		}
	}
	return terminal
}

func applyAIEvent(data []byte, result Result) bool {
	if result.Operation == "chat_completions" && bytes.Equal(data, []byte("[DONE]")) {
		return true
	}
	var event struct {
		aiPayload
		Type     string    `json:"type"`
		Code     string    `json:"code"`
		Message  aiPayload `json:"message"`
		Delta    aiPayload `json:"delta"`
		Response aiPayload `json:"response"`
	}
	d := result.Details
	if err := json.Unmarshal(data, &event); err != nil {
		d.issue("invalid captured event data")
		return false
	}
	switch event.Type {
	case "message_start":
		d.applyAI(event.Message)
	case "message_delta":
		d.applyAI(event.Delta)
		d.applyAI(event.aiPayload)
	case "message_stop":
		return true
	case "response.created", "response.in_progress":
		d.applyAI(event.Response)
	case "response.completed", "response.failed", "response.incomplete":
		d.applyAI(event.Response)
		return true
	case "error":
		d.applyAI(event.aiPayload)
		d.ErrorCode = first(event.Code, d.ErrorCode, "stream-error")
		return true
	case "":
		if result.Operation == "chat_completions" {
			d.applyAI(event.aiPayload)
		}
	}
	return false
}
