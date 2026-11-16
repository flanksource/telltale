package tracing

import (
	"strings"
	"time"

	"github.com/flanksource/clicky/api"
)

func SeverityStyle(level string) string {
	switch strings.ToLower(level) {
	case "error", "fatal", "panic", "failed", "down":
		return "text-red-500 font-bold"
	case "warn", "warning":
		return "text-yellow-500 font-bold"
	case "ok", "up", "info":
		return "text-green-500"
	default:
		return "text-muted"
	}
}

func PrettySampleValue(value int64, unit string) api.Text {
	switch unit {
	case "bytes":
		return api.HumanizeBytes(value).Styles("text-blue-500")
	case "ns", "nanoseconds":
		return api.Human(time.Duration(value), "text-yellow-500")
	default:
		return api.HumanNumber(value, "text-blue-500").Space().Append(unit, "text-muted")
	}
}
