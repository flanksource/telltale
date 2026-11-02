package datadir_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/datadir"
)

func TestDatadir(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Datadir Suite")
}

var _ = Describe("telltale's data and config dirs", func() {
	const home = "/home/dev"

	DescribeTable("resolve under the XDG dir when it is set, else the OS default",
		func(resolve func() (string, error), xdgVar, xdgValue, want string) {
			GinkgoT().Setenv("HOME", home)
			GinkgoT().Setenv(xdgVar, xdgValue)
			Expect(resolve()).To(Equal(want))
		},
		Entry("cache, XDG_CACHE_HOME set", datadir.Cache, "XDG_CACHE_HOME", "/xdg/cache", "/xdg/cache/telltale"),
		Entry("cache, XDG_CACHE_HOME unset", datadir.Cache, "XDG_CACHE_HOME", "", osCacheDir(home)),
		Entry("config, XDG_CONFIG_HOME set", datadir.Config, "XDG_CONFIG_HOME", "/xdg/config", "/xdg/config/telltale"),
		Entry("config, XDG_CONFIG_HOME unset", datadir.Config, "XDG_CONFIG_HOME", "", filepath.Join(home, ".config", "telltale")),
	)

	DescribeTable("fail when neither the XDG var nor HOME resolves",
		func(resolve func() (string, error), xdgVar string) {
			GinkgoT().Setenv("HOME", "")
			GinkgoT().Setenv(xdgVar, "")
			_, err := resolve()
			Expect(err).To(MatchError(ContainSubstring("telltale")))
		},
		Entry("cache", datadir.Cache, "XDG_CACHE_HOME"),
		Entry("config", datadir.Config, "XDG_CONFIG_HOME"),
	)

	Describe("config dirs", func() {
		var cwd, global string

		BeforeEach(func() {
			cwd, global = GinkgoT().TempDir(), GinkgoT().TempDir()
			GinkgoT().Chdir(cwd)
			GinkgoT().Setenv("XDG_CONFIG_HOME", global)
			// macOS temp dirs are reached through the /var -> /private/var link.
			var err error
			cwd, err = os.Getwd()
			Expect(err).ToNot(HaveOccurred())
		})

		It("are the working directory's .telltale, then the global config dir", func() {
			Expect(datadir.ConfigDirs()).To(Equal([]datadir.ConfigDir{
				{Name: datadir.ConfigCWD, Dir: filepath.Join(cwd, ".telltale")},
				{Name: datadir.ConfigGlobal, Dir: filepath.Join(global, "telltale")},
			}))
		})

		DescribeTable("find a config file in the first dir that has it",
			func(inCWD, inGlobal bool, want func() string) {
				for _, dir := range []struct {
					path string
					has  bool
				}{{filepath.Join(cwd, ".telltale"), inCWD}, {filepath.Join(global, "telltale"), inGlobal}} {
					if dir.has {
						Expect(os.MkdirAll(dir.path, 0o700)).To(Succeed())
						Expect(os.WriteFile(filepath.Join(dir.path, "groups.yaml"), nil, 0o600)).To(Succeed())
					}
				}
				dirs, err := datadir.ConfigDirs()
				Expect(err).ToNot(HaveOccurred())
				Expect(datadir.ConfigFile(dirs, "groups.yaml")).To(Equal(want()))
			},
			Entry("the working directory's overrides the global one", true, true, func() string { return filepath.Join(cwd, ".telltale", "groups.yaml") }),
			Entry("the global one when the working directory has none", false, true, func() string { return filepath.Join(global, "telltale", "groups.yaml") }),
			Entry("none when neither has it", false, false, func() string { return "" }),
		)
	})
})

// osCacheDir is os.UserCacheDir's telltale dir for home on the platforms
// telltale runs on.
func osCacheDir(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches", "telltale")
	}
	return filepath.Join(home, ".cache", "telltale")
}
