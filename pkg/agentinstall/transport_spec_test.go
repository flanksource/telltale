package agentinstall_test

import (
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
	It("builds the Java 8 attach command from one JDK home", func() {
		args := (agentinstall.Java{Home: "/opt/jdk8"}).Attach(agentinstall.Attachment{PID: 123, Artifact: "/opt/agent.jar", Config: "/opt/config.json"})
		Expect(args).To(Equal([]string{"/opt/jdk8/bin/java", "-cp", "/opt/agent.jar:/opt/jdk8/lib/tools.jar", "com.flanksource.telltale.Attach", "123", "/opt/agent.jar", "/opt/config.json"}))
	})
})
