package main

import (
	"os"
	"path/filepath"

	"github.com/flanksource/commons-db/connection/crd"
	"github.com/flanksource/commons-db/types"
	"github.com/microsoft/go-mssqldb/msdsn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

var _ = Describe("shared lab connection", func() {
	It("normalizes the lab DSN, saves privately, and refuses to overwrite it", func() {
		dir := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CONFIG_HOME", dir)
		resource := crd.Connection{}
		resource.Name = "lab"
		resource.Spec = crd.ConnectionSpec{Type: "sql_server", URL: types.EnvVar{ValueStatic: "sqlserver://mssql.lab:31433?database=example"}}
		data, err := yaml.Marshal(resource)
		Expect(err).NotTo(HaveOccurred())
		source := filepath.Join(dir, "lab.yaml")
		Expect(os.WriteFile(source, data, 0o600)).To(Succeed())
		Expect(saveConnection(source)).To(Succeed())
		path := filepath.Join(dir, "flanksource", "connections", "mssql.yaml")
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		stored, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(yaml.UnmarshalStrict(stored, &resource)).To(Succeed())
		config, err := msdsn.Parse(resource.Spec.URL.ValueStatic)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Database).To(Equal("master"))
		Expect(resource.Name).To(Equal("mssql"))
		Expect(saveConnection(source)).To(MatchError(ContainSubstring("refusing to overwrite")))
		again, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(stored))
	})
})
