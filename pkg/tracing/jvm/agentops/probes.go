package agentops

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/clicky/api"
)

const (
	// InstrumentOp weaves a method probe into a class under a restore lease.
	InstrumentOp = "jvm.classes.instrument"
	// ProbesListOp lists the JVM's open method probes.
	ProbesListOp = "jvm.probes.list"
	// ProbesFreezeOp stops a probe recording while keeping its buffer readable.
	ProbesFreezeOp = "jvm.probes.freeze"
)

// Instrumented is the class a probe was woven into and the methods it matched.
// LeaseExpiresAt is epoch milliseconds, 0 when the class holds no lease.
type Instrumented struct {
	Class          string               `json:"class"`
	Loader         string               `json:"loader"`
	Digest         string               `json:"digest"`
	OriginalDigest string               `json:"originalDigest"`
	HandedDigest   string               `json:"handedDigest"`
	Patched        bool                 `json:"patched"`
	LeaseExpiresAt int64                `json:"leaseExpiresAt"`
	Methods        []InstrumentedMethod `json:"methods"`
	ProbeKey       string               `json:"probeKey"`
	Generation     int64                `json:"generation"`
	Capacity       int                  `json:"capacity"`
	Alloc          bool                 `json:"alloc"`
	GC             bool                 `json:"gc"`
	Instrumented   bool                 `json:"instrumented"`
}

// InstrumentedMethod is one method the selector matched; Skipped says why one
// could not be instrumented.
type InstrumentedMethod struct {
	Name         string `json:"name"`
	Descriptor   string `json:"descriptor"`
	Instrumented bool   `json:"instrumented"`
	Skipped      string `json:"skipped,omitempty"`
}

// DecodeInstrument decodes the agent's instrument result.
func DecodeInstrument(raw json.RawMessage) (Instrumented, error) {
	var result Instrumented
	if err := decode(InstrumentOp, raw, &result, "class", "probeKey", "methods", "instrumented"); err != nil {
		return result, err
	}
	return result, nil
}

// Pretty is the instrument acknowledged on one line.
// Failure is why no method was instrumented, for a command that must exit non-zero.
func (r Instrumented) Failure() error {
	if r.Instrumented {
		return nil
	}
	reasons := []string{}
	for _, method := range r.Methods {
		if method.Skipped != "" {
			reasons = append(reasons, method.Name+method.Descriptor+" skipped: "+method.Skipped)
		}
	}
	if len(reasons) == 0 {
		return fmt.Errorf("%s was not instrumented", r.Class)
	}
	return fmt.Errorf("%s was not instrumented: %s", r.Class, strings.Join(reasons, "; "))
}

func (r Instrumented) Pretty() api.Text {
	var armed, skipped []string
	for _, method := range r.Methods {
		name := shortType(r.Class) + "." + method.Name + method.Descriptor
		if method.Instrumented {
			armed = append(armed, name)
		} else {
			skipped = append(skipped, name+": "+method.Skipped)
		}
	}
	if !r.Instrumented {
		return api.Text{}.Append(fmt.Sprintf("✗ %s was not instrumented: the rewrite changed nothing · probe %s", r.Class, r.ProbeKey), "text-red-600")
	}
	facts := []string{"probe " + r.ProbeKey}
	if r.LeaseExpiresAt > 0 {
		facts = append(facts, "lease until "+time.UnixMilli(r.LeaseExpiresAt).Local().Format("15:04:05"))
	}
	facts = append(facts, fmt.Sprintf("ring %d", r.Capacity))
	if r.Alloc {
		facts = append(facts, "alloc")
	}
	if r.GC {
		facts = append(facts, "gc")
	}
	line := api.Text{}.Append("✓ instrumented ", "text-green-600").Append(strings.Join(armed, ", "), "font-bold").
		Append(" · "+strings.Join(facts, " · "), "text-muted")
	if len(skipped) > 0 {
		line = line.Append(" · skipped "+strings.Join(skipped, ", "), "text-yellow-600")
	}
	return line
}

// Probes are the JVM's open method probes.
type Probes struct {
	Keys []ProbeKey `json:"keys"`
}

// ProbeKey is one open probe. Owners are "loader|class"; WriteSeq-OldestSeq
// events are buffered. Armings are the options each owner armed it with.
type ProbeKey struct {
	Key        string           `json:"key"`
	Owners     []string         `json:"owners"`
	Generation int64            `json:"generation"`
	Capacity   int              `json:"capacity"`
	WriteSeq   int64            `json:"writeSeq"`
	OldestSeq  int64            `json:"oldestSeq"`
	Degraded   bool             `json:"degraded"`
	Armings    []map[string]any `json:"armings,omitempty"`
}

// DecodeProbes decodes the agent's probe list.
func DecodeProbes(raw json.RawMessage) (Probes, error) {
	var result Probes
	if err := decode(ProbesListOp, raw, &result); err != nil {
		return result, err
	}
	if result.Keys == nil {
		return result, fmt.Errorf("%s returned no keys: %s", ProbesListOp, raw)
	}
	return result, nil
}

func (ProbeKey) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("key").Label("Key").Build(),
		api.Column("class").Label("Class").Build(),
		api.Column("method").Label("Method").Build(),
		api.Column("events").Label("Events").Build(),
		api.Column("capacity").Label("Ring").Build(),
		api.Column("degraded").Label("").Build(),
	}
}

func (p ProbeKey) Row() map[string]any {
	var classes, methods []string
	for _, owner := range p.Owners {
		_, class, _ := strings.Cut(owner, "|")
		classes = append(classes, shortType(class))
	}
	for _, arming := range p.Armings {
		if method, ok := arming["method"].(string); ok {
			methods = append(methods, method)
		}
	}
	degraded := ""
	if p.Degraded {
		degraded = "degraded"
	}
	return map[string]any{
		"key":      p.Key,
		"class":    strings.Join(classes, ", "),
		"method":   strings.Join(methods, ", "),
		"events":   p.WriteSeq - p.OldestSeq,
		"capacity": p.Capacity,
		"degraded": degraded,
	}
}

// Pretty is the open probes under a heading, or one muted line when none is open.
func (p Probes) Pretty() api.Text {
	if len(p.Keys) == 0 {
		return api.Text{}.Append("no probes", "text-muted")
	}
	return api.Text{}.Append("Probes", "font-bold").Append(fmt.Sprintf(" %d open", len(p.Keys)), "text-muted").
		NewLine().Add(api.NewTableFrom(p.Keys))
}

// Frozen is a probe that stopped recording; Changed is false when it already had.
type Frozen struct {
	Key        string `json:"key"`
	Frozen     bool   `json:"frozen"`
	Changed    bool   `json:"changed"`
	Generation int64  `json:"generation"`
	WriteSeq   int64  `json:"writeSeq"`
	Capacity   int    `json:"capacity"`
}

// DecodeFreeze decodes the agent's freeze result.
func DecodeFreeze(raw json.RawMessage) (Frozen, error) {
	var result Frozen
	if err := decode(ProbesFreezeOp, raw, &result, "key", "frozen", "changed"); err != nil {
		return result, err
	}
	if !result.Frozen {
		return result, fmt.Errorf("%s did not freeze probe %s: %s", ProbesFreezeOp, result.Key, raw)
	}
	return result, nil
}

// Pretty is the freeze acknowledged on one line.
func (f Frozen) Pretty() api.Text {
	head := "✓ froze probe " + f.Key
	if !f.Changed {
		head = "✓ probe " + f.Key + " was already frozen"
	}
	return api.Text{}.Append(head, "text-green-600").Append(" · "+plural(f.WriteSeq, "event", "events")+" written", "text-muted")
}
