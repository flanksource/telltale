package deploy_test

import (
	"os"
	"path/filepath"

	"github.com/flanksource/commons-db/connection/store"
	dbcontext "github.com/flanksource/commons-db/context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/deploy"
)

var _ = Describe("the default issuer", func() {
	var connections *store.Store
	var global string

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		global = filepath.Join(dir, "flanksource", "connections")
		var err error
		connections, err = store.New(store.Layer{Name: store.LayerGlobal, Dir: global}, store.Layer{Name: store.LayerCWD, Dir: filepath.Join(dir, ".connections")})
		Expect(err).ToNot(HaveOccurred())
	})

	It("is created once, private, in the shared connections, with a key NewIssuer signs with", func() {
		created, err := deploy.EnsureIssuer(connections, deploy.DefaultIssuer)
		Expect(err).ToNot(HaveOccurred())
		Expect(created).To(BeTrue())
		info, err := os.Stat(filepath.Join(global, "issuer.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		first, err := os.ReadFile(filepath.Join(global, "issuer.yaml"))
		Expect(err).ToNot(HaveOccurred())

		issuer, err := deploy.NewIssuer(connections.Attach(dbcontext.New()), deploy.AuthSpec{Issuer: deploy.DefaultIssuer})
		Expect(err).ToNot(HaveOccurred())
		Expect(issuer.Name()).To(Equal(deploy.DefaultIssuer))

		created, err = deploy.EnsureIssuer(connections, deploy.DefaultIssuer)
		Expect(err).ToNot(HaveOccurred())
		Expect(created).To(BeFalse(), "an existing issuer keeps its key")
		again, err := os.ReadFile(filepath.Join(global, "issuer.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(again).To(Equal(first))
	})

	It("refuses a reference that names no namespace and connection", func() {
		_, err := deploy.EnsureIssuer(connections, "https://issuer.example")
		Expect(err).To(MatchError(ContainSubstring("connection://<namespace>/<name>")))
	})
})
