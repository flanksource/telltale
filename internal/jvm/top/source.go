package top

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
)

type Source struct {
	mu     sync.Mutex
	Agent  string
	Server string
	local  *agentclient.Agent
}

func (s *Source) Next(ctx context.Context) (Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Frame{}, err
	}
	if s.Server != "" {
		var frame Frame
		err := s.remote(ctx, "/api/v1/jvm/top", map[string]any{"agent": s.Agent, "once": true, "format": "json"}, &frame, true)
		if err == nil && (frame.AgentID == "" || frame.SampledAt.IsZero() || frame.Threads.Threads == nil) {
			err = fmt.Errorf("server returned an incomplete JVM Top frame")
		}
		if err == nil {
			s.Agent = frame.AgentID
		}
		return frame, err
	}
	if s.local == nil {
		agent, err := agentclient.Default.Lookup(ctx, s.Agent, agentclient.Want{Runtime: "java"})
		if err != nil {
			return Frame{}, err
		}
		if agent.Hello.Runtime.Lang != "java" {
			return Frame{}, fmt.Errorf("agent %q is not Java", agent.Hello.AgentID)
		}
		s.local, s.Agent = agent, agent.Hello.AgentID
	}
	return Sample(ctx, s.Agent, s.local.Call)
}

func (s *Source) Stack(ctx context.Context, id int64) (jvmruntime.ThreadStack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if id <= 0 {
		return jvmruntime.ThreadStack{}, fmt.Errorf("thread ID must be positive")
	}
	if s.Server != "" {
		var stack jvmruntime.ThreadStack
		err := s.remote(ctx, "/api/agents/"+url.PathEscape(s.Agent)+"/call", map[string]any{"operation": "jvm.threads.stack", "params": map[string]any{"id": id}}, &stack, false)
		return stack, err
	}
	if s.local == nil {
		return jvmruntime.ThreadStack{}, fmt.Errorf("sample JVM before inspecting a stack")
	}
	raw, err := s.local.Call(ctx, agentclient.CallRequest{Op: jvmruntime.ThreadStackOp, Params: json.RawMessage(fmt.Sprintf(`{"id":%d}`, id))})
	if err != nil {
		return jvmruntime.ThreadStack{}, err
	}
	return jvmruntime.DecodeThreadStack(raw)
}

func (s *Source) remote(ctx context.Context, path string, body any, result any, command bool) (err error) {
	address, err := url.Parse(s.Server)
	if err != nil || address.Host == "" || (address.Scheme != "http" && address.Scheme != "https") || address.User != nil || address.RawQuery != "" || address.Fragment != "" {
		return fmt.Errorf("invalid server URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.Server, "/")+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("TELLTALE_API_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	data, err = io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("JVM Top HTTP %d: %s", response.StatusCode, data)
	}
	if command {
		var envelope struct {
			Success bool   `json:"success"`
			Output  string `json:"output"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return err
		}
		if !envelope.Success {
			return fmt.Errorf("JVM Top: %s", envelope.Error)
		}
		data = []byte(envelope.Output)
	}
	return json.Unmarshal(data, result)
}
