package agentinstall_test

import (
	"context"
	"errors"

	"github.com/flanksource/telltale/pkg/agentinstall"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent runtime upgrades", func() {
	It("passes explicit upgrade intent to the target JVM attachment", func() {
		runtime := &attachHomeRuntime{}
		_, err := (agentinstall.Installer{Runtime: runtime, Transport: attachHomeTransport{exe: "/opt/java/bin/java"}}).Install(context.Background(), agentinstall.Options{
			Destination: agentinstall.Destination{Kind: "kubernetes", Namespace: "shop", Pod: "orders", Container: "app"},
			PID:         321, Directory: GinkgoT().TempDir(), Service: "orders", Upgrade: true,
			HTTP: &agentinstall.HTTPListener{Bind: "127.0.0.1", Port: 7070, Issuer: "fixture", Audience: "orders", JWKS: []byte(`{"keys":[]}`)},
		})
		Expect(errors.Is(err, errAttachRecorded)).To(BeTrue(), "%v", err)
		Expect(runtime.attachment.Upgrade).To(BeTrue())
	})
})
