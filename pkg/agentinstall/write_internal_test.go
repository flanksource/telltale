package agentinstall

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recording runs each command on this host, as the local destination does,
// and keeps the argv it was given.
type recording struct {
	commands [][]string
}

func (r *recording) Run(ctx context.Context, args []string, input io.Reader) ([]byte, error) {
	r.commands = append(r.commands, args)
	return CommandTransport{Destination: Destination{Kind: "local"}}.Run(ctx, args, input)
}

var _ = Describe("staging a file on the target", func() {
	It("streams it as a tar archive, private, and moves it into place whole", func() {
		dir := GinkgoT().TempDir()
		target := filepath.Join(dir, "telltale-agent.jar")
		Expect(os.WriteFile(target, []byte("an older agent"), 0o600)).To(Succeed())
		data := bytes.Repeat([]byte("telltale-agent"), 64<<10)
		transport := &recording{}

		Expect(write(context.Background(), transport, target, data)).To(Succeed())

		Expect(os.ReadFile(target)).To(Equal(data))
		info, err := os.Stat(target)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1), "the staged file is moved, not left beside it")
		Expect(transport.commands[0][:6]).To(Equal([]string{"tar", "-x", "-m", "--no-same-owner", "-f", "-"}), "a bare dd can see end of input before a pod's exec attaches stdin")
	})
})
