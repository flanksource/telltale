package telltale

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("slog capture", func() {
	It("masks sensitive log attributes before the agent sends them", func() {
		agent := Loopback()
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		DeferCleanup(stream.Close)
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "mask", Probe: "logs", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		logger := slog.New(SlogHandler(slog.NewJSONHandler(&bytes.Buffer{}, nil))).With("password", "private-password")
		logger.Info("example", "authorization", "private-token", "component", "checkout", slog.Group("credentials", "password", "private-nested"), slog.Any("payload", map[string]string{"token": "private-map"}))
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(frame.Body)).NotTo(ContainSubstring("private-password"))
		Expect(string(frame.Body)).NotTo(ContainSubstring("private-token"))
		Expect(string(frame.Body)).NotTo(ContainSubstring("private-nested"))
		Expect(string(frame.Body)).NotTo(ContainSubstring("private-map"))
		Expect(string(frame.Body)).To(ContainSubstring("checkout"))
	})
	It("raises a named level while armed and restores it on disarm", func() {
		var output bytes.Buffer
		logger := slog.New(SlogHandler(slog.NewJSONHandler(&output, nil))).With("logger", "orders")
		Expect(logger.Enabled(GinkgoT().Context(), slog.LevelDebug)).To(BeFalse())
		agent := Loopback()
		agent.options.AllowMutation = true
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		DeferCleanup(stream.Close)
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "logs", Probe: "logs", Params: json.RawMessage(`{"levels":{"orders":"debug"}}`), AllowMutation: true})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())
		Expect(logger.Enabled(GinkgoT().Context(), slog.LevelDebug)).To(BeTrue())
		logger.Debug("example message")
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(frame.Body)).To(ContainSubstring("example message"))
		Expect(conn.Send("disarm", protocol.Disarm{Session: "logs"})).To(Succeed())
		_, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(logger.Enabled(GinkgoT().Context(), slog.LevelDebug)).To(BeFalse())
	})
	It("reverts levels when the connection closes", func() {
		logger := slog.New(SlogHandler(slog.NewJSONHandler(&bytes.Buffer{}, nil))).With("logger", "orders")
		agent := Loopback()
		agent.options.AllowMutation = true
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "logs", Probe: "logs", Params: json.RawMessage(`{"levels":{"orders":"debug"}}`), AllowMutation: true})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())
		Expect(logger.Enabled(GinkgoT().Context(), slog.LevelDebug)).To(BeTrue())
		Expect(stream.Close()).To(Succeed())
		Eventually(func() bool { return logger.Enabled(GinkgoT().Context(), slog.LevelDebug) }).Should(BeFalse())
	})
	It("rejects a level change without mutation permission", func() {
		agent := Loopback()
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		DeferCleanup(stream.Close)
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "logs", Probe: "logs", Params: json.RawMessage(`{"levels":{"orders":"debug"}}`)})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(ContainSubstring("mutation disabled"))
	})
	It("allows tap-only capture without mutation permission", func() {
		agent := Loopback()
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		DeferCleanup(stream.Close)
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "tap", Probe: "logs", Params: json.RawMessage(`{}`)})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())
		logger := slog.New(SlogHandler(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
		logger.Info("tap message")
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(frame.Body)).To(ContainSubstring("tap message"))
	})
})
