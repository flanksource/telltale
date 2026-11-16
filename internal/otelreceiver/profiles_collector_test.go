package otelreceiver

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

func collectorPipeline(endpoint, queueSizer string) string {
	GinkgoHelper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	address := listener.Addr().String()
	Expect(listener.Close()).To(Succeed())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	command := exec.CommandContext(ctx, "go", "run", ".", "-listen", address, "-export", endpoint, "-queue-sizer", queueSizer)
	command.Dir = "../../hack/collector-fixture"
	command.Env = append(os.Environ(), "GOWORK=off")
	command.Stderr = GinkgoWriter
	output, err := command.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	input, err := command.StdinPipe()
	Expect(err).NotTo(HaveOccurred())
	Expect(command.Start()).To(Succeed())
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- fmt.Sprintf("Collector exited before ready: %v", scanner.Err())
		}
	}()
	DeferCleanup(func() {
		Expect(input.Close()).To(Succeed())
		Expect(command.Wait()).To(Succeed())
		cancel()
	})
	var message string
	Eventually(ready).WithTimeout(150 * time.Second).Should(Receive(&message))
	Expect(message).To(Equal("ready"))
	return "http://" + address + "/v1development/profiles"
}

var _ = Describe("released Collector Profiles pipeline", func() {
	DescribeTable("preserves active, updated and empty snapshots through the 0.163 receiver and queued OTLP exporter", func(queueSizer string) {
		receiver, store := testReceiver(DefaultOptions())
		endpoint := collectorPipeline(serveGRPC(receiver, false), queueSizer)
		client := &http.Client{Timeout: 10 * time.Second}
		for sequence := int64(1); sequence <= 3; sequence++ {
			snapshot := topSnapshot(sequence)
			if sequence == 3 {
				snapshot.Rows, snapshot.Observed = nil, 0
			}
			body, err := profiles.Encode([]profiles.Snapshot{snapshot})
			Expect(err).NotTo(HaveOccurred())
			request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
			Expect(err).NotTo(HaveOccurred())
			request.Header.Set("Content-Type", protobufType)
			request.Header.Set("OTLP-Profiles-Development-Version", profiles.DevelopmentVersion)
			response, err := client.Do(request)
			Expect(err).NotTo(HaveOccurred())
			responseBody, err := io.ReadAll(response.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(response.Body.Close()).To(Succeed())
			Expect(response.StatusCode).To(Equal(http.StatusOK), string(responseBody))
		}
		Eventually(func(g Gomega) {
			stored, err := store.ReadProfiles(context.Background(), ProfileReadOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(stored)).To(Equal(3), "Collector must preserve the clearing profile")
			g.Expect(stored[0].Rows[0].ElapsedNS).To(Equal(int64(time.Second)))
			g.Expect(stored[1].Rows[0].ElapsedNS).To(Equal(int64(2 * time.Second)))
			g.Expect(stored[2].Rows).To(BeEmpty())
			g.Expect(stored[2].Observed).To(BeZero())
			native, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(stored[2].Native)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(native.SampleCount()).To(Equal(1))
			ref := read.Ref{Runtime: store.Runtime(), TraceRef: tracing.TraceRef{Kind: "http_top", Stream: profileStream("http_top")}}
			latest, err := read.Top(context.Background(), ref, read.TopQuery{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(latest.Frame.Tick).To(Equal(int64(3)))
			g.Expect(latest.Frame.Retained).To(BeZero())
			g.Expect(latest.Rows).To(BeEmpty())
		}).WithTimeout(10 * time.Second).Should(Succeed())
	}, Entry("request-sized queue", "requests"), Entry("item-sized queue", "items"))
})
