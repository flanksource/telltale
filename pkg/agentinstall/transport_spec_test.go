package agentinstall_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/agentinstall"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("installation target validation", func() {
	DescribeTable("rejects unsafe or incomplete destinations", func(d agentinstall.Destination) { Expect(d.Validate()).To(HaveOccurred()) },
		Entry("missing kind", agentinstall.Destination{}),
		Entry("SSH option injection", agentinstall.Destination{Kind: "ssh", Host: "-oProxyCommand=example"}),
		Entry("ambiguous Kubernetes container", agentinstall.Destination{Kind: "kubernetes", Namespace: "example", Pod: "example"}),
		Entry("local with remote identity", agentinstall.Destination{Kind: "local", Host: "example"}),
	)
	It("hands a Kubernetes command its input on stdin, as the agent jar is staged with dd", func() {
		bin := GinkgoT().TempDir()
		// kubectl stands in for the pod: it answers with the digest of what it reads.
		Expect(os.WriteFile(filepath.Join(bin, "kubectl"), []byte("#!/bin/sh\nexec shasum -a 256\n"), 0o755)).To(Succeed())
		GinkgoT().Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		jar := bytes.Repeat([]byte("telltale-agent"), 4096)
		sum := sha256.Sum256(jar)
		out, err := agentinstall.CommandTransport{Destination: agentinstall.Destination{Kind: "kubernetes", Namespace: "shop", Pod: "orders-0", Container: "app"}}.
			Run(context.Background(), []string{"dd", "of=/tmp/agent.jar"}, bytes.NewReader(jar))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).To(HavePrefix(hex.EncodeToString(sum[:]) + " "))
	})
	It("builds the Java 8 attach command from one JDK home", func() {
		args := (agentinstall.Java{}).Attach(agentinstall.Attachment{PID: 123, Artifact: "/opt/agent.jar", Config: "/opt/config.json", JavaHome: "/opt/jdk8"})
		Expect(args).To(Equal([]string{"/opt/jdk8/bin/java", "-cp", "/opt/agent.jar:/opt/jdk8/lib/tools.jar", "com.flanksource.telltale.Attach", "123", "/opt/agent.jar", "/opt/config.json"}))
	})
})
