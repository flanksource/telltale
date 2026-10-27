package agentclient_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent discovery", func() {
	It("skips a discovery file older than its live process", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, strconv.Itoa(os.Getpid())+".json")
		data, err := json.Marshal(protocol.Endpoint{PID: os.Getpid(), Addr: "/example.sock", Token: "token", Started: time.Now()})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, data, 0o600)).To(Succeed())
		old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
		Expect(os.Chtimes(path, old, old)).To(Succeed())
		endpoints, err := agentclient.DiscoverIn(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(endpoints).To(BeEmpty())
		Expect(path).To(BeAnExistingFile())
	})
	It("removes dead process files while keeping live endpoints", func() {
		dir := GinkgoT().TempDir()
		child := exec.Command("sleep", "60")
		Expect(child.Start()).To(Succeed())
		pid := child.Process.Pid
		Expect(child.Process.Kill()).To(Succeed())
		_ = child.Wait()
		dead := filepath.Join(dir, strconv.Itoa(pid)+".json")
		live := filepath.Join(dir, strconv.Itoa(os.Getpid())+".json")
		for path, process := range map[string]int{dead: pid, live: os.Getpid()} {
			data, err := json.Marshal(protocol.Endpoint{PID: process, Addr: "/example.sock", Token: "token", Started: time.Now()})
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(path, data, 0o600)).To(Succeed())
		}
		endpoints, err := agentclient.DiscoverIn(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(endpoints).To(HaveLen(1))
		Expect(endpoints[0].PID).To(Equal(os.Getpid()))
		Expect(dead).NotTo(BeAnExistingFile())
	})
	It("returns malformed discovery files as errors", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o600)).To(Succeed())
		_, err := agentclient.DiscoverIn(dir)
		Expect(err).To(MatchError(ContainSubstring("agent discovery")))
	})
	It("returns unreadable discovery files as errors", func() {
		path := filepath.Join(GinkgoT().TempDir(), "unreadable.json")
		Expect(os.WriteFile(path, []byte(`{}`), 0o600)).To(Succeed())
		Expect(os.Chmod(path, 0)).To(Succeed())
		DeferCleanup(func() { Expect(os.Chmod(path, 0o600)).To(Succeed()) })
		_, err := agentclient.DiscoverIn(filepath.Dir(path))
		Expect(err).To(MatchError(ContainSubstring("permission denied")))
	})
})
