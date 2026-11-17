package agentinstall

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type processTransport struct {
	output string
	err    error
}

func (t processTransport) Run(_ context.Context, args []string, _ io.Reader) ([]byte, error) {
	Expect(args).To(Equal([]string{"sh", "-c", javaProcessesScript, "telltale-java-processes", "/proc"}))
	return []byte(t.output), t.err
}

var _ = Describe("container Java process discovery", func() {
	DescribeTable("requires exactly one readable JVM",
		func(output string, want int, message string) {
			pid, err := javaPID(context.Background(), processTransport{output: output})
			if message != "" {
				Expect(err).To(MatchError(ContainSubstring(message)))
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(pid).To(Equal(want))
			}
		},
		Entry("a JVM behind an init process", "321\n", 321, ""),
		Entry("no JVM", "", 0, "no Java process"),
		Entry("several JVMs", "321\n456\n", 0, "--pid"),
		Entry("invalid PID", "invalid\n", 0, "invalid"),
		Entry("zero PID", "0\n", 0, "invalid"),
	)

	It("reports a failed proc inspection", func() {
		_, err := javaPID(context.Background(), processTransport{err: errors.New("permission denied")})
		Expect(err).To(MatchError(ContainSubstring("permission denied")))
	})

	It("scans proc executables with no ps, jps, java or PATH Java home", func() {
		root := GinkgoT().TempDir()
		tools := filepath.Join(root, "tools")
		Expect(os.Mkdir(tools, 0o700)).To(Succeed())
		readlink, err := exec.LookPath("readlink")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Symlink(readlink, filepath.Join(tools, "readlink"))).To(Succeed())
		Expect(os.Mkdir(filepath.Join(root, "self"), 0o700)).To(Succeed())
		for pid, executable := range map[string]string{"1": "/sbin/init", "321": "/opt/Java Home/jre/bin/java", "456": "/usr/bin/sh"} {
			Expect(os.Mkdir(filepath.Join(root, pid), 0o700)).To(Succeed())
			Expect(os.Symlink(executable, filepath.Join(root, pid, "exe"))).To(Succeed())
		}
		command := exec.Command("/bin/sh", "-c", javaProcessesScript, "telltale-java-processes", root)
		command.Env = []string{"PATH=" + tools}
		output, err := command.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(string(output)).To(Equal("321\n"))
	})
})
