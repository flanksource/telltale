package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/flanksource/commons-db/connection/crd"
	"github.com/flanksource/commons-db/connection/store"
	"github.com/flanksource/commons-db/types"
	"github.com/microsoft/go-mssqldb/msdsn"
	"sigs.k8s.io/yaml"
)

func saveConnection(source string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	var resource crd.Connection
	if err := yaml.UnmarshalStrict(data, &resource); err != nil {
		return fmt.Errorf("decode connection resource: invalid YAML")
	}
	model, err := crd.ToModel(&resource)
	if err != nil {
		return fmt.Errorf("decode connection resource: invalid connection")
	}
	config, err := msdsn.Parse(model.URL)
	if err != nil {
		return fmt.Errorf("parse SQL Server connection: invalid DSN")
	}
	if config.Host != "mssql.lab" || config.Port != 31433 {
		return fmt.Errorf("expected mssql.lab:31433")
	}
	config.Database = "master"
	resource.Name, resource.Namespace = "mssql", ""
	resource.Spec = crd.ConnectionSpec{Type: "sql_server", URL: types.EnvVar{ValueStatic: config.URL().String()}}
	connections, err := store.Open("telltale")
	if err != nil {
		return err
	}
	global, err := store.ConfigDir(store.GlobalApp)
	if err != nil {
		return err
	}
	path := filepath.Join(global, "connections", "mssql.yaml")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("connection://mssql already exists; refusing to overwrite %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := connections.Save(store.LayerGlobal, &resource); err != nil {
		return err
	}
	fmt.Println("Saved connection://mssql at", path)
	return nil
}
