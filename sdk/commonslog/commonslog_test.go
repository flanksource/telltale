package commonslog

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/properties"
	telltale "github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type sink struct{}

func (sink) TryEmit(any) bool                       { return true }
func (sink) Blob(string, string, []byte, any) error { return nil }
func (sink) Report(any)                             {}
func (sink) Warn(string)                            {}

func TestCommonsLog(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Commons logger adapter") }

var _ = Describe("commons logger adapter", func() {
	It("restores baseline levels after concurrent arms, releases and logger creation", func() {
		name := "concurrent-orders"
		properties.Set("log.level."+name, "warn")
		DeferCleanup(properties.Unset, "log.level."+name)
		logger.GetLogger(name)
		var workers sync.WaitGroup
		for worker := range 8 {
			workers.Go(func() {
				for n := range 100 {
					release, err := arm(telltale.LogOptions{Levels: map[string]string{"concurrent-*": "debug"}}, sink{})
					if err != nil {
						panic(err)
					}
					logger.GetLogger(fmt.Sprintf("concurrent-extra-%d-%d", worker, n%4))
					if err := release(); err != nil {
						panic(err)
					}
				}
			})
		}
		workers.Wait()
		Expect(properties.Get("log.level." + name)).To(Equal("warn"))
		Expect(logger.GetNamedLoggingLevels()[name]).To(Equal("warn"))
	})
	It("captures named debug messages and restores the prior level on disconnect", func() {
		GinkgoT().Setenv("TELLTALE_ALLOW_MUTATION", "1")
		name := "orders-test"
		properties.Set("log.level."+name, "info")
		DeferCleanup(properties.Unset, "log.level."+name)
		log := logger.GetLogger(name)
		agent := telltale.Loopback()
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "logs", Probe: "logs", Params: json.RawMessage(`{"levels":{"orders-*":"debug"}}`), AllowMutation: true})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())
		Expect(properties.Get("log.level." + name)).To(Equal("debug"))
		log.Debugf("recorded order")
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(frame.Body)).To(ContainSubstring("recorded order"))
		Expect(stream.Close()).To(Succeed())
		Eventually(func() string { return properties.Get("log.level." + name) }).Should(Equal("info"))
	})
	It("retains a second capture's level until that capture stops", func() {
		name := "overlap-example"
		properties.Set("log.level."+name, "warn")
		DeferCleanup(properties.Unset, "log.level."+name)
		logger.GetLogger(name)
		first, err := arm(telltale.LogOptions{Levels: map[string]string{name: "debug"}}, sink{})
		Expect(err).NotTo(HaveOccurred())
		second, err := arm(telltale.LogOptions{Levels: map[string]string{name: "info"}}, sink{})
		Expect(err).NotTo(HaveOccurred())
		Expect(properties.Get("log.level." + name)).To(Equal("debug"))
		Expect(first()).To(Succeed())
		Expect(properties.Get("log.level." + name)).To(Equal("info"))
		Expect(second()).To(Succeed())
		Expect(properties.Get("log.level." + name)).To(Equal("warn"))
	})
	It("applies wildcard levels to newly created loggers and restores them", func() {
		GinkgoT().Setenv("TELLTALE_ALLOW_MUTATION", "1")
		agent := telltale.Loopback()
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		DeferCleanup(stream.Close)
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "new-logger", Probe: "logs", Params: json.RawMessage(`{"levels":{"new-orders-*":"debug"}}`), AllowMutation: true})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())
		logger.GetLogger("new-orders-example")
		Expect(properties.Get("log.level.new-orders-example")).To(Equal("debug"))
		Expect(conn.Send("disarm", protocol.Disarm{Session: "new-logger"})).To(Succeed())
		_, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(properties.Get("log.level.new-orders-example")).To(BeEmpty())
	})
})
