package server

import (
	"github.com/flanksource/commons-db/connection/store"
	"github.com/flanksource/telltale/pkg/datadir"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("startup storage directories", func() {
	config := []datadir.ConfigDir{
		{Name: datadir.ConfigCWD, Dir: "/work/.telltale"},
		{Name: datadir.ConfigGlobal, Dir: "/home/dev/.config/telltale"},
	}
	connections := []store.Layer{
		{Name: "global", Dir: "/home/dev/.config/flanksource/connections"},
		{Name: "app", Dir: "/home/dev/.config/telltale/connections"},
		{Name: "cwd", Dir: "/work/.connections"},
	}

	It("prints every store serve opened, then the config dirs in lookup order, in one aligned block", func() {
		dirs := storageDirs{
			Runs: "/cache/telltale/traces", Spans: "/cache/telltale/otel/spans/records.sqlite",
			RuleEvents: "/cache/telltale/monitor/events.sqlite", Config: config, Connections: connections,
		}
		Expect(dirs.String()).To(Equal(`Telltale storage:
  runs                  /cache/telltale/traces
  OTLP spans            /cache/telltale/otel/spans/records.sqlite
  rule events           /cache/telltale/monitor/events.sqlite
  config (cwd)          /work/.telltale
  config (global)       /home/dev/.config/telltale
  connections (global)  /home/dev/.config/flanksource/connections
  connections (app)     /home/dev/.config/telltale/connections
  connections (cwd)     /work/.connections
`))
	})

	It("leaves out the OTLP and rules stores serve did not open", func() {
		dirs := storageDirs{Runs: "/cache/telltale/traces", Config: config[1:], Connections: connections[2:]}
		Expect(dirs.String()).To(Equal(`Telltale storage:
  runs                  /cache/telltale/traces
  config (global)       /home/dev/.config/telltale
  connections (cwd)     /work/.connections
`))
	})
})
