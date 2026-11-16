package classes

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/clicky/api"
)

// Compile is a javac run over replacement source, applied or not. Classes are
// every class file it produced; diagnostics are positioned in the source sent.
type Compile struct {
	Success          bool         `json:"success"`
	Classes          []string     `json:"classes"`
	Diagnostics      []Diagnostic `json:"diagnostics"`
	ClasspathEntries int          `json:"classpathEntries"`
}

// Diagnostic is one javac message. Line and Column are -1 when it fell in the
// generated stand-in rather than the method the caller sent.
type Diagnostic struct {
	Kind    string `json:"kind"`
	Line    int64  `json:"line"`
	Column  int64  `json:"column"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func DecodeCompile(raw json.RawMessage) (Compile, error) {
	var compile Compile
	err := decodeObject(CompileOp, raw, &compile, "success", "classes", "diagnostics")
	return compile, err
}

func (c Compile) Pretty() api.Text {
	if !c.Success {
		return c.failure("✗ ")
	}
	text := api.Text{}.Append("✓ compiled "+strings.Join(c.Classes, ", "), "text-green-600").
		Append(" · "+classpath(c.ClasspathEntries), "text-muted")
	return c.withDiagnostics(text)
}

// Failure is why the compile failed, for a command that must exit non-zero.
func (c Compile) Failure() error {
	if c.Success {
		return nil
	}
	return fmt.Errorf("compile failed with %s", plural(c.errors(), "error"))
}

func (c Compile) errors() int {
	errors := 0
	for _, d := range c.Diagnostics {
		if d.Kind == "ERROR" {
			errors++
		}
	}
	return errors
}

func (c Compile) failure(prefix string) api.Text {
	text := api.Text{}.Append(fmt.Sprintf("%scompile failed · %s", prefix, plural(c.errors(), "error")), "text-red-600").
		Append(" · "+classpath(c.ClasspathEntries), "text-muted")
	return c.withDiagnostics(text)
}

func (c Compile) withDiagnostics(text api.Text) api.Text {
	for _, d := range c.Diagnostics {
		text = text.NewLine().Append(d.String(), d.class())
	}
	return text
}

func (d Diagnostic) String() string {
	line := d.Kind + " "
	if d.Line > 0 {
		line += fmt.Sprintf("%d:%d ", d.Line, d.Column)
	}
	line += d.Message
	if d.Code != "" {
		line += " (" + d.Code + ")"
	}
	return line
}

func (d Diagnostic) class() string {
	switch d.Kind {
	case "ERROR":
		return "text-red-600"
	case "WARNING", "MANDATORY_WARNING":
		return "text-yellow-600"
	}
	return "text-muted"
}

// PatchStatus is one class's live bytes against the original held for it.
// LeaseExpiresAt is epoch milliseconds, 0 when nothing will restore it on its
// own; Error says why the class could not be read or was left unchanged.
type PatchStatus struct {
	Class          string `json:"class"`
	Loader         string `json:"loader"`
	Digest         string `json:"digest,omitempty"`
	OriginalDigest string `json:"originalDigest,omitempty"`
	HandedDigest   string `json:"handedDigest,omitempty"`
	Patched        bool   `json:"patched"`
	LeaseExpiresAt int64  `json:"leaseExpiresAt,omitempty"`
	Error          string `json:"error,omitempty"`
}

// Patch is a compile and, when it succeeded, the redefinition it applied.
// Method is the resolved descriptor of a single replaced method.
type Patch struct {
	Compile
	PatchStatus
	Method  string `json:"method,omitempty"`
	Applied bool   `json:"applied"`
}

func DecodePatch(raw json.RawMessage) (Patch, error) {
	var patch Patch
	if err := decodeObject(PatchOp, raw, &patch, "success", "classes", "diagnostics", "applied"); err != nil {
		return patch, err
	}
	if patch.Applied && (patch.Class == "" || patch.Digest == "") {
		return patch, fmt.Errorf("%s reported applied without the class it patched: %s", PatchOp, raw)
	}
	return patch, nil
}

// Failure is why nothing was patched, for a command that must exit non-zero.
func (p Patch) Failure() error {
	target := strings.TrimSpace(p.Class + " " + p.Method)
	switch {
	case !p.Applied && !p.Success:
		return fmt.Errorf("%s was not patched: %w", target, p.Compile.Failure())
	case !p.Applied:
		return fmt.Errorf("%s was not patched", target)
	case p.Error != "":
		return fmt.Errorf("%s: %s", target, p.Error)
	}
	return nil
}

func (p Patch) Pretty() api.Text {
	target := strings.TrimSpace(p.Class + " " + p.Method)
	if !p.Applied {
		return p.failure(strings.TrimSpace("✗ not patched "+target) + ": ")
	}
	if p.Error != "" {
		return api.Text{}.Append("✗ "+p.Error, "text-red-600")
	}
	return api.Text{}.Append("✓ patched "+target, "text-green-600").
		Append(" · loader "+p.Loader+" · ", "text-muted").
		Add(lease(p.LeaseExpiresAt)).
		Append(" · "+shortDigest(p.Digest), "text-muted")
}

// Lease is a patched class's restore deadline after a renewal.
type Lease struct {
	Class          string `json:"class"`
	Loader         string `json:"loader"`
	Patched        bool   `json:"patched"`
	LeaseExpiresAt int64  `json:"leaseExpiresAt"`
}

func DecodeLease(raw json.RawMessage) (Lease, error) {
	var l Lease
	err := decodeObject(LeaseOp, raw, &l, "class", "loader", "patched", "leaseExpiresAt")
	return l, err
}

func (l Lease) Pretty() api.Text {
	if !l.Patched {
		return api.Text{}.Append(l.Class+" is not patched", "text-yellow-600").
			Append(" · loader "+l.Loader, "text-muted")
	}
	return api.Text{}.Append("✓ "+l.Class+" ", "text-green-600").Add(lease(l.LeaseExpiresAt)).
		Append(" · loader "+l.Loader, "text-muted")
}

// Restore is a class put back to its original bytes, or, when Released names
// a probe other probes still need (HeldBy), that probe's claim let go of.
type Restore struct {
	PatchStatus
	Restored     []string         `json:"restored"`
	Released     string           `json:"released,omitempty"`
	HeldBy       []string         `json:"heldBy,omitempty"`
	ProbesClosed []map[string]any `json:"probesClosed"`
}

func DecodeRestore(raw json.RawMessage) (Restore, error) {
	var r Restore
	err := decodeObject(RestoreOp, raw, &r, "class", "loader", "patched", "restored")
	return r, err
}

func (r Restore) Pretty() api.Text {
	if len(r.HeldBy) > 0 {
		return api.Text{}.Append("✓ released probe "+r.Released, "text-green-600").
			Append(" · "+r.Class+" still patched, held by "+strings.Join(r.HeldBy, ", "), "text-muted")
	}
	detail := " · loader " + r.Loader
	if r.Released != "" {
		detail += " · released probe " + r.Released
	}
	var closed []string
	for _, probe := range r.ProbesClosed {
		closed = append(closed, fmt.Sprint(probe["key"]))
	}
	switch len(closed) {
	case 0:
	case 1:
		detail += " · closed probe " + closed[0]
	default:
		detail += " · closed probes " + strings.Join(closed, ", ")
	}
	return api.Text{}.Append("✓ restored "+strings.Join(r.Restored, ", "), "text-green-600").Append(detail, "text-muted")
}

// Patches are the classes holding stashed original bytes, by class name.
type Patches []PatchStatus

// PatchRow is a PatchStatus as a row of the patches table.
type PatchRow PatchStatus

func DecodePatches(raw json.RawMessage) (Patches, error) {
	return decodeRows(PatchesOp, raw, func(p PatchStatus) bool { return p.Class != "" && p.Loader != "" })
}

func (PatchRow) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("class").Label("Class").MaxWidth(100).Build(),
		api.Column("loader").Label("Loader").Build(),
		api.Column("patched").Label("State").Build(),
		api.Column("lease").Label("Lease until").Build(),
		api.Column("digest").Label("Digest").Build(),
		api.Column("error").Label("Error").MaxWidth(80).Build(),
	}
}

func (p PatchRow) Row() map[string]any {
	state, until := "original", "none"
	if p.Patched {
		state = "patched"
	}
	if p.Error != "" {
		state = "error"
	}
	if p.LeaseExpiresAt > 0 {
		until = leaseTime(p.LeaseExpiresAt)
	}
	return map[string]any{"class": p.Class, "loader": p.Loader, "patched": state, "lease": until, "digest": shortDigest(p.Digest), "error": p.Error}
}

func (p Patches) Pretty() api.Text {
	if len(p) == 0 {
		return api.Text{}.Append("no active patches", "text-muted")
	}
	rows := make([]PatchRow, len(p))
	patched := 0
	for i, status := range p {
		rows[i] = PatchRow(status)
		if status.Patched {
			patched++
		}
	}
	return api.Text{}.Append("Patches", "font-bold").
		Append(fmt.Sprintf(" %s · %d patched", plural(len(p), "class"), patched), "text-muted").
		NewLine().Add(api.NewTableFrom(rows))
}

func lease(expiresAt int64) api.Text {
	if expiresAt <= 0 {
		return api.Text{}.Append("no restore lease", "text-yellow-600")
	}
	return api.Text{}.Append("lease until "+leaseTime(expiresAt), "text-muted")
}

func leaseTime(expiresAt int64) string {
	return time.UnixMilli(expiresAt).UTC().Format(time.RFC3339)
}

func shortDigest(digest string) string {
	algorithm, sum, found := strings.Cut(digest, ":")
	if !found || len(sum) <= 8 {
		return digest
	}
	return algorithm + ":" + sum[:8]
}

func classpath(entries int) string {
	if entries == 1 {
		return "1 classpath entry"
	}
	return fmt.Sprintf("%d classpath entries", entries)
}
