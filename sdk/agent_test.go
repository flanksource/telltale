package telltale

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type temporaryAcceptError struct{}

func (temporaryAcceptError) Error() string   { return "temporary accept" }
func (temporaryAcceptError) Timeout() bool   { return false }
func (temporaryAcceptError) Temporary() bool { return true }

type temporaryListener struct {
	net.Listener
	failed atomic.Bool
}

func (l *temporaryListener) Accept() (net.Conn, error) {
	if l.failed.CompareAndSwap(false, true) {
		return nil, temporaryAcceptError{}
	}
	return l.Listener.Accept()
}

var _ = Describe("loopback agent", func() {
	It("retries a temporary listener failure", func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		wrapped := &temporaryListener{Listener: listener}
		agent := newAgent(Options{})
		agent.listener = wrapped
		go agent.accept()
		defer agent.Close()
		stream, err := net.Dial("tcp", listener.Addr().String())
		Expect(err).NotTo(HaveOccurred())
		defer stream.Close()
		Expect(stream.SetDeadline(time.Now().Add(time.Second))).To(Succeed())
		_, _, err = protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(wrapped.failed.Load()).To(BeTrue())
	})
	It("removes its discovery file when its listener fails permanently", func() {
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		a, err := Start(Options{Listen: "tcp"})
		Expect(err).NotTo(HaveOccurred())
		Expect(a.listener.Close()).To(Succeed())
		Eventually(func() bool { _, err := os.Stat(a.portFile); return errors.Is(err, os.ErrNotExist) }).Should(BeTrue())
		_ = a.Close()
	})
	It("replaces a stale Unix socket", func() {
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		config, err := os.UserConfigDir()
		Expect(err).NotTo(HaveOccurred())
		dir := filepath.Join(config, "telltale", "agents")
		Expect(os.MkdirAll(dir, 0700)).To(Succeed())
		path := filepath.Join(dir, strconv.Itoa(os.Getpid())+".sock")
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		Expect(err).NotTo(HaveOccurred())
		listener.SetUnlinkOnClose(false)
		Expect(listener.Close()).To(Succeed())
		agent, err := Start(Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(agent.Close()).To(Succeed())
	})
	It("closes an idle pre-authentication connection", func() {
		agent := newAgent(Options{})
		agent.authTimeout = 50 * time.Millisecond
		defer agent.Close()
		stream := agent.Connect()
		defer stream.Close()
		Expect(stream.SetReadDeadline(time.Now().Add(time.Second))).To(Succeed())
		_, _, err := protocol.NewConn(stream).Read()
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, os.ErrDeadlineExceeded)).To(BeFalse())
	})
	It("closes a connection that declares an oversized pre-authentication frame", func() {
		a := Loopback()
		defer a.Close()
		stream := a.Connect()
		defer stream.Close()
		Expect(stream.SetDeadline(time.Now().Add(time.Second))).To(Succeed())
		Expect(binary.Write(stream, binary.BigEndian, uint32(4097))).To(Succeed())
		_, _, err := protocol.NewConn(stream).Read()
		Expect(errors.Is(err, os.ErrDeadlineExceeded)).To(BeFalse())
		Expect(err).To(HaveOccurred())
	})
	It("replaces stale discovery files for a dead listener", func() {
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		config, err := os.UserConfigDir()
		Expect(err).NotTo(HaveOccurred())
		dir := filepath.Join(config, "telltale", "agents")
		Expect(os.MkdirAll(dir, 0700)).To(Succeed())
		data, err := json.Marshal(protocol.Endpoint{PID: os.Getpid(), Addr: "127.0.0.1:1", Token: "stale", Started: time.Now()})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"), data, 0600)).To(Succeed())
		agent, err := Start(Options{Listen: "tcp"})
		Expect(err).NotTo(HaveOccurred())
		Expect(agent.Close()).To(Succeed())
	})
	It("advertises and arms only probes allowed by options", func() {
		agent := newAgent(Options{Probes: []string{"stacks"}})
		defer agent.Close()
		stream := agent.Connect()
		defer stream.Close()
		conn, hello, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(hello.Probes).To(HaveLen(1))
		Expect(hello.Probes[0].Name).To(Equal("stacks"))
		Expect(conn.Send("arm", protocol.Arm{Session: "denied", Probe: "goprof", Params: json.RawMessage(`{"profile":"heap"}`)})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(ContainSubstring("not allowed"))
	})
	It("does not reveal hello before authentication", func() {
		agent := newAgent(Options{Token: "secret"})
		defer agent.Close()
		stream := agent.Connect()
		defer stream.Close()
		Expect(stream.SetReadDeadline(time.Now().Add(50 * time.Millisecond))).To(Succeed())
		_, _, err := protocol.NewConn(stream).Read()
		Expect(err).To(HaveOccurred())
	})
	It("writes a private discovery file, rejects stale contents, and removes it on close", func() {
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		agent, err := Start(Options{Listen: "tcp", Token: "chosen-token"})
		Expect(err).NotTo(HaveOccurred())
		info, err := os.Stat(agent.portFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
		endpoint, err := protocol.ReadEndpoint(context.Background(), agent.portFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(endpoint.Token).To(Equal("chosen-token"))
		endpoint.Started = endpoint.Started.Add(-time.Hour)
		data, err := json.Marshal(endpoint)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(agent.portFile, data, 0600)).To(Succeed())
		_, err = protocol.ReadEndpoint(context.Background(), agent.portFile)
		Expect(errors.Is(err, protocol.ErrStaleEndpoint)).To(BeTrue())
		Expect(agent.Close()).To(Succeed())
		_, err = os.Stat(agent.portFile)
		Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
	})
	It("rejects an invalid authentication token with a named error", func() {
		agent := newAgent(Options{Token: "correct"})
		defer agent.Close()
		stream := agent.Connect()
		defer stream.Close()
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		_, _, err := protocol.Authenticate(stream, "wrong")
		Expect(errors.Is(err, protocol.ErrAuthRejected)).To(BeTrue())
	})
	It("connects using the shared protocol client", func() {
		agent := Loopback()
		defer agent.Close()
		stream := agent.Connect()
		defer stream.Close()
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, hello, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(hello.Protocol).To(Equal(protocol.ProtocolVersion))
		Expect(conn.Send("ping", protocol.Ping{})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("pong"))
	})
	It("ends a session when its record limit is reached", func() {
		agent := Loopback()
		defer agent.Close()
		stream := agent.Connect()
		defer stream.Close()
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(send(conn, "arm", protocol.Arm{Session: "limited", Probe: "stacks", Params: json.RawMessage(`{}`), Limits: protocol.Limits{MaxRecords: 1}}, nil)).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("armed"))
		Record(context.Background(), "stacks", "one")
		for frame.Type != "ended" {
			frame, _, err = conn.Read()
			Expect(err).NotTo(HaveOccurred())
		}
		var ended protocol.Ended
		Expect(json.Unmarshal(frame.Body, &ended)).To(Succeed())
		Expect(ended.Error).To(ContainSubstring("limit"))
		Expect(conn.Send("arm", protocol.Arm{Session: "limited", Probe: "stacks", Params: json.RawMessage(`{}`)})).To(Succeed())
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var rearmed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &rearmed)).To(Succeed())
		Expect(rearmed.Error).To(BeEmpty())
	})
	It("drops excess records without ending a rate-limited session", func() {
		agent := Loopback()
		defer agent.Close()
		stream := agent.Connect()
		defer stream.Close()
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "rate", Probe: "stacks", Params: json.RawMessage(`{}`), Limits: protocol.Limits{Rate: 1}})).To(Succeed())
		_, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Record(context.Background(), "stacks", "one")
		Record(context.Background(), "stacks", "two")
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("records"))
		var records protocol.Records
		Expect(json.Unmarshal(frame.Body, &records)).To(Succeed())
		Expect(records.Items).To(HaveLen(1))
		Expect(records.Dropped).To(Equal(uint64(1)))
		Expect(Armed("stacks")).To(BeTrue())
	})
	It("ends a session when a checkpoint fails", func() {
		agent := Loopback()
		defer agent.Close()
		stream := agent.Connect()
		defer stream.Close()
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(send(conn, "arm", protocol.Arm{Session: "failure", Probe: "goprof", Params: json.RawMessage(`{"profile":"heap"}`)}, nil)).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("armed"))
		Expect(send(conn, "checkpoint", protocol.Checkpoint{Session: "failure", ID: "reading", Params: json.RawMessage(`{"profile":"invalid"}`)}, nil)).To(Succeed())
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("checkpointed"))
		Expect(string(frame.Body)).To(ContainSubstring("unsupported"))
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("ended"))
		var ended protocol.Ended
		Expect(json.Unmarshal(frame.Body, &ended)).To(Succeed())
		Expect(ended.Error).To(ContainSubstring("unsupported"))
	})
	It("authenticates, arms a stacks probe, and returns a checkpoint blob", func() {
		agent := Loopback()
		defer agent.Close()
		stream := agent.Connect()
		defer stream.Close()
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		body, err := json.Marshal(protocol.Arm{Session: "capture", Probe: "stacks", Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Write(protocol.Envelope{Type: "arm", Body: body}, nil)).To(Succeed())
		armed, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(armed.Type).To(Equal("armed"))
		body, err = json.Marshal(protocol.Checkpoint{Session: "capture", ID: "reading", Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Write(protocol.Envelope{Type: "checkpoint", Body: body}, nil)).To(Succeed())
		blob, data, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(blob.Type).To(Equal("blob"))
		Expect(string(data)).To(ContainSubstring("goroutine"))
	})
})
