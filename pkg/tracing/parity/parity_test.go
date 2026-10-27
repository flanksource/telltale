package parity_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"testing"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/commons-db/observability"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/query/providers/sqlite"
	"github.com/flanksource/commons/har"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/pgproxy"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	"github.com/jackc/pgx/v5/pgproto3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestParity(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "HTTP and SQL parity") }

func postgres() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(listener.Close)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				backend := pgproto3.NewBackend(conn, conn)
				if _, err := backend.ReceiveStartupMessage(); err != nil {
					return
				}
				backend.Send(&pgproto3.AuthenticationOk{})
				backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
				if backend.Flush() != nil {
					return
				}
				for {
					message, err := backend.Receive()
					if err != nil {
						return
					}
					if _, ok := message.(*pgproto3.Query); ok {
						backend.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
						backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
						if backend.Flush() != nil {
							return
						}
					}
				}
			}()
		}
	}()
	return "postgres://test@" + listener.Addr().String() + "/test?sslmode=disable"
}

func queryProxy(address string) {
	conn, err := net.Dial("tcp", address)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = conn.Close() }()
	client := pgproto3.NewFrontend(conn, conn)
	client.Send(&pgproto3.StartupMessage{ProtocolVersion: pgproto3.ProtocolVersionNumber, Parameters: map[string]string{"user": "test", "database": "test"}})
	Expect(client.Flush()).To(Succeed())
	for {
		message, err := client.Receive()
		Expect(err).NotTo(HaveOccurred())
		if _, ok := message.(*pgproto3.ReadyForQuery); ok {
			break
		}
	}
	client.Send(&pgproto3.Query{String: "SELECT 1"})
	Expect(client.Flush()).To(Succeed())
	for {
		message, err := client.Receive()
		Expect(err).NotTo(HaveOccurred())
		if _, ok := message.(*pgproto3.ReadyForQuery); ok {
			break
		}
	}
}

var _ = Describe("HTTP and SQL seams", func() {
	It("records equivalent request and statement fields through self, agent and proxy and exports HAR", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		}))
		defer server.Close()
		requestURL := server.URL + "/items?token=token-secret&code=code-secret&sig=signature-secret"
		config := GinkgoT().TempDir()
		child := tracingtest.StartGoChild(tracingtest.GoChildBinary(GinkgoT().TempDir()), "-agent-config", config)
		registry := agentclient.NewRegistry()
		registry.Dir = filepath.Join(config, "telltale", "agents")
		var httpColumns, sqlColumns []string
		var maskedURL string
		var maskedHeaders map[string]string
		var maskedQuery map[string]string
		for _, mode := range []string{"self", "agent", "proxy"} {
			env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"http": httpproxy.UnifiedKind(registry), "sql": pgproxy.UnifiedKind(registry)})
			target := map[string]any{"seam": mode}
			if mode == "agent" {
				target["pid"] = child.PID
			}
			httpOptions := map[string]any{"target": target}
			if mode != "proxy" {
				httpOptions["features"] = []string{"http"}
			}
			httpParams, err := json.Marshal(httpOptions)
			Expect(err).NotTo(HaveOccurred())
			sqlParams, err := json.Marshal(map[string]any{"target": target, "connections": []string{"parity"}, "connection": "parity", "dsn": postgres()})
			Expect(err).NotTo(HaveOccurred())
			httpCapture, err := env.Runtime.Start(dbcontext.New(), tracing.StartRequest{Kind: "http", Params: httpParams})
			Expect(err).NotTo(HaveOccurred(), mode)
			sqlCapture, err := env.Runtime.Start(dbcontext.New(), tracing.StartRequest{Kind: "sql", Params: sqlParams})
			Expect(err).NotTo(HaveOccurred(), mode)
			tracingtest.Running(httpCapture.Session())
			tracingtest.Running(sqlCapture.Session())
			switch mode {
			case "agent":
				_, err = fmt.Fprintln(child.Stdin, "http", requestURL)
				Expect(err).NotTo(HaveOccurred())
				Expect(child.Await("http")).To(Equal("done"))
				_, err = fmt.Fprintln(child.Stdin, "sql")
				Expect(err).NotTo(HaveOccurred())
				Expect(child.Await("sql")).To(Equal("done"))
			case "self":
				client := http.Client{Transport: observability.ApplyHTTPObservability(context.Background(), "http", nil, nil)}
				request, err := http.NewRequest(http.MethodGet, requestURL, nil)
				Expect(err).NotTo(HaveOccurred())
				request.Header.Set("Authorization", "Bearer authorization-secret")
				request.Header.Set("Cookie", "session=cookie-secret")
				response, err := client.Do(request)
				Expect(err).NotTo(HaveOccurred())
				Expect(response.Body.Close()).To(Succeed())
				db, err := sql.Open("sqlite", ":memory:")
				Expect(err).NotTo(HaveOccurred())
				_, err = sqlite.ReadSQLPage(context.Background(), db, models.ConnectionTypeSQLite, sqlite.SQLPageRequest{Connection: "parity", Query: "SELECT 1", Page: query.PageRequest{Limit: 10}})
				Expect(err).NotTo(HaveOccurred())
				Expect(db.Close()).To(Succeed())
			case "proxy":
				proxyURL, err := url.Parse(httpCapture.Status().Exports["HTTP_PROXY"])
				Expect(err).NotTo(HaveOccurred())
				client := http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
				request, err := http.NewRequest(http.MethodGet, requestURL, nil)
				Expect(err).NotTo(HaveOccurred())
				request.Header.Set("Authorization", "Bearer authorization-secret")
				request.Header.Set("Cookie", "session=cookie-secret")
				response, err := client.Do(request)
				Expect(err).NotTo(HaveOccurred())
				Expect(response.Body.Close()).To(Succeed())
				queryProxy(net.JoinHostPort(sqlCapture.Status().Exports["PGHOST"], sqlCapture.Status().Exports["PGPORT"]))
			}
			httpCapture.Session().Stop("spec")
			sqlCapture.Session().Stop("spec")
			httpInfo := tracingtest.Ended(httpCapture.Session())
			sqlInfo := tracingtest.Ended(sqlCapture.Session())
			Expect(httpInfo.Error).To(BeEmpty(), mode)
			Expect(sqlInfo.Error).To(BeEmpty(), mode)
			httpRows, sqlRows := env.Rows(httpInfo), env.Rows(sqlInfo)
			Expect(httpRows).To(HaveLen(1), mode)
			Expect(sqlRows).To(HaveLen(1), mode)
			if httpColumns == nil {
				httpColumns = slices.Sorted(maps.Keys(httpRows[0]))
				sqlColumns = slices.Sorted(maps.Keys(sqlRows[0]))
			}
			Expect(slices.Sorted(maps.Keys(httpRows[0]))).To(Equal(httpColumns), mode)
			Expect(slices.Sorted(maps.Keys(sqlRows[0]))).To(Equal(sqlColumns), mode)
			if mode != "proxy" {
				Expect(httpRows[0]["resource"]).To(HaveKey("process.pid"), mode)
				Expect(sqlRows[0]["resource"]).To(HaveKey("process.pid"), mode)
			}
			Expect(httpRows[0]).To(And(HaveKeyWithValue("method", "GET"), HaveKeyWithValue("status", BeNumerically("==", 200)), HaveKey("request"), HaveKey("response")), mode)
			Expect(sqlRows[0]).To(And(HaveKeyWithValue("sql", ContainSubstring("SELECT 1")), HaveKeyWithValue("op", "SELECT"), HaveKey("durationMs"), HaveKey("rows")), mode)
			var output bytes.Buffer
			Expect(env.Runtime.Export(context.Background(), tracing.TraceRef{Kind: "http", Stream: httpInfo.Events.Stream}, "har", &output)).To(Succeed())
			var archive har.File
			Expect(json.Unmarshal(output.Bytes(), &archive)).To(Succeed())
			Expect(archive.Log.Version).To(Equal("1.2"))
			Expect(archive.Log.Entries).To(HaveLen(1))
			captured := archive.Log.Entries[0].Request
			encoded, err := json.Marshal(captured)
			Expect(err).NotTo(HaveOccurred())
			for _, secret := range []string{"token-secret", "code-secret", "signature-secret", "authorization-secret", "cookie-secret"} {
				Expect(string(encoded)).NotTo(ContainSubstring(secret), mode)
			}
			headers := map[string]string{}
			for _, header := range captured.Headers {
				if header.Name == "Authorization" || header.Name == "Cookie" {
					headers[header.Name] = header.Value
				}
			}
			Expect(headers).To(HaveLen(2), mode)
			Expect(captured.QueryString).To(HaveLen(3), mode)
			query := map[string]string{}
			for _, item := range captured.QueryString {
				query[item.Name] = item.Value
			}
			Expect(query).To(HaveKey("token"))
			Expect(query).To(HaveKey("code"))
			Expect(query).To(HaveKey("sig"))
			if maskedHeaders == nil {
				maskedURL, maskedHeaders, maskedQuery = captured.URL, headers, query
			}
			Expect(captured.URL).To(Equal(maskedURL), mode)
			Expect(headers).To(Equal(maskedHeaders), mode)
			Expect(query).To(Equal(maskedQuery), mode)
			Expect(archive.Log.Entries[0].Response.Status).To(Equal(200))
		}
	})
})
