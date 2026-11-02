package definition_test

import (
	"os"
	"path/filepath"

	"github.com/flanksource/commons-db/connection/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/definition"
)

const header = `apiVersion: telltale.flanksource.com/v1alpha1
kind: TraceDefinition
metadata: {name: checkout}
`

// names are the resolved names of spec's entries, in walk order.
func names(spec string) []string {
	resolved, err := definition.Resolve(decode(header+spec), definition.Options{})
	Expect(err).ToNot(HaveOccurred())
	var resolvedNames []string
	for _, at := range resolved.Entries {
		resolvedNames = append(resolvedNames, at.Name)
	}
	return resolvedNames
}

// refusal is the error Validate gives spec, with connections as its store.
func refusal(spec string, connections *store.Store) error {
	return definition.Validate(decode(header+spec), definition.Options{Connections: connections})
}

// connectionOf is the connection the first sqlserver planCache entry of spec
// resolves to.
func connectionOf(spec string, connections *store.Store) string {
	resolved, err := definition.Resolve(decode(header+spec), definition.Options{Connections: connections})
	Expect(err).ToNot(HaveOccurred())
	return resolved.Definition.Spec.Collect.SQLServer.PlanCache[0].Connection
}

// storeWith is a connection store of one layer defining each name as a
// connection of its type.
func storeWith(types map[string]string) *store.Store {
	dir := GinkgoT().TempDir()
	for name, connType := range types {
		resource := "apiVersion: mission-control.flanksource.com/v1\nkind: Connection\nmetadata: {name: " + name + "}\nspec: {type: " + connType + ", url: {value: 'sqlserver://db.example.test'}}\n"
		Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(resource), 0o600)).To(Succeed())
	}
	connections, err := store.New(store.Layer{Name: "test", Dir: dir})
	Expect(err).ToNot(HaveOccurred())
	return connections
}

var _ = Describe("an entry's default name", func() {
	DescribeTable("is <definition>-<section>[-<group>]-<type>, numbered only in a list of several",
		func(spec string, want []string) {
			Expect(names(spec)).To(Equal(want))
		},
		Entry("one proxy", "spec: {proxy: {http: [{}]}}", []string{"checkout-proxy-http"}),
		Entry("two proxies, numbered from 1", "spec: {proxy: {postgres: [{env: A_DSN}, {env: B_DSN}]}}",
			[]string{"checkout-proxy-postgres-1", "checkout-proxy-postgres-2"}),
		Entry("a grouped entry", "spec: {collect: {go: {gops: [{}]}}}", []string{"checkout-collect-go-gops"}),
		Entry("a query provider", "spec: {collect: {query: {prometheus: [{query: up, url: 'http://localhost:9090'}]}}}", []string{"checkout-collect-query-prometheus"}),
		Entry("a named entry keeps its name, its sibling numbered", "spec: {connections: [{name: db, type: sql_server}], instrument: {sqlserver: {xevent: [{name: slow, sessionName: a}, {sessionName: b}]}}}",
			[]string{"slow", "checkout-instrument-sqlserver-xevent-2"}),
	)

	It("is refused when it collides with another entry's, naming both", func() {
		Expect(refusal(`spec: {collect: {go: {gops: [{}], pprof: [{name: checkout-collect-go-gops, profile: heap}]}}}`, nil)).
			To(MatchError(ContainSubstring(`/spec/collect/go/pprof/0/name: "checkout-collect-go-gops" is also the name of /spec/collect/go/gops/0`)))
	})
})

var _ = Describe("a default connection", func() {
	planCache := "    collect: {sqlserver: {planCache: [{}]}}\n"

	It("is the one connection of the entry's type", func() {
		spec := "spec:\n    connections: [{name: orders, type: postgres}, {name: db, type: sql_server}]\n" + planCache
		Expect(connectionOf(spec, nil)).To(Equal("connection://db"))
	})

	It("is a stored reference of the entry's type, told by the store", func() {
		spec := "spec:\n    connections: [connection://checkout-sqlserver, connection://loki]\n" + planCache
		Expect(connectionOf(spec, storeWith(map[string]string{"checkout-sqlserver": "sql_server", "loki": "loki"}))).To(Equal("connection://checkout-sqlserver"))
	})

	It("gives way to the connection an entry names", func() {
		spec := "spec:\n    connections: [{name: db, type: sql_server}]\n    collect: {sqlserver: {planCache: [{connection: connection://other}]}}\n"
		Expect(connectionOf(spec, nil)).To(Equal("connection://other"))
	})

	DescribeTable("is refused when it cannot be told",
		func(connections string, connectionStore func() *store.Store, message string) {
			var opened *store.Store
			if connectionStore != nil {
				opened = connectionStore()
			}
			Expect(refusal("spec:\n    connections: "+connections+"\n"+planCache, opened)).To(MatchError(ContainSubstring(message)))
		},
		Entry("none of the type", "[{name: orders, type: postgres}]", nil,
			"/spec/collect/sqlserver/planCache/0/connection: names no connection, and spec.connections has no sql_server connection to default to: name one, or add one to spec.connections"),
		Entry("two of the type, naming both", "[{name: a, type: sql_server}, {name: b, type: sql_server}]", nil,
			"connection://a, connection://b are each a default sql_server connection"),
		Entry("a reference with no store to tell its type", "[connection://checkout-sqlserver]", nil,
			"without a connection store the type of connection://checkout-sqlserver is unknown"),
		Entry("a reference the store does not define", "[connection://missing]", func() *store.Store { return storeWith(map[string]string{}) },
			"/spec/connections/0: no connection connection://missing"),
	)
})

var _ = Describe("an inline connection's credentials", func() {
	DescribeTable("are refused as literals",
		func(connection, message string) {
			Expect(refusal("spec:\n    connections: ["+connection+"]\n", nil)).To(MatchError(ContainSubstring(message)))
		},
		Entry("a literal password", "{name: orders, type: postgres, password: {value: hunter2}}", "password: a literal value would be stored in the definition"),
		Entry("userinfo in the url", "{name: orders, type: postgres, url: 'postgres://app:hunter2@db:5432/orders'}", "url: postgres://***@db:5432/orders carries literal credentials"),
		Entry("a valueFrom naming nothing", "{name: orders, type: postgres, username: {valueFrom: {}}}", "username: valueFrom names no secret"),
	)

	It("take valueFrom references and the url placeholders", func() {
		Expect(refusal(`spec:
    connections:
      - name: orders
        type: postgres
        url: postgres://$(username):$(password)@db:5432/orders
        username: {valueFrom: {onePassword: op://dev/orders-db/username}}
        password: {valueFrom: {onePassword: op://dev/orders-db/password}}
`, nil)).To(Succeed())
	})

	It("are refused in a proxy's DSN, which a $VAR would carry", func() {
		Expect(refusal(`spec: {proxy: {postgres: [{env: ORDERS_DSN, dsn: 'postgres://app:hunter2@db/orders'}]}}`, nil)).
			To(MatchError(ContainSubstring("/spec/proxy/postgres/0: dsn: ")))
		Expect(refusal(`spec: {proxy: {postgres: [{env: ORDERS_DSN, dsn: 'postgres://$DB_USER:$DB_PASSWORD@db/orders'}]}}`, nil)).To(Succeed())
	})
})

var _ = Describe("two proxies", func() {
	DescribeTable("are refused when they would clash, naming both",
		func(proxy, message string) {
			Expect(refusal("spec: {proxy: "+proxy+"}", nil)).To(MatchError(ContainSubstring(message)))
		},
		Entry("on one fixed port", "{http: [{listen: '127.0.0.1:18080'}, {listen: '127.0.0.1:18080', export: {proxy: B_PROXY}}]}",
			"/spec/proxy/http/1/listen: port 18080 is also /spec/proxy/http/0's"),
		Entry("exporting the standard variables both", "{http: [{}, {}]}",
			"/spec/proxy/http/1/export: HTTP_PROXY is also exported by /spec/proxy/http/0"),
		Entry("reading one DSN variable", "{postgres: [{env: ORDERS_DSN}, {env: ORDERS_DSN}]}",
			"/spec/proxy/postgres/1/export: ORDERS_DSN is also exported by /spec/proxy/postgres/0"),
	)

	It("start side by side on their own ports and variables", func() {
		Expect(refusal(`spec:
    proxy:
      http: [{listen: '127.0.0.1:18080'}, {listen: '127.0.0.1:18081', export: {proxy: PARTNER_PROXY}}]
      postgres: [{env: ORDERS_DSN}, {env: USERS_DSN}]
`, nil)).To(Succeed())
	})
})
