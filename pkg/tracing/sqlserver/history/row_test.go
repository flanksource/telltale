package history_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"time"

	deadlockgraph "github.com/flanksource/telltale/pkg/tracing/sqlserver/deadlocks"
	deadlocks "github.com/flanksource/telltale/pkg/tracing/sqlserver/history"

	"github.com/flanksource/commons-db/query"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// rowGraph is a three-participant graph whose lead (the first victim) is its
// second process, spread over two databases, two hosts and two apps, so every
// derived row column has something to tell apart.
func rowGraph() deadlockgraph.Graph {
	at := time.Date(2026, 9, 14, 13, 46, 31, 365_000_000, time.UTC)
	return deadlockgraph.Graph{
		Deadlock: deadlockgraph.Deadlock{
			ID: deadlockgraph.FormatID(at, "processB"), Timestamp: at, Database: "OIPA_UAT",
			Shape: deadlockgraph.ShapeKeyLookupVsWriter, Signature: "keylock AsActivity.CX X→S ⇄ keylock AsActivity.IX_2 S→X",
			Objects: []string{"AsActivity"}, Indexes: []string{"CX", "IX_2"}, LockTypes: []string{"keylock"},
			Victims: 2, Participants: 3,
			VictimHost: "cycle-0", VictimApp: "jdbc", VictimStatement: "SELECT 1 FROM AsActivity",
			Processes: []deadlockgraph.Process{
				{ID: "processA", SPID: 210, Host: "oipa-app-1", ClientApp: "web", Database: "OIPA_UAT",
					Statement: "UPDATE  ASACTIVITY\n SET StatusCode = @P0", StatementLength: 38},
				{ID: "processB", Victim: true, SPID: 211, Host: "cycle-0", ClientApp: "jdbc", Database: "OIPA_UAT",
					Statement: "SELECT 1\n FROM AsActivity", StatementLength: 900, InputBufferTruncated: true},
				{ID: "processC", Victim: true, SPID: 212, Host: "cycle-0", ClientApp: "jdbc", Database: "OIPA_AUDIT",
					Statement: "SELECT 2"},
			},
			Resources: []deadlockgraph.Resource{{Kind: "keylock", Object: "AsActivity", Index: "CX", Resolution: deadlockgraph.ResolutionNamed}},
		},
		XML: "<deadlock/>",
	}
}

var _ = Describe("Row", func() {
	It("projects a graph into the stored row: participant facets distinct and sorted, the lead's truncation, every statement", func() {
		graph := rowGraph()

		row, err := deadlocks.RowFromGraph(graph)

		Expect(err).NotTo(HaveOccurred())
		Expect(row).To(Equal(deadlocks.Row{
			ID: "20260914T134631365Z-processB", Timestamp: graph.Timestamp, Database: "OIPA_UAT",
			Databases: []string{"OIPA_AUDIT", "OIPA_UAT"},
			Shape:     "key-lookup-vs-writer", Signature: graph.Signature,
			Objects: []string{"AsActivity"}, Indexes: []string{"CX", "IX_2"}, LockTypes: []string{"keylock"},
			Hosts: []string{"cycle-0", "oipa-app-1"}, Apps: []string{"jdbc", "web"},
			Victims: 2, Participants: 3,
			VictimHost: "cycle-0", VictimApp: "jdbc", VictimStatement: "SELECT 1 FROM AsActivity",
			StatementTruncated: true,
			Statements:         "UPDATE ASACTIVITY SET StatusCode = @P0\nSELECT 1 FROM AsActivity\nSELECT 2",
			Processes:          graph.Processes, Resources: graph.Resources, XML: "<deadlock/>",
		}))
	})

	It("stores empty lists rather than null, so every row read back has the same shape", func() {
		graph := rowGraph()
		graph.Objects, graph.Indexes, graph.LockTypes, graph.Resources = nil, nil, nil, nil

		row, err := deadlocks.RowFromGraph(graph)

		Expect(err).NotTo(HaveOccurred())
		Expect([]any{row.Objects, row.Indexes, row.LockTypes, row.Resources}).To(Equal([]any{
			[]string{}, []string{}, []string{}, []deadlockgraph.Resource{},
		}))
	})

	DescribeTable("refuses a graph whose id does not name one of its participants",
		func(id, message string) {
			graph := rowGraph()
			graph.ID = id

			_, err := deadlocks.RowFromGraph(graph)

			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("an id that is not FormatID's", "not-an-id", "not-an-id"),
		Entry("an id naming a process the graph does not have", "20260914T134631365Z-processZ", "processZ"),
	)

	It("rebuilds the graph it was projected from", func() {
		graph := rowGraph()
		row, err := deadlocks.RowFromGraph(graph)
		Expect(err).NotTo(HaveOccurred())

		Expect(row.Graph()).To(Equal(graph))
	})

	It("encodes every key the graph detail encodes, so a row renders wherever a graph does", func() {
		graph := decoded("key-lookup-ix2-four-process.xml")
		deadlockgraph.Analyze(&graph.Deadlock)
		row, err := deadlocks.RowFromGraph(graph)
		Expect(err).NotTo(HaveOccurred())

		graphKeys, rowKeys := jsonKeys(graph), jsonKeys(row)

		for key, value := range graphKeys {
			Expect(rowKeys).To(HaveKeyWithValue(key, value), key)
		}
		Expect(len(rowKeys)-len(graphKeys)).To(Equal(5), "databases, hosts, apps, statementTruncated and statements")
	})

	It("declares the columns, filters and time column the trace store indexes", func() {
		columns, err := query.ColumnsFor(reflect.TypeFor[deadlocks.Row]())
		Expect(err).NotTo(HaveOccurred())

		kinds := map[string]string{}
		for _, column := range columns {
			switch {
			case column.Filter == nil:
				kinds[column.Name] = string(column.Type)
			case column.Filter.Disabled:
				kinds[column.Name] = "disabled"
			default:
				kinds[column.Name] = string(column.Type) + "/" + string(column.Filter.Kind.Normalized())
			}
		}
		Expect(kinds).To(Equal(map[string]string{
			"id": "string/exact", "timestamp": "datetime", "database": "string/terms", "databases": "json/terms",
			"shape": "string/terms", "signature": "string/text",
			"objects": "json/terms", "indexes": "json/terms", "lockTypes": "json/terms",
			"hosts": "json/terms", "apps": "json/terms",
			"victims": "number/range", "participants": "number/range",
			"victimHost": "string/terms", "victimApp": "string/terms", "victimStatement": "string/text",
			"statementTruncated": "boolean/boolean", "statements": "string/text",
			"processes": "json", "resources": "json", "xml": "disabled", "decodeError": "string/text",
		}))
	})

	It("hides the key and the detail columns from the table, and names the time column timestamp", func() {
		var visible, hidden []string
		for _, column := range (deadlocks.Row{}).Columns() {
			if column.Hidden {
				hidden = append(hidden, column.Name)
			} else {
				visible = append(visible, column.Name)
			}
		}

		Expect(visible).To(Equal([]string{"timestamp", "shape", "signature", "indexes", "database", "participants", "victimHost", "victimStatement", "decodeError"}))
		Expect(hidden).To(ConsistOf("id", "databases", "objects", "lockTypes", "hosts", "apps", "victims", "victimApp",
			"statementTruncated", "statements", "processes", "resources", "xml"))
	})

	It("renders a table row for every column it declares", func() {
		row, err := deadlocks.RowFromGraph(rowGraph())
		Expect(err).NotTo(HaveOccurred())

		cells := row.Row()
		for _, column := range row.Columns() {
			Expect(cells).To(HaveKey(column.Name))
		}
		Expect(cells).To(HaveLen(len(row.Columns())))
	})
})

func jsonKeys(value any) map[string]any {
	GinkgoHelper()
	encoded, err := json.Marshal(value)
	Expect(err).NotTo(HaveOccurred())
	var keys map[string]any
	Expect(json.Unmarshal(encoded, &keys)).To(Succeed())
	return keys
}

var _ = Describe("UndecodedRow", func() {
	const event = `<event name="xml_deadlock_report" timestamp="2026-10-01T23:57:43.704Z"><data name="xml_report"><value><deadlock/></value></data></event>`
	cause := errors.New("stmtstart=-3 stmtend=-1 are not the UTF-16 byte offsets of a statement")

	It("dates and keys the report by its event, so the stream orders and skips it like a graph, and keeps its XML and why it failed", func() {
		row, err := deadlocks.UndecodedRow(event, cause)
		Expect(err).NotTo(HaveOccurred())
		again, err := deadlocks.UndecodedRow(event, cause)
		Expect(err).NotTo(HaveOccurred())

		at, lead, err := deadlockgraph.ParseID(row.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect([]any{at, lead[:len("undecoded-")], row.Timestamp, row.DecodeError, row.XML, again.ID}).To(Equal([]any{
			time.Date(2026, 10, 1, 23, 57, 43, 704_000_000, time.UTC), "undecoded-",
			time.Date(2026, 10, 1, 23, 57, 43, 704_000_000, time.UTC), cause.Error(), event, row.ID,
		}))
	})

	It("fails when the report is not even an event with a timestamp, since it cannot be placed in the stream", func() {
		_, err := deadlocks.UndecodedRow(`<event name="xml_deadlock_report"/>`, cause)
		Expect(err).To(MatchError(ContainSubstring("no event timestamp")))
	})
})
