package server

import (
	"context"
	"net/http"
	"time"

	"github.com/flanksource/clicky/entity"
	"github.com/flanksource/telltale/internal/jvm/top"
	"github.com/flanksource/telltale/internal/livestream"
	"github.com/flanksource/telltale/pkg/agentclient"
)

func jvmTopUpdates() http.HandlerFunc {
	var samples livestream.Sampler[string, top.Frame]
	return func(w http.ResponseWriter, r *http.Request) {
		selector := r.URL.Query().Get("agent")
		if selector == "" {
			http.Error(w, "JVM Top requires agent", http.StatusBadRequest)
			return
		}
		agent, err := agentclient.Default.Lookup(r.Context(), selector, agentclient.Want{Runtime: "java"})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id := agent.Hello.AgentID
		livestream.Serve(w, r, func(ctx context.Context, send entity.StreamSend) error {
			return samples.Follow(ctx, id, func(ctx context.Context) (top.Frame, time.Duration, error) {
				ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				frame, err := top.Sample(ctx, id, agent.Call)
				return frame, 2 * time.Second, err
			}, func(frame top.Frame) error { return send(entity.StreamEvent{Data: frame}) })
		})
	}
}
