package traces

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/commons-db/connection/store"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("a host's connections", func() {
	It("start its captures under a context that resolves the store's connections", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "orders.yaml"), []byte(
			"apiVersion: mission-control.flanksource.com/v1\nkind: Connection\nspec:\n  mssql:\n    url: {value: 'sqlserver://db:1433'}\n",
		), 0o600)).To(Succeed())
		connections, err := store.New(store.Layer{Name: store.LayerCWD, Dir: dir})
		Expect(err).ToNot(HaveOccurred())

		host, err := Open(OpenOptions{WorkDir: GinkgoT().TempDir(), RunID: RunID(time.Now()), Connections: connections})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(host.Close(context.Background())).To(Succeed()) })

		connection, err := dbcontext.HydrateConnectionByURL(host.ctx, "connection://orders")
		Expect(err).ToNot(HaveOccurred())
		Expect(connection.Type).To(Equal(models.ConnectionTypeSQLServer))
		Expect(connection.URL).To(Equal("sqlserver://db:1433"))
	})
})
