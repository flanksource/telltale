package logs_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/flanksource/commons-db/query/providers/sqlite"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/logs"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	telltale "github.com/flanksource/telltale/sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLogs(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Logs") }

var _ = Describe("logs", func() {
	It("refuses process level changes and names the agent seam", func() {
		params := logs.Params{Target: target.Target{Seam: target.Process}, Source: "container", Container: "example", Levels: map[string]string{"*": "debug"}}
		Expect(params.Validate()).To(MatchError(ContainSubstring("agent seam")))
	})
	It("stores matching slog records with agent resource", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{logs.Kind: logs.NewKind(nil)})
		session := env.Start(logs.Kind, `{"match":"order"}`)
		tracingtest.Running(session)
		logger := slog.New(telltale.SlogHandler(slog.NewJSONHandler(&bytes.Buffer{}, nil))).With("logger", "checkout")
		logger.Info("order accepted")
		Eventually(func() string { data, _ := json.Marshal(env.Rows(session.Snapshot())); return string(data) }).WithTimeout(5 * time.Second).Should(And(ContainSubstring("order accepted"), ContainSubstring("checkout"), ContainSubstring("process.pid")))
	})
	It("records a container log with its resource labels", func() {
		socket := filepath.Join(GinkgoT().TempDir(), "docker.sock")
		listener, err := net.Listen("unix", socket)
		Expect(err).NotTo(HaveOccurred())
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/containers/example/json" {
				_, _ = w.Write([]byte(`{"Id":"container-id","Name":"/example","Config":{}}`))
				return
			}
			message := []byte("container message\n")
			header := make([]byte, 8)
			header[0] = 1
			binary.BigEndian.PutUint32(header[4:], uint32(len(message)))
			_, _ = w.Write(append(header, message...))
		})}
		go func() { _ = server.Serve(listener) }()
		DeferCleanup(server.Close)
		GinkgoT().Setenv("DOCKER_HOST", "unix://"+socket)
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{logs.Kind: logs.NewKind(nil)})
		session := env.Start(logs.Kind, `{"target":{"seam":"process"},"source":"container","container":"example"}`)
		Eventually(func() string { data, _ := json.Marshal(env.Rows(session.Snapshot())); return string(data) }).WithTimeout(5 * time.Second).Should(And(ContainSubstring("container message"), ContainSubstring("container.id")))
	})
})
