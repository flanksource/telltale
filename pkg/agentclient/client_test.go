package agentclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type checkpointProbe struct{}
type slowArmProbe struct {
	started  chan struct{}
	unblock  chan struct{}
	released chan struct{}
}

func (p *slowArmProbe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "slow-arm"} }
func (p *slowArmProbe) Arm(context.Context, json.RawMessage, telltale.Sink) (func() error, error) {
	close(p.started)
	<-p.unblock
	return func() error { close(p.released); return nil }, nil
}

func (checkpointProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: "client-checkpoint", Checkpoints: true}
}
func (checkpointProbe) Arm(context.Context, json.RawMessage, telltale.Sink) (func() error, error) {
	return func() error { return nil }, nil
}
func (checkpointProbe) Checkpoint(_ context.Context, _ json.RawMessage, sink telltale.Sink) error {
	if !sink.TryEmit(map[string]string{"message": "first"}) || !sink.TryEmit(map[string]string{"message": "second"}) {
		return errors.New("checkpoint records dropped")
	}
	return sink.Blob("reading", "text/plain", []byte("data"), nil)
}

var _ = Describe("agent client", func() {
	It("cancels a pending arm without disconnecting another session", func() {
		probe := &slowArmProbe{started: make(chan struct{}), unblock: make(chan struct{}), released: make(chan struct{})}
		telltale.RegisterProbe(probe)
		server := telltale.Loopback()
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		client, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(client.Close()).To(Succeed()) })
		other, err := client.Arm(context.Background(), agentclient.ArmRequest{Probe: "stacks", Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := client.Arm(ctx, agentclient.ArmRequest{Probe: "slow-arm", Params: json.RawMessage(`{}`)})
			result <- err
		}()
		Eventually(probe.started).Should(BeClosed())
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
		close(probe.unblock)
		Eventually(probe.released, 5*time.Second).Should(BeClosed())
		_, err = other.Checkpoint(context.Background(), json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
	})
	It("collects all checkpoint records and blobs before returning", func() {
		telltale.RegisterProbe(checkpointProbe{})
		server := telltale.Loopback()
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		client, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(client.Close()).To(Succeed()) })
		session, err := client.Arm(context.Background(), agentclient.ArmRequest{Probe: "client-checkpoint", Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		result, err := session.Checkpoint(context.Background(), json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Records).To(HaveLen(1))
		Expect(result.Records[0].Items).To(HaveLen(2))
		Expect(result.Blobs).To(HaveLen(1))
		Expect(string(result.Blobs[0].Data)).To(Equal("data"))
		Expect(string(result.Blobs[0].CheckpointParams)).To(MatchJSON(`{}`))
	})
	It("arms a loopback stacks probe and receives its checkpoint", func() {
		server := telltale.Loopback()
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		client, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(client.Close()).To(Succeed()) })
		session, err := client.Arm(context.Background(), agentclient.ArmRequest{Probe: "stacks", Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		result, err := session.Checkpoint(context.Background(), json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Blobs).To(HaveLen(1))
		Expect(result.Blobs[0].Info.Name).To(Equal("stacks"))
	})

	It("returns a checkpoint probe error instead of reporting success", func() {
		server := telltale.Loopback()
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		client, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(client.Close()).To(Succeed()) })
		session, err := client.Arm(context.Background(), agentclient.ArmRequest{Probe: "goprof", Params: json.RawMessage(`{"profile":"heap"}`)})
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = session.Checkpoint(ctx, json.RawMessage(`{"profile":"invalid"}`))
		Expect(err).To(MatchError(ContainSubstring("unsupported")))
	})

	It("waits for disarm and reports an unknown call", func() {
		server := telltale.Loopback()
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		client, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(client.Close()).To(Succeed()) })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		session, err := client.Arm(ctx, agentclient.ArmRequest{Probe: "stacks", Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(session.Disarm(ctx)).To(Succeed())
		_, err = client.Call(ctx, agentclient.CallRequest{Op: "unknown", Params: json.RawMessage(`{}`)})
		Expect(err).To(MatchError(ContainSubstring("unknown operation")))
	})

	It("ends an armed session when the agent connection is lost", func() {
		server := telltale.Loopback()
		client, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		session, err := client.Arm(context.Background(), agentclient.ArmRequest{Probe: "stacks", Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(server.Close()).To(Succeed())
		var ended protocol.Ended
		Eventually(session.Ended, 5*time.Second).Should(Receive(&ended))
		Expect(ended.Error).To(ContainSubstring("connection"))
		Expect(client.Close()).To(Succeed())
	})

	It("refuses mutating goprof sessions without caller consent", func() {
		server := telltale.Loopback()
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		client, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(client.Close()).To(Succeed()) })
		_, err = client.Arm(context.Background(), agentclient.ArmRequest{Probe: "goprof", Params: json.RawMessage(`{"profile":"mutex"}`), Mutates: true})
		Expect(err).To(MatchError(ContainSubstring("caller mutation disabled")))
	})
	It("enforces caller mutation consent for any probe named by the kind", func() {
		server := telltale.Loopback()
		DeferCleanup(func() { Expect(server.Close()).To(Succeed()) })
		client, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(client.Close()).To(Succeed()) })
		_, err = client.Arm(context.Background(), agentclient.ArmRequest{Probe: "stacks", Params: json.RawMessage(`{}`), Mutates: true})
		Expect(err).To(MatchError(ContainSubstring("caller mutation disabled")))
	})
})
