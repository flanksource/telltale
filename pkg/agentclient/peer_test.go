package agentclient_test

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type peer struct {
	conn     *protocol.Conn
	incoming chan protocol.Envelope
}

func openPeer() (*agentclient.Agent, *peer) {
	client, server := net.Pipe()
	p := &peer{conn: protocol.NewConn(server), incoming: make(chan protocol.Envelope, 128)}
	go func() {
		_, _, err := p.conn.Read()
		if err != nil {
			return
		}
		if p.conn.Send("auth", protocol.Welcome{Accepted: true}) != nil {
			return
		}
		if p.conn.Send("hello", protocol.Hello{AgentID: "peer", Protocol: protocol.ProtocolVersion, Probes: []protocol.ProbeSpec{{Name: "probe"}}}) != nil {
			return
		}
		for {
			frame, _, err := p.conn.Read()
			if err != nil {
				return
			}
			p.incoming <- frame
		}
	}()
	a, err := agentclient.Connect(context.Background(), client, "")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = a.Close(); _ = server.Close() })
	return a, p
}

func (p *peer) arm(a *agentclient.Agent) *agentclient.Session {
	result := make(chan *agentclient.Session, 1)
	go func() {
		s, _ := a.Arm(context.Background(), agentclient.ArmRequest{Probe: "probe", Params: json.RawMessage(`{}`)})
		result <- s
	}()
	var frame protocol.Envelope
	Eventually(p.incoming).Should(Receive(&frame))
	var request protocol.Arm
	Expect(json.Unmarshal(frame.Body, &request)).To(Succeed())
	Expect(p.conn.Send("armed", protocol.Armed{Session: request.Session})).To(Succeed())
	var session *agentclient.Session
	Eventually(result).Should(Receive(&session))
	Expect(session).NotTo(BeNil())
	return session
}

var _ = Describe("agent multiplexing", func() {
	It("drops a full session's records without disconnecting another session", func() {
		a, p := openPeer()
		full, other := p.arm(a), p.arm(a)
		for range 80 {
			Expect(p.conn.Send("records", protocol.Records{Session: full.ID, Items: []json.RawMessage{json.RawMessage(`{"value":1}`)}})).To(Succeed())
		}
		Eventually(full.Dropped).Should(BeNumerically(">", 0))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := other.Checkpoint(ctx, json.RawMessage(`{}`)); done <- err }()
		var frame protocol.Envelope
		Eventually(p.incoming).Should(Receive(&frame))
		var request protocol.Checkpoint
		Expect(json.Unmarshal(frame.Body, &request)).To(Succeed())
		Expect(p.conn.Send("checkpointed", protocol.Checkpointed{Session: other.ID, ID: request.ID})).To(Succeed())
		Eventually(done).Should(Receive(BeNil()))
	})
	It("ends active sessions with the protocol decode error", func() {
		a, p := openPeer()
		session := p.arm(a)
		Expect(p.conn.Send("records", map[string]any{"session": session.ID, "items": "invalid"})).To(Succeed())
		var end protocol.Ended
		Eventually(session.Ended).Should(Receive(&end))
		Expect(end.Error).To(ContainSubstring("decode records"))
	})
	It("includes the underlying read error when a connection ends", func() {
		a, p := openPeer()
		session := p.arm(a)
		Expect(p.conn.Close()).To(Succeed())
		var end protocol.Ended
		Eventually(session.Ended).Should(Receive(&end))
		Expect(end.Error).To(ContainSubstring("EOF"))
	})
	It("fails a connection for an armed frame without a session", func() {
		a, p := openPeer()
		session := p.arm(a)
		Expect(p.conn.Send("armed", protocol.Armed{})).To(Succeed())
		var end protocol.Ended
		Eventually(session.Ended).Should(Receive(&end))
		Expect(end.Error).To(ContainSubstring("armed frame has no session"))
	})
	It("fails a connection on a duplicate armed frame", func() {
		a, p := openPeer()
		session := p.arm(a)
		Expect(p.conn.Send("armed", protocol.Armed{Session: session.ID})).To(Succeed())
		var end protocol.Ended
		Eventually(session.Ended).Should(Receive(&end))
		Expect(end.Error).To(ContainSubstring("duplicate armed"))
	})
	It("fails a connection on a duplicate checkpointed frame after the call returns", func() {
		a, p := openPeer()
		session := p.arm(a)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := session.Checkpoint(ctx, json.RawMessage(`{}`)); done <- err }()
		var frame protocol.Envelope
		Eventually(p.incoming).Should(Receive(&frame))
		var request protocol.Checkpoint
		Expect(json.Unmarshal(frame.Body, &request)).To(Succeed())
		reply := protocol.Checkpointed{Session: session.ID, ID: request.ID}
		Expect(p.conn.Send("checkpointed", reply)).To(Succeed())
		Eventually(done).Should(Receive(BeNil()))
		Expect(p.conn.Send("checkpointed", reply)).To(Succeed())
		var end protocol.Ended
		Eventually(session.Ended).Should(Receive(&end))
		Expect(end.Error).To(ContainSubstring("duplicate checkpointed"))
	})
	It("fails a connection on a duplicate call result after the call returns", func() {
		a, p := openPeer()
		session := p.arm(a)
		done := make(chan error, 1)
		go func() { _, err := a.Call(context.Background(), agentclient.CallRequest{Op: "read"}); done <- err }()
		var frame protocol.Envelope
		Eventually(p.incoming).Should(Receive(&frame))
		var request protocol.Call
		Expect(json.Unmarshal(frame.Body, &request)).To(Succeed())
		reply := protocol.Result{CallID: request.CallID, Value: json.RawMessage(`{}`)}
		Expect(p.conn.Send("result", reply)).To(Succeed())
		Eventually(done).Should(Receive(BeNil()))
		Expect(p.conn.Send("result", reply)).To(Succeed())
		var end protocol.Ended
		Eventually(session.Ended).Should(Receive(&end))
		Expect(end.Error).To(ContainSubstring("duplicate result"))
	})
})
