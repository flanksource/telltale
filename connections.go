package telltale

import (
	"fmt"
	"os"

	"github.com/flanksource/commons-db/connection/store"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/models"
)

// App names telltale's own config dir, whose connections dir is the app
// layer of OpenConnections.
const App = "telltale"

// OpenConnections is telltale's connection store: the connections every
// Flanksource tool shares (<config>/flanksource/connections), then
// telltale's own (<config>/telltale/connections), then the working
// directory's (./.connections), each a dir of typed Connection resources.
func OpenConnections() (*store.Store, error) { return store.Open(App) }

// ConnectionInfo is one connection a store defines, without its URL or
// credentials.
type ConnectionInfo struct {
	// Reference is what a capture's connection param names it by.
	Reference string `json:"reference"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Type      string `json:"type"`
	// Source is the file that defines it.
	Source string `json:"source"`
}

// ListConnections describes the connections connections defines, those of
// connectionType alone when it is set.
func ListConnections(connections *store.Store, connectionType string) ([]ConnectionInfo, error) {
	defined, err := connections.ListConnections()
	if err != nil {
		return nil, err
	}
	infos := []ConnectionInfo{}
	for _, connection := range defined {
		if connectionType == "" || connection.Type == connectionType {
			infos = append(infos, connectionInfo(connection))
		}
	}
	return infos, nil
}

// GetConnection describes the connection reference resolves to in
// connections: a bare name or connection://[namespace/]name.
func GetConnection(connections *store.Store, reference string) (ConnectionInfo, error) {
	connection, err := connections.ResolveConnection(dbcontext.New(), reference)
	if err != nil {
		return ConnectionInfo{}, err
	}
	if connection == nil {
		return ConnectionInfo{}, fmt.Errorf("no connection %q in %v: %w", reference, connections.Layers(), os.ErrNotExist)
	}
	return connectionInfo(connection), nil
}

func connectionInfo(connection *models.Connection) ConnectionInfo {
	reference := "connection://" + connection.Name
	if connection.Namespace != "" {
		reference = "connection://" + connection.Namespace + "/" + connection.Name
	}
	return ConnectionInfo{
		Reference: reference, Name: connection.Name, Namespace: connection.Namespace, Type: connection.Type, Source: connection.Source,
	}
}
