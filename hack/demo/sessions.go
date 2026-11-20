package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/flanksource/commons-db/query"
)

type sessionEvidence struct {
	Kind    string             `json:"kind"`
	State   query.SessionState `json:"state"`
	Error   string             `json:"error,omitempty"`
	Warning string             `json:"warning,omitempty"`
	Summary json.RawMessage    `json:"summary"`
}

func captureSessions(infos []query.SessionInfo) []sessionEvidence {
	result := make([]sessionEvidence, 0, len(infos))
	for _, info := range infos {
		result = append(result, sessionEvidence{Kind: strings.TrimPrefix(info.Profile, "traces/"), State: info.State, Error: info.Error, Warning: info.Warning, Summary: info.Summary})
	}
	return result
}

func validateSessions(sessions []sessionEvidence) error {
	if len(sessions) == 0 {
		return fmt.Errorf("no final session evidence recorded")
	}
	var failures []error
	for _, session := range sessions {
		if session.State != query.SessionStopped && session.State != query.SessionCompleted {
			failures = append(failures, fmt.Errorf("%s session ended %s: %s", session.Kind, session.State, session.Error))
		}
		if session.Warning != "" || session.Error != "" {
			failures = append(failures, fmt.Errorf("%s session: %s %s", session.Kind, session.Error, session.Warning))
		}
		var summary map[string]any
		if err := json.Unmarshal(session.Summary, &summary); err != nil {
			failures = append(failures, fmt.Errorf("%s session summary: %w", session.Kind, err))
		} else if err := captureLoss(summary); err != nil {
			failures = append(failures, fmt.Errorf("%s session: %w", session.Kind, err))
		}
	}
	return errors.Join(failures...)
}
