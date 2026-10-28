package java

import "embed"

//go:embed all:assets
var assets embed.FS

func Jar() ([]byte, error) { return assets.ReadFile("assets/telltale-agent.jar") }
