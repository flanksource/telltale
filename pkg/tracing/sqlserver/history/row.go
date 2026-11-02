package history

import (
	"fmt"
	"slices"
	"strings"
	"time"

	deadlockgraph "github.com/flanksource/telltale/pkg/tracing/sqlserver/deadlocks"

	"github.com/flanksource/clicky/api"
)

// Row is one deadlock as the trace store keeps it: the kind sql_deadlock,
// served by the trace-results/sql_deadlock profile, which filters, counts,
// sorts and pages rows in SQL.
//
// Its JSON is a superset of Graph's, so whatever renders a graph detail renders
// a row as it is read back. What Graph does not carry is derived for the
// index: the participants' databases, hosts and apps as lists, so a facet
// counts deadlocks rather than processes, and every running statement as one
// text column, which is what a statement search matches.
type Row struct {
	ID        string    `json:"id" filter:"exact"`
	Timestamp time.Time `json:"timestamp"`

	// Database is the lead process's database; Databases every participant's.
	Database  string   `json:"database" filter:"terms"`
	Databases []string `json:"databases"`

	Shape     string `json:"shape" filter:"terms"`
	Signature string `json:"signature" filter:"text"`

	Objects   []string `json:"objects"`
	Indexes   []string `json:"indexes"`
	LockTypes []string `json:"lockTypes"`
	Hosts     []string `json:"hosts"`
	Apps      []string `json:"apps"`

	Victims      int `json:"victims" filter:"range"`
	Participants int `json:"participants" filter:"range"`

	VictimHost      string `json:"victimHost" filter:"terms"`
	VictimApp       string `json:"victimApp" filter:"terms"`
	VictimStatement string `json:"victimStatement" filter:"text"`
	// StatementTruncated is the lead process's InputBufferTruncated: the
	// victim statement is only the part SQL Server's input buffer kept.
	StatementTruncated bool `json:"statementTruncated" filter:"boolean"`
	// Statements is every participant's running statement, whitespace
	// collapsed, one per line.
	Statements string `json:"statements" filter:"text"`

	Processes []deadlockgraph.Process  `json:"processes"`
	Resources []deadlockgraph.Resource `json:"resources"`
	XML       string                   `json:"xml" filter:"-"`

	// DecodeError is why the report would not decode (UndecodedRow); such a
	// row has only its time, id and XML.
	DecodeError string `json:"decodeError,omitempty" filter:"text"`
}

// RowFromGraph projects an analysed graph into its stored row. The lead
// process is the one the graph's id names, as DecodeReport chose it.
func RowFromGraph(graph deadlockgraph.Graph) (Row, error) {
	_, leadID, err := deadlockgraph.ParseID(graph.ID)
	if err != nil {
		return Row{}, err
	}
	lead := slices.IndexFunc(graph.Processes, func(p deadlockgraph.Process) bool { return p.ID == leadID })
	if lead < 0 {
		return Row{}, fmt.Errorf("deadlock %s names lead process %s, which is not one of its %d participants",
			graph.ID, leadID, len(graph.Processes))
	}
	statements := make([]string, len(graph.Processes))
	for i, process := range graph.Processes {
		statements[i] = strings.Join(strings.Fields(process.Statement), " ")
	}
	d := graph.Deadlock
	return Row{
		ID: d.ID, Timestamp: d.Timestamp, Database: d.Database,
		Databases: participantValues(d.Processes, func(p deadlockgraph.Process) string { return p.Database }),
		Shape:     string(d.Shape), Signature: d.Signature,
		Objects: nonNil(d.Objects), Indexes: nonNil(d.Indexes), LockTypes: nonNil(d.LockTypes),
		Hosts:   participantValues(d.Processes, func(p deadlockgraph.Process) string { return p.Host }),
		Apps:    participantValues(d.Processes, func(p deadlockgraph.Process) string { return p.ClientApp }),
		Victims: d.Victims, Participants: d.Participants,
		VictimHost: d.VictimHost, VictimApp: d.VictimApp, VictimStatement: d.VictimStatement,
		StatementTruncated: d.Processes[lead].InputBufferTruncated,
		Statements:         strings.Join(statements, "\n"),
		Processes:          nonNil(d.Processes), Resources: nonNil(d.Resources), XML: graph.XML,
	}, nil
}

// Graph is the graph the row was projected from, for the detail view.
func (r Row) Graph() deadlockgraph.Graph {
	return deadlockgraph.Graph{
		Deadlock: deadlockgraph.Deadlock{
			ID: r.ID, Timestamp: r.Timestamp, Database: r.Database, Shape: deadlockgraph.Shape(r.Shape), Signature: r.Signature,
			Objects: r.Objects, Indexes: r.Indexes, LockTypes: r.LockTypes,
			Victims: r.Victims, Participants: r.Participants,
			VictimHost: r.VictimHost, VictimApp: r.VictimApp, VictimStatement: r.VictimStatement,
			Processes: r.Processes, Resources: r.Resources,
		},
		XML: r.XML,
	}
}

func participantValues(processes []deadlockgraph.Process, value func(deadlockgraph.Process) string) []string {
	values := []string{}
	for _, process := range processes {
		if v := value(process); v != "" && !slices.Contains(values, v) {
			values = append(values, v)
		}
	}
	slices.Sort(values)
	return values
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// Columns is the deadlock table: when, what shape, which lock cycle, where and
// who lost. The key, the participant lists and the graph detail are hidden
// here, and only here: a column hidden in the profile is dropped from the JSON
// a row detail renders from.
func (Row) Columns() []api.ColumnDef {
	hidden := func(name string) api.ColumnDef { return api.Column(name).Hidden().Build() }
	return []api.ColumnDef{
		api.Column("timestamp").Label("Time (UTC)").Kind("timestamp").Build(),
		api.Column("shape").Label("Shape").Build(),
		api.Column("signature").Label("Lock cycle").MinWidthPixels(280).MaxWidthPixels(560).Build(),
		api.Column("indexes").Label("Indexes").Kind("tags").Build(),
		api.Column("database").Label("DB").Build(),
		api.Column("participants").Label("Sessions").Style("text-right").Build(),
		api.Column("victimHost").Label("Victim host").Build(),
		api.Column("victimStatement").Label("Victim statement").MaxWidth(120).Build(),
		api.Column("decodeError").Label("Decode error").DefaultHidden().Build(),
		hidden("id"), hidden("databases"), hidden("objects"), hidden("lockTypes"), hidden("hosts"), hidden("apps"),
		hidden("victims"), hidden("victimApp"), hidden("statementTruncated"), hidden("statements"),
		hidden("processes"), hidden("resources"), hidden("xml"),
	}
}

// Row is the table row: the shape and session count labelled for a reader,
// filtering on the stored values.
func (r Row) Row() map[string]any {
	shape := deadlockgraph.Shape(r.Shape)
	return map[string]any{
		"timestamp":       r.Timestamp.UTC(),
		"shape":           api.TableCell{Value: api.Text{Content: shape.Label(), Style: shape.Style()}, FilterValue: r.Shape},
		"signature":       api.Text{Content: r.Signature, Style: "font-mono text-muted"},
		"indexes":         r.Indexes,
		"database":        r.Database,
		"participants":    api.TableCell{Value: participantsLabel(r.Participants, r.Victims), FilterValue: r.Participants},
		"victimHost":      r.VictimHost,
		"victimStatement": victimStatement(r),
		"id":              r.ID, "databases": r.Databases, "objects": r.Objects, "lockTypes": r.LockTypes,
		"hosts": r.Hosts, "apps": r.Apps, "victims": r.Victims, "victimApp": r.VictimApp,
		"statementTruncated": r.StatementTruncated, "statements": r.Statements,
		"processes": r.Processes, "resources": r.Resources, "xml": r.XML, "decodeError": r.DecodeError,
	}
}

func victimStatement(r Row) api.Text {
	if r.DecodeError != "" {
		return api.Text{}.AddText("(not decoded) ", "text-red-600").AddText(r.DecodeError, "text-muted")
	}
	if !r.StatementTruncated {
		return api.Text{Content: r.VictimStatement, Style: "text-muted"}
	}
	return api.Text{}.AddText("(truncated) ", "text-orange-600").AddText(r.VictimStatement, "text-muted")
}

func participantsLabel(participants, victims int) string {
	if victims == 1 {
		return fmt.Sprintf("%d", participants)
	}
	return fmt.Sprintf("%d (%d victims)", participants, victims)
}
