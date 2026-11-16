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

type attachHomeRuntime struct{ attachment agentinstall.Attachment }

func (*attachHomeRuntime) Artifact() (agentinstall.Artifact, error) {
	return agentinstall.Artifact{Name: "fixture.jar", Data: []byte("fixture-agent")}, nil
}

func (r *attachHomeRuntime) Attach(attachment agentinstall.Attachment) []string {
	r.attachment = attachment
	return []string{"fixture-attach"}
}

type attachHomeTransport struct{}

func (attachHomeTransport) Run(_ context.Context, args []string, _ io.Reader) ([]byte, error) {
	switch args[0] {
	case "ps":
		return []byte("fixture JVM identity"), nil
	case "uname":
		return []byte("Linux"), nil
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
	It("passes the configured target JDK through the installer runtime boundary", func() {
		runtime := &attachHomeRuntime{}
		token := filepath.Join(GinkgoT().TempDir(), "agent-token")
		Expect(os.WriteFile(token, []byte("fixture-token-for-attach-home"), 0o600)).To(Succeed())
		_, err := (agentinstall.Installer{Runtime: runtime, Transport: attachHomeTransport{}}).Install(context.Background(), agentinstall.Options{
			Destination: agentinstall.Destination{Kind: "ssh", Host: "fixture-host"}, PID: 321,
			Directory: GinkgoT().TempDir(), JavaHome: "/opt/target-jdk8", HubURL: "http://localhost:8080", TokenFile: token,
		})
		Expect(errors.Is(err, errAttachRecorded)).To(BeTrue())
		Expect(runtime.attachment.JavaHome).To(Equal("/opt/target-jdk8"))
	})
})
