package telltale

import "runtime/debug"

const modulePath = "github.com/flanksource/telltale/sdk"

// sdkVersion is the release of this module the running binary was built with,
// as the java agent reports its jar's Implementation-Version.
var sdkVersion = func() string {
	info, _ := debug.ReadBuildInfo()
	return moduleVersion(info, modulePath)
}()

// moduleVersion is path's tagged version in info, or dev when the binary was
// built from a checkout: as the main module, through a replace, or in a workspace.
func moduleVersion(info *debug.BuildInfo, path string) string {
	if info == nil {
		return "dev"
	}
	for _, module := range append([]*debug.Module{&info.Main}, info.Deps...) {
		if module.Path != path {
			continue
		}
		if module.Replace != nil {
			module = module.Replace
		}
		if module.Version == "" || module.Version == "(devel)" {
			return "dev"
		}
		return module.Version
	}
	return "dev"
}
