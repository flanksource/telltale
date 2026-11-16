package jmx

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/clicky/api"
)

// Loggers are the java.util.logging loggers in name order. The root logger is named ""
// and has no Parent; a nil Level inherits the parent's.
type Loggers []Logger

type Logger struct {
	Name   string  `json:"name"`
	Level  *string `json:"level"`
	Parent *string `json:"parent"`
}

func (l *Logger) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Name   *string `json:"name"`
		Level  *string `json:"level"`
		Parent *string `json:"parent"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	if wire.Name == nil {
		return fmt.Errorf("logger without a name: %s", raw)
	}
	*l = Logger{Name: *wire.Name, Level: wire.Level, Parent: wire.Parent}
	return nil
}

func DecodeLoggers(raw json.RawMessage) (Loggers, error) {
	var loggers Loggers
	if err := json.Unmarshal(raw, &loggers); err != nil {
		return nil, fmt.Errorf("decode %s result: %w", LoggingListOp, err)
	}
	for _, logger := range loggers {
		if logger.Name == "" && logger.Parent == nil {
			return loggers, nil
		}
	}
	return nil, fmt.Errorf("%s returned no root logger: %s", LoggingListOp, raw)
}

// LoggerRow is one logger with the level it actually logs at; Level is empty when inherited.
type LoggerRow struct {
	Logger    string `json:"logger"`
	Level     string `json:"level"`
	Effective string `json:"effective"`
}

// Rows resolve each logger's effective level through its listed parents.
func (l Loggers) Rows() []LoggerRow {
	byName := make(map[string]Logger, len(l))
	for _, logger := range l {
		byName[logger.Name] = logger
	}
	rows := make([]LoggerRow, len(l))
	for i, logger := range l {
		rows[i] = LoggerRow{Logger: logger.Name, Level: deref(logger.Level), Effective: effectiveLevel(byName, logger)}
		if logger.Name == "" {
			rows[i].Logger = "(root)"
		}
	}
	return rows
}

func effectiveLevel(byName map[string]Logger, logger Logger) string {
	for range len(byName) {
		if logger.Level != nil {
			return *logger.Level
		}
		if logger.Parent == nil {
			return ""
		}
		parent, listed := byName[*logger.Parent]
		if !listed {
			return ""
		}
		logger = parent
	}
	return ""
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (LoggerRow) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("logger").Label("Logger").MaxWidth(100).Build(),
		api.Column("level").Label("Level").Build(),
		api.Column("effective").Label("Effective").Build(),
	}
}

func (r LoggerRow) Row() map[string]any {
	level := api.Text{}.Append(r.Level)
	if r.Level == "" {
		level = api.Text{}.Append("inherit", "text-muted")
	}
	effective := api.Text{}.Append(r.Effective)
	if r.Effective == "" {
		effective = api.Text{}.Append("unknown", "text-yellow-600")
	}
	return map[string]any{"logger": r.Logger, "level": level, "effective": effective}
}

func (l Loggers) Pretty() api.Text {
	explicit := 0
	for _, logger := range l {
		if logger.Level != nil {
			explicit++
		}
	}
	return api.Text{}.Append("Loggers", "font-bold").
		Append(fmt.Sprintf(" %d · %d with explicit levels", len(l), explicit), "text-muted").
		NewLine().Add(api.NewTableFrom(l.Rows()))
}
