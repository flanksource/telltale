package java

import "embed"

//go:embed all:assets
var assets embed.FS

func Jar() ([]byte, error) { return assets.ReadFile("assets/telltale-agent.jar") }

// War is telltale-diag.war, which the tomcat deployer hot-deploys.
func War() ([]byte, error) { return assets.ReadFile("assets/telltale-diag.war") }
