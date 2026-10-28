package telltale

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func blockAfterDisconnect() {
	ready := make(chan struct{})
	time.AfterFunc(10*time.Millisecond, func() { close(ready) })
	<-ready
}

func blockWhileArmed() {
	ready := make(chan struct{})
	time.AfterFunc(10*time.Millisecond, func() { close(ready) })
	<-ready
}

func openAgent(a *Agent) (net.Conn, *protocol.Conn) {
	stream := a.Connect()
	Expect(stream.SetDeadline(time.Now().Add(10 * time.Second))).To(Succeed())
	conn, _, err := protocol.Authenticate(stream, a.endpoint.Token)
	Expect(err).NotTo(HaveOccurred())
	return stream, conn
}

func armProfile(conn *protocol.Conn, id, profile string, allow bool) protocol.Armed {
	params, err := json.Marshal(profileParams{Profile: profile, Seconds: 1})
	Expect(err).NotTo(HaveOccurred())
	Expect(conn.Send("arm", protocol.Arm{Session: id, Probe: "goprof", Params: params, AllowMutation: allow})).To(Succeed())
	frame, _, err := conn.Read()
	Expect(err).NotTo(HaveOccurred())
	Expect(frame.Type).To(Equal("armed"))
	var reply protocol.Armed
	Expect(json.Unmarshal(frame.Body, &reply)).To(Succeed())
	return reply
}

var _ = Describe("SDK capture protocol", func() {
	It("keeps mutex and block profiling enabled for a heap capture until disarm", func() {
		previous := runtime.SetMutexProfileFraction(7)
		defer runtime.SetMutexProfileFraction(previous)
		a := newAgent(Options{AllowMutation: true})
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "rates", Probe: "goprof", Params: json.RawMessage(`{"profile":"heap","mutex":true,"block":true}`), AllowMutation: true})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())
		Expect(runtime.SetMutexProfileFraction(-1)).To(Equal(1))
		Expect(conn.Send("disarm", protocol.Disarm{Session: "rates"})).To(Succeed())
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("ended"))
		Expect(runtime.SetMutexProfileFraction(-1)).To(Equal(7))
	})
	It("splits large records into frames below the protocol limit", func() {
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "large", Probe: "stacks", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		data := strings.Repeat("x", maxBatchBytes/2)
		Record(context.Background(), "stacks", data)
		Record(context.Background(), "stacks", data)
		go func() { _ = conn.Send("disarm", protocol.Disarm{Session: "large"}) }()
		batches := 0
		for {
			frame, _, err := conn.Read()
			Expect(err).NotTo(HaveOccurred())
			if frame.Type == "ended" {
				break
			}
			if frame.Type == "records" {
				Expect(len(frame.Body)).To(BeNumerically("<", protocol.MaxFrameSize))
				batches++
			}
		}
		Expect(batches).To(Equal(2))
	})
	It("counts a failed batch and ends its session on connection failure", func() {
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		Expect(conn.Send("arm", protocol.Arm{Session: "broken", Probe: "stacks", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var captured *session
		active.RLock()
		for s := range active.sessions["stacks"] {
			if s.id == "broken" {
				captured = s
			}
		}
		active.RUnlock()
		Expect(captured).NotTo(BeNil())
		for i := 0; i < 128; i++ {
			Record(context.Background(), "stacks", i)
		}
		Expect(stream.Close()).To(Succeed())
		Eventually(captured.done).Should(BeClosed())
		Expect(captured.dropped.Load()).To(BeNumerically(">", 0))
	})
	It("releases connection goroutines after close", func() {
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		baseline := runtime.NumGoroutine()
		fds, err := os.ReadDir("/dev/fd")
		Expect(err).NotTo(HaveOccurred())
		fdBaseline := len(fds)
		a, err := Start(Options{Listen: "tcp"})
		Expect(err).NotTo(HaveOccurred())
		for i := 0; i < 10; i++ {
			client, err := protocol.Dial(context.Background(), "tcp", a.endpoint.Addr, a.endpoint.Token)
			Expect(err).NotTo(HaveOccurred())
			Expect(client.Close()).To(Succeed())
		}
		Expect(a.Close()).To(Succeed())
		Eventually(runtime.NumGoroutine).WithTimeout(3 * time.Second).Should(BeNumerically("<=", baseline+3))
		Eventually(func() (int, error) { fds, err := os.ReadDir("/dev/fd"); return len(fds), err }).Should(BeNumerically("<=", fdBaseline+3))
	})
	It("rejects a concurrent CPU capture from another connection", func() {
		a := Loopback()
		defer a.Close()
		firstStream, first := openAgent(a)
		defer firstStream.Close()
		secondStream, second := openAgent(a)
		defer secondStream.Close()
		Expect(armProfile(first, "cpu-first", "cpu", false).Error).To(BeEmpty())
		Expect(armProfile(second, "cpu-second", "cpu", false).Error).To(ContainSubstring("already armed"))
	})
	It("counts marshal failures without blocking the recording goroutine", func() {
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "marshal", Probe: "stacks", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		done := make(chan struct{})
		go func() { Record(context.Background(), "stacks", make(chan int)); close(done) }()
		Eventually(done).Should(BeClosed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("warn"))
		go func() { _ = conn.Send("disarm", protocol.Disarm{Session: "marshal"}) }()
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var ended protocol.Ended
		Expect(json.Unmarshal(frame.Body, &ended)).To(Succeed())
		Expect(ended.Dropped).To(Equal(uint64(1)))
	})
	It("reverts profiling when the agent receives no liveness traffic", func() {
		previous := runtime.SetMutexProfileFraction(7)
		defer runtime.SetMutexProfileFraction(previous)
		a := newAgent(Options{AllowMutation: true})
		a.readTimeout = 50 * time.Millisecond
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(armProfile(conn, "liveness", "mutex", true).Error).To(BeEmpty())
		Eventually(func() int { return runtime.SetMutexProfileFraction(-1) }).WithTimeout(time.Second).Should(Equal(7))
	})
	It("flushes every accepted record before ended on disarm", func() {
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "drain", Probe: "stacks", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		for i := 0; i < 20; i++ {
			Record(context.Background(), "stacks", i)
		}
		go func() { _ = conn.Send("disarm", protocol.Disarm{Session: "drain"}) }()
		count := 0
		for {
			frame, _, err := conn.Read()
			Expect(err).NotTo(HaveOccurred())
			if frame.Type == "ended" {
				break
			}
			if frame.Type == "records" {
				var records protocol.Records
				Expect(json.Unmarshal(frame.Body, &records)).To(Succeed())
				count += len(records.Items)
			}
		}
		Expect(count).To(Equal(20))
	})
	It("ends sessions on byte limits and stop times", func() {
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		for _, request := range []protocol.Arm{
			{Session: "bytes", Probe: "stacks", Params: json.RawMessage(`{}`), Limits: protocol.Limits{MaxBytes: 1}},
			{Session: "deadline", Probe: "stacks", Params: json.RawMessage(`{}`), StopAt: time.Now().Add(100 * time.Millisecond)},
		} {
			Expect(conn.Send("arm", request)).To(Succeed())
			frame, _, err := conn.Read()
			Expect(err).NotTo(HaveOccurred())
			Expect(frame.Type).To(Equal("armed"))
			if request.Session == "bytes" {
				Record(context.Background(), "stacks", "too large")
			}
			frame, _, err = conn.Read()
			Expect(err).NotTo(HaveOccurred())
			Expect(frame.Type).To(Equal("ended"))
			var ended protocol.Ended
			Expect(json.Unmarshal(frame.Body, &ended)).To(Succeed())
			Expect(ended.Session).To(Equal(request.Session))
		}
	})
	It("reports records dropped while a connection cannot drain its ring", func() {
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "drops", Probe: "stacks", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		for i := 0; i < 2000; i++ {
			Record(context.Background(), "stacks", i)
		}
		var captured *session
		active.RLock()
		for s := range active.sessions["stacks"] {
			if s.id == "drops" {
				captured = s
			}
		}
		active.RUnlock()
		Expect(captured).NotTo(BeNil())
		Expect(captured.dropped.Load()).To(BeNumerically(">", 0))
		go func() { _ = conn.Send("disarm", protocol.Disarm{Session: "drops"}) }()
		for {
			frame, _, err := conn.Read()
			Expect(err).NotTo(HaveOccurred())
			if frame.Type == "ended" {
				var ended protocol.Ended
				Expect(json.Unmarshal(frame.Body, &ended)).To(Succeed())
				Expect(ended.Dropped).To(BeNumerically(">", 0))
				break
			}
		}
	})
	It("delivers heap checkpoints and CPU windows as pprof blobs", func() {
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(armProfile(conn, "heap", "heap", false).Error).To(BeEmpty())
		Expect(conn.Send("checkpoint", protocol.Checkpoint{Session: "heap", ID: "reading", Params: json.RawMessage(`{"profile":"heap"}`)})).To(Succeed())
		frame, blob, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("blob"))
		Expect(blob).NotTo(BeEmpty())
		var info protocol.Blob
		Expect(json.Unmarshal(frame.Body, &info)).To(Succeed())
		Expect(info.ContentType).To(Equal(protocol.ContentTypePprof))
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("checkpointed"))
		Expect(armProfile(conn, "cpu", "cpu", false).Error).To(BeEmpty())
		frame, blob, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("blob"))
		Expect(blob).NotTo(BeEmpty())
		Expect(json.Unmarshal(frame.Body, &info)).To(Succeed())
		Expect(info.Name).To(Equal("cpu"))
	})
	It("rejects mutations without either opt-in", func() {
		a := newAgent(Options{})
		stream, conn := openAgent(a)
		Expect(armProfile(conn, "agent-denied", "mutex", true).Error).To(ContainSubstring(ErrAgentMutationDisabled.Error()))
		Expect(stream.Close()).To(Succeed())
		Expect(a.Close()).To(Succeed())
		b := newAgent(Options{AllowMutation: true})
		defer b.Close()
		stream, conn = openAgent(b)
		defer stream.Close()
		Expect(armProfile(conn, "caller-denied", "block", false).Error).To(ContainSubstring(ErrCallerMutationDisabled.Error()))
	})
	It("restores mutex and block profiling after a connection drops", func() {
		previous := runtime.SetMutexProfileFraction(7)
		defer runtime.SetMutexProfileFraction(previous)
		profileMu.Lock()
		blockBaseline = 0
		runtime.SetBlockProfileRate(0)
		profileMu.Unlock()
		a := newAgent(Options{AllowMutation: true})
		defer a.Close()
		stream, conn := openAgent(a)
		Expect(armProfile(conn, "mutex", "mutex", true).Error).To(BeEmpty())
		Expect(armProfile(conn, "block", "block", true).Error).To(BeEmpty())
		blockWhileArmed()
		var armedProfile bytes.Buffer
		Expect(pprof.Lookup("block").WriteTo(&armedProfile, 1)).To(Succeed())
		Expect(armedProfile.String()).To(ContainSubstring("blockWhileArmed"))
		Expect(stream.Close()).To(Succeed())
		Eventually(func() int { return runtime.SetMutexProfileFraction(-1) }).Should(Equal(7))
		Eventually(func() int { profileMu.Lock(); defer profileMu.Unlock(); return blockSessions }).Should(Equal(0))
		blockAfterDisconnect()
		var profile bytes.Buffer
		Expect(pprof.Lookup("block").WriteTo(&profile, 1)).To(Succeed())
		Expect(profile.String()).NotTo(ContainSubstring("blockAfterDisconnect"))
		runtime.SetBlockProfileRate(1)
		blockAfterDisconnect()
		runtime.SetBlockProfileRate(0)
		profile.Reset()
		Expect(pprof.Lookup("block").WriteTo(&profile, 1)).To(Succeed())
		Expect(profile.String()).To(ContainSubstring("blockAfterDisconnect"))
	})
})
