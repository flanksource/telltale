//go:build linux

package agentinstall

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("live Linux Java discovery", func() {
	It("finds the JVM behind init and its home with only proc inspection tools on PATH", func() {
		java, err := exec.LookPath("java")
		Expect(err).NotTo(HaveOccurred())
		java, err = filepath.EvalSymlinks(java)
		Expect(err).NotTo(HaveOccurred())
		directory := GinkgoT().TempDir()
		output, err := exec.Command("javac", "-d", directory, "testdata/Workload.java").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		child := exec.Command(java, "-cp", directory, "Workload")
		Expect(child.Start()).To(Succeed())
		DeferCleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		tools := filepath.Join(directory, "tools")
		Expect(os.Mkdir(tools, 0o700)).To(Succeed())
		for _, name := range []string{"sh", "readlink", "cat"} {
			path, err := exec.LookPath(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.Symlink(path, filepath.Join(tools, name))).To(Succeed())
		}
		GinkgoT().Setenv("PATH", tools)
		GinkgoT().Setenv("JAVA_HOME", "/not-the-running-jvm")
		transport := CommandTransport{Destination: Destination{Kind: "local"}}
		pid, err := javaPID(context.Background(), transport)
		Expect(err).NotTo(HaveOccurred())
		Expect(pid).To(Equal(child.Process.Pid))
		home, err := targetJavaHome(context.Background(), transport, pid)
		Expect(err).NotTo(HaveOccurred())
		Expect(home).To(Equal(filepath.Dir(filepath.Dir(java))))
		identity, err := processIdentity(context.Background(), transport, true, pid)
		Expect(err).NotTo(HaveOccurred())
		Expect(identity).NotTo(BeEmpty())
	})
})
