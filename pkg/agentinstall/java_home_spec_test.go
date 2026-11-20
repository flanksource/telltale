package agentinstall_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/agentinstall"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var errAttachRecorded = errors.New("fixture attach recorded")

// linuxStat is a JVM's /proc/<pid>/stat: its command holds a space and a
// parenthesis, and its start time (field 22) is 2318867.
const linuxStat = "321 (java (main)) S 0 321 321 0 -1 4194560 950 0 0 0 1 2 0 0 20 0 41 0 2318867 5000000 3000 18446744073709551615\n"

type attachHomeRuntime struct{ attachment agentinstall.Attachment }

func (*attachHomeRuntime) Artifact() (agentinstall.Artifact, error) {
	return agentinstall.Artifact{Name: "fixture.jar", Data: []byte("fixture-agent")}, nil
}

func (r *attachHomeRuntime) Attach(attachment agentinstall.Attachment) []string {
	r.attachment = attachment
	return []string{"fixture-attach"}
}

// attachHomeTransport is a Linux target whose JVM runs exe.
type attachHomeTransport struct{ exe, processes string }

func (t attachHomeTransport) Run(_ context.Context, args []string, _ io.Reader) ([]byte, error) {
	switch args[0] {
	case "sh":
		if args[3] == "telltale-java-runtime" {
			return []byte("__TELLTALE_PRESENT__=true\nJAVA_VERSION=\"1.8.0_512\"\n__TELLTALE_TOOLS__=true\n"), nil
		}
		return []byte(t.processes), nil
	case "cat":
		if args[1] != "/proc/321/stat" {
			return nil, fmt.Errorf("cat of %q, not the target JVM's stat", args[1])
		}
		return []byte(linuxStat), nil
	case "uname":
		return []byte("Linux"), nil
	case "readlink":
		if args[1] != "/proc/321/exe" {
			return nil, fmt.Errorf("readlink of %q, not the target JVM's executable", args[1])
		}
		return []byte(t.exe + "\n"), nil
	case "sha256sum":
		return []byte(fmt.Sprintf("%x  %s", sha256.Sum256([]byte("fixture-agent")), args[1])), nil
	case "fixture-attach":
		return nil, errAttachRecorded
	case "install", "tar", "mv", "rm":
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected installer command %q", args[0])
	}
}

var _ = Describe("target-compatible Java attachment", func() {
	install := func(transport attachHomeTransport, javaHome string, pid ...int) (agentinstall.Attachment, error) {
		runtime := &attachHomeRuntime{}
		token := filepath.Join(GinkgoT().TempDir(), "agent-token")
		Expect(os.WriteFile(token, []byte("fixture-token-for-attach-home"), 0o600)).To(Succeed())
		options := agentinstall.Options{
			Destination: agentinstall.Destination{Kind: "kubernetes", Namespace: "shop", Pod: "orders", Container: "app"}, PID: 321,
			Directory: GinkgoT().TempDir(), JavaHome: javaHome, HubURL: "http://localhost:8080", TokenFile: token,
		}
		if len(pid) > 0 {
			options.PID = pid[0]
		}
		_, err := (agentinstall.Installer{Runtime: runtime, Transport: transport}).Install(context.Background(), options)
		return runtime.attachment, err
	}

	It("passes the configured target JDK through the installer runtime boundary", func() {
		attachment, err := install(attachHomeTransport{exe: "/usr/lib/jvm/other/bin/java"}, "/opt/target-jdk8")
		Expect(errors.Is(err, errAttachRecorded)).To(BeTrue())
		Expect(attachment.JavaHome).To(Equal("/opt/target-jdk8"))
	})

	DescribeTable("defaults the target JDK to the one the JVM runs, so a Java 8 helper finds tools.jar",
		func(exe, home string) {
			attachment, err := install(attachHomeTransport{exe: exe}, "")
			Expect(errors.Is(err, errAttachRecorded)).To(BeTrue(), fmt.Sprint(err))
			Expect(attachment.JavaHome).To(Equal(home))
		},
		Entry("a JDK's own java", "/usr/lib/jvm/temurin-8-jdk-amd64/bin/java", "/usr/lib/jvm/temurin-8-jdk-amd64"),
		Entry("a Java 8 JDK's jre/bin/java", "/usr/lib/jvm/temurin-8-jdk-amd64/jre/bin/java", "/usr/lib/jvm/temurin-8-jdk-amd64"),
		Entry("a JDK path with spaces", "/opt/Java Home/bin/java", "/opt/Java Home"),
	)

	It("discovers the container's JVM behind PID 1 and uses its Java home", func() {
		attachment, err := install(attachHomeTransport{exe: "/opt/java/bin/java", processes: "321\n"}, "", 0)
		Expect(errors.Is(err, errAttachRecorded)).To(BeTrue(), fmt.Sprint(err))
		Expect(attachment).To(And(HaveField("PID", 321), HaveField("JavaHome", "/opt/java")))
	})

	It("refuses several container JVMs instead of picking one", func() {
		_, err := install(attachHomeTransport{processes: "321\n456\n"}, "", 0)
		Expect(err).To(MatchError(ContainSubstring("--pid")))
	})

	It("identifies a Linux JVM by its /proc start time, so an image without ps attaches", func() {
		attachment, err := install(attachHomeTransport{exe: "/usr/local/openjdk-8/bin/java"}, "")
		Expect(errors.Is(err, errAttachRecorded)).To(BeTrue(), fmt.Sprint(err))
		Expect(attachment.JavaHome).To(Equal("/usr/local/openjdk-8"))
	})

	It("refuses a JVM executable it cannot place in a JDK, naming --java-home", func() {
		_, err := install(attachHomeTransport{exe: "/opt/app/launcher"}, "")
		Expect(err).To(MatchError(ContainSubstring("--java-home")))
	})
})
