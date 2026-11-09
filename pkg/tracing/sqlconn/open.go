// Package sqlconn opens the SQL connection a connection-seam capture reads.
package sqlconn

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/flanksource/commons-db/connection"
	"github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
)

// labels name the connection types an OpenOptions accepts in its refusal.
var labels = map[string]string{models.ConnectionTypeSQLServer: "SQL Server", models.ConnectionTypePostgres: "Postgres"}

// OpenOptions are the connection a capture leases, the types it reads, and
// the capture named in a refusal.
type OpenOptions struct {
	Reference string
	Types     []string
	Purpose   string
}

// Open leases the connection named by options.Reference and opens a client on
// it, returning the client, the connection's type and the release that closes
// the client and gives the lease back. A connection of a type outside
// options.Types is refused.
func Open(ctx context.Context, options OpenOptions) (*sql.DB, string, func(), error) {
	conn := connection.SQLConnection{ConnectionName: options.Reference}
	if err := conn.HydrateConnection(ctx); err != nil {
		return nil, "", nil, err
	}
	if !slices.Contains(options.Types, conn.Type) {
		names := make([]string, 0, len(options.Types))
		for _, connType := range options.Types {
			names = append(names, labels[connType])
		}
		return nil, "", nil, fmt.Errorf("%s requires a %s connection; %s is a %q connection", options.Purpose, strings.Join(names, " or "), options.Reference, conn.Type)
	}
	releaseLease, err := ctx.AcquireConnectionLease(options.Reference)
	if err != nil {
		return nil, "", nil, err
	}
	client, err := conn.Client(ctx)
	if err != nil {
		releaseLease()
		return nil, "", nil, err
	}
	return client, conn.Type, func() {
		_ = client.Close()
		releaseLease()
	}, nil
}
