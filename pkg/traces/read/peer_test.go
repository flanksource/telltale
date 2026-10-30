package read_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces/read"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestReadPeer is the subprocess half of the Ginkgo owner/reader spec.
func TestReadPeer(t *testing.T) {
	dir := os.Getenv("TELLTALE_READ_PEER")
	if dir == "" {
		return
	}
	if err := registerFixture(); err != nil {
		t.Fatal(err)
	}
	info, err := telltale.Get(telltale.GetOptions{DataDir: dir, RunID: "run-peer"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != telltale.StatusRunning || info.ExitCode != nil {
		t.Fatalf("live manifest: %+v", info)
	}
	s, err := read.Resolve("read_fixture", read.Scope{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	fmt.Println(s.Refs[0].Runtime.Results.Role())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	encoder := json.NewEncoder(os.Stdout)
	if err := read.Follow(ctx, s.Refs[0], read.Query{}, func(row query.Row) error { return encoder.Encode(row) }); err != nil {
		t.Fatal(err)
	}
}

var _ = Describe("cross-process read-only follower", func() {
	It("discovers a running manifest, sees subsequent commits and exits on seal", func() {
		feeding = &feed{rows: make(chan reading), ack: make(chan error, 1)}
		dir := GinkgoT().TempDir()
		_, capture := fixture(dir, "run-peer")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReadPeer$")
		child.Env = append(os.Environ(), "TELLTALE_READ_PEER="+dir)
		stderr := &bytes.Buffer{}
		child.Stderr = stderr
		stdout, err := child.StdoutPipe()
		Expect(err).NotTo(HaveOccurred())
		Expect(child.Start()).To(Succeed())
		DeferCleanup(func() {
			if child.ProcessState == nil {
				_ = child.Process.Kill()
				_ = child.Wait()
			}
		})
		lines := bufio.NewScanner(stdout)
		Expect(lines.Scan()).To(BeTrue(), stderr.String())
		Expect(lines.Text()).To(Equal("reader"))
		appendRow(capture, reading{At: time.Now(), Value: 7, Label: "peer"})
		Expect(lines.Scan()).To(BeTrue(), stderr.String())
		var row query.Row
		decodeErr := json.Unmarshal(lines.Bytes(), &row)
		if decodeErr != nil {
			var rest strings.Builder
			for lines.Scan() {
				fmt.Fprintln(&rest, lines.Text())
			}
			Expect(lines.Err()).NotTo(HaveOccurred())
			Fail(fmt.Sprintf("peer output: %s (%v)", rest.String(), decodeErr))
		}
		Expect(row).To(HaveKeyWithValue("value", float64(7)))
		_, err = capture.Stop(context.Background())
		Expect(err).NotTo(HaveOccurred())
		for lines.Scan() {
		}
		Expect(lines.Err()).NotTo(HaveOccurred())
		Expect(child.Wait()).To(Succeed(), stderr.String())
	})
})
