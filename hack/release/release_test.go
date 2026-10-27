package release_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

func TestRelease(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Release graph") }

var _ = Describe("release-only assets", func() {
	var directory, script, source string
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = directory
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		return strings.TrimSpace(string(output))
	}
	prepare := func(tag string) ([]byte, error) {
		cmd := exec.Command("bash", script)
		cmd.Dir = directory
		cmd.Env = append(os.Environ(), "SOURCE_SHA="+source, "RELEASE_TAG="+tag, "GOWORK="+filepath.Join(directory, "go.work.ci"))
		return cmd.CombinedOutput()
	}
	selectVersion := func() string {
		selector, err := filepath.Abs("../release-version.sh")
		Expect(err).NotTo(HaveOccurred())
		cmd := exec.Command("bash", selector)
		cmd.Dir = directory
		cmd.Env = append(os.Environ(), "SOURCE_SHA="+source)
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		return strings.TrimSpace(string(output))
	}
	BeforeEach(func() {
		var err error
		script, err = filepath.Abs("../release.sh")
		Expect(err).NotTo(HaveOccurred())
		scratch, err := filepath.Abs("../../.tmp")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.MkdirAll(scratch, 0755)).To(Succeed())
		directory, err = os.MkdirTemp(scratch, "release-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(directory)).To(Succeed()) })
		run("init", "-b", "main")
		run("config", "user.name", "Release Test")
		run("config", "user.email", "release@example.com")
		Expect(os.WriteFile(filepath.Join(directory, ".gitignore"), []byte("webapp/dist/\n"), 0644)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(directory, "sdk/adapter"), 0755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(directory, "hack/modsum"), 0755)).To(Succeed())
		tool, err := os.ReadFile("../modsum/main.go")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(directory, "hack/modsum/main.go"), tool, 0644)).To(Succeed())
		rootSum, err := os.ReadFile("../../go.sum")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(directory, "go.sum"), rootSum, 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "go.mod"), []byte("module github.com/flanksource/telltale\n\ngo 1.27.1\n\nrequire (\n github.com/flanksource/telltale/sdk v0.0.0\n github.com/flanksource/telltale/sdk/adapter v0.0.0\n golang.org/x/mod v0.37.0\n)\n"), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "sdk/go.mod"), []byte("module github.com/flanksource/telltale/sdk\n\ngo 1.27.1\n"), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "sdk/core.go"), []byte("package sdk\n"), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "sdk/adapter/go.mod"), []byte("module github.com/flanksource/telltale/sdk/adapter\n\ngo 1.27.1\n\nrequire github.com/flanksource/telltale/sdk v0.0.0\n"), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "sdk/adapter/adapter.go"), []byte("package adapter\n\nimport _ \"github.com/flanksource/telltale/sdk\"\n"), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "go.work.ci"), []byte("go 1.27.1\n\nuse (\n .\n ./sdk\n ./sdk/adapter\n)\n\nreplace github.com/flanksource/telltale/sdk v0.0.0 => ./sdk\nreplace github.com/flanksource/telltale/sdk/adapter v0.0.0 => ./sdk/adapter\n"), 0644)).To(Succeed())
		run("add", ".gitignore", "go.mod", "go.sum", "go.work.ci", "sdk", "hack/modsum/main.go")
		run("commit", "-m", "chore: initialize")
		source = run("rev-parse", "HEAD")
		Expect(os.MkdirAll(filepath.Join(directory, "webapp/dist/assets"), 0755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "webapp/dist/index.html"), []byte("<html>Telltale</html>"), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "webapp/dist/assets/app.js"), []byte("export default 'telltale'"), 0644)).To(Succeed())
	})

	It("tags one asset commit without moving main", func() {
		output, err := prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(run("rev-parse", "main")).To(Equal(source))
		Expect(run("rev-parse", "v0.0.1^")).To(Equal(source))
		Expect(run("rev-parse", "sdk/v0.0.1")).To(Equal(run("rev-parse", "v0.0.1")))
		Expect(run("rev-parse", "sdk/adapter/v0.0.1")).To(Equal(run("rev-parse", "v0.0.1")))
		Expect(run("show", "v0.0.1:go.mod")).To(ContainSubstring("github.com/flanksource/telltale/sdk/adapter v0.0.1"))
		Expect(run("show", "v0.0.1:sdk/adapter/go.mod")).To(ContainSubstring("github.com/flanksource/telltale/sdk v0.0.1"))
		Expect(run("show", "v0.0.1:webapp/dist/index.html")).To(ContainSubstring("Telltale"))
		tagCommit := run("rev-parse", "v0.0.1")
		output, err = prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(run("rev-parse", "v0.0.1")).To(Equal(tagCommit))
		Expect(run("rev-parse", "main")).To(Equal(source))
	})

	It("refuses to move an existing SDK tag", func() {
		run("tag", "sdk/adapter/v0.0.1")
		output, err := prepare("v0.0.1")
		Expect(err).To(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("already exists"))
	})

	It("preserves non-SDK replacements and removes SDK replacements", func() {
		modPath := filepath.Join(directory, "go.mod")
		mod, err := os.ReadFile(modPath)
		Expect(err).NotTo(HaveOccurred())
		mod = append(mod, []byte("\nrequire github.com/glebarez/sqlite v1.11.0\nreplace github.com/glebarez/sqlite => github.com/clarkmcc/gorm-sqlite v0.0.0-20240426202654-00ed082c0311\nreplace github.com/flanksource/telltale/sdk => ./sdk\nreplace github.com/flanksource/telltale/sdk/adapter v0.0.0 => ./sdk/adapter\nreplace github.com/flanksource/telltale/sdkx v1.0.0 => ./sdk/adapter\n")...)
		Expect(os.WriteFile(modPath, mod, 0644)).To(Succeed())
		run("add", "go.mod")
		run("commit", "-m", "test: add replacement")
		source = run("rev-parse", "HEAD")
		output, err := prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		released := run("show", "v0.0.1:go.mod")
		Expect(released).To(ContainSubstring("replace github.com/glebarez/sqlite => github.com/clarkmcc/gorm-sqlite v0.0.0-20240426202654-00ed082c0311"))
		Expect(released).To(ContainSubstring("replace github.com/flanksource/telltale/sdkx v1.0.0 => ./sdk/adapter"))
		Expect(released).NotTo(ContainSubstring("github.com/flanksource/telltale/sdk =>"))
		Expect(released).NotTo(ContainSubstring("github.com/flanksource/telltale/sdk/adapter v0.0.0 =>"))
	})

	It("selects the patch after the highest stable root or SDK version", func() {
		Expect(selectVersion()).To(Equal("v0.0.1"))
		run("tag", "v1.9.9")
		run("tag", "sdk/adapter/v1.10.2")
		run("tag", "sdk/adapter/v9.0.0-rc.1")
		Expect(selectVersion()).To(Equal("v1.10.3"))
		run("tag", "v2.0.4")
		Expect(selectVersion()).To(Equal("v2.0.5"))
	})

	It("selects a new version after verification leaves SDK tags for an older source", func() {
		output, err := prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		run("tag", "-d", "v0.0.1")
		Expect(selectVersion()).To(Equal("v0.0.1"))
		run("checkout", "main")
		Expect(os.WriteFile(filepath.Join(directory, "README.md"), []byte("New source\n"), 0644)).To(Succeed())
		run("add", "README.md")
		run("commit", "-m", "feat: new source")
		source = run("rev-parse", "HEAD")
		Expect(selectVersion()).To(Equal("v0.0.2"))
		Expect(os.MkdirAll(filepath.Join(directory, "webapp/dist"), 0755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "webapp/dist/index.html"), []byte("<html>New release</html>"), 0644)).To(Succeed())
		output, err = prepare(selectVersion())
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(run("rev-parse", "v0.0.2^")).To(Equal(source))
	})

	It("requires workspace replacements to resolve unpublished placeholder versions", func() {
		workspace := filepath.Join(directory, "go.work.ci")
		original, err := os.ReadFile(workspace)
		Expect(err).NotTo(HaveOccurred())
		without := strings.Split(string(original), "\nreplace ")[0] + "\n"
		Expect(os.WriteFile(workspace, []byte(without), 0644)).To(Succeed())
		cmd := exec.Command("go", "list", "-m", "all")
		cmd.Dir = directory
		cmd.Env = append(os.Environ(), "GOWORK="+workspace, "GOPROXY=off", "GOSUMDB=off")
		output, err := cmd.CombinedOutput()
		Expect(err).To(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("github.com/flanksource/telltale/sdk@v0.0.0"))
		Expect(os.WriteFile(workspace, original, 0644)).To(Succeed())
		cmd = exec.Command("go", "list", "-m", "all")
		cmd.Dir = directory
		cmd.Env = append(os.Environ(), "GOWORK="+workspace, "GOPROXY=off", "GOSUMDB=off")
		output, err = cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
	})

	It("declares the hub's websocket dependency as direct", func() {
		data, err := os.ReadFile("../../go.mod")
		Expect(err).NotTo(HaveOccurred())
		file, err := modfile.Parse("go.mod", data, nil)
		Expect(err).NotTo(HaveOccurred())
		var direct bool
		for _, requirement := range file.Require {
			if requirement.Mod.Path == "github.com/gorilla/websocket" {
				direct = !requirement.Indirect
			}
		}
		Expect(direct).To(BeTrue())
		hub, err := os.ReadFile("../../pkg/agentclient/hub.go")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(hub)).To(ContainSubstring(`"github.com/gorilla/websocket"`))
	})

	It("reuses an SDK-tagged commit when root publication was interrupted", func() {
		output, err := prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		commit := run("rev-parse", "sdk/v0.0.1")
		run("tag", "-d", "v0.0.1")
		output, err = prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(string(output)).To(ContainSubstring("Reusing v0.0.1"))
		Expect(run("rev-parse", "v0.0.1")).To(Equal(commit))
	})

	It("matches checksums downloaded from a file module proxy", func() {
		output, err := prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		rootSum := run("show", "v0.0.1:go.sum")
		adapterSum := run("show", "v0.0.1:sdk/adapter/go.sum")
		proxy := filepath.Join(directory, ".tmp", "proxy")
		for _, subdir := range []string{"sdk", "sdk/adapter"} {
			path := "github.com/flanksource/telltale/" + subdir
			version := module.Version{Path: path, Version: "v0.0.1"}
			versionDir := filepath.Join(proxy, path, "@v")
			Expect(os.MkdirAll(versionDir, 0755)).To(Succeed())
			archive, err := os.Create(filepath.Join(versionDir, "v0.0.1.zip"))
			Expect(err).NotTo(HaveOccurred())
			Expect(modzip.CreateFromVCS(archive, version, directory, "v0.0.1", subdir)).To(Succeed())
			Expect(archive.Close()).To(Succeed())
			mod := run("show", "v0.0.1:"+subdir+"/go.mod") + "\n"
			Expect(os.WriteFile(filepath.Join(versionDir, "v0.0.1.mod"), []byte(mod), 0644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(versionDir, "v0.0.1.info"), []byte(`{"Version":"v0.0.1","Time":"2026-01-01T00:00:00Z"}`), 0644)).To(Succeed())
			cmd := exec.Command("go", "mod", "download", "-json", path+"@v0.0.1")
			cmd.Dir = directory
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-modcacherw", "GOSUMDB=off", "GOPROXY=file://"+proxy, "GOMODCACHE="+filepath.Join(directory, ".tmp", "modcache"))
			output, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(output))
			var result struct{ Sum, GoModSum string }
			Expect(json.Unmarshal(output, &result)).To(Succeed())
			Expect(rootSum).To(ContainSubstring(fmt.Sprintf("%s v0.0.1 %s", path, result.Sum)))
			Expect(rootSum).To(ContainSubstring(fmt.Sprintf("%s v0.0.1/go.mod %s", path, result.GoModSum)))
			if subdir == "sdk" {
				Expect(adapterSum).To(ContainSubstring(fmt.Sprintf("%s v0.0.1 %s", path, result.Sum)))
			}
		}
		cmd := exec.Command("go", "test", "github.com/flanksource/telltale/sdk/adapter")
		cmd.Dir = directory
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly -modcacherw", "GOSUMDB=off", "GOPROXY=file://"+proxy+",https://proxy.golang.org", "GOMODCACHE="+filepath.Join(directory, ".tmp", "modcache"))
		output, err = cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
	})

	It("rejects source changes before creating a release commit", func() {
		Expect(os.WriteFile(filepath.Join(directory, ".gitignore"), []byte("changed\n"), 0644)).To(Succeed())
		output, err := prepare("v0.0.1")
		Expect(err).To(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("working tree"))
		Expect(run("rev-parse", "HEAD")).To(Equal(source))
	})

	It("rejects an existing tag targeting another source", func() {
		run("tag", "v0.0.1")
		output, err := prepare("v0.0.1")
		Expect(err).To(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("already exists"))
	})

	It("creates successive tags above their source revisions without merging assets into main", func() {
		firstSource := source
		output, err := prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		run("checkout", "main")
		Expect(os.WriteFile(filepath.Join(directory, "README.md"), []byte("Next source revision\n"), 0644)).To(Succeed())
		run("add", "README.md")
		run("commit", "-m", "feat: next source revision")
		source = run("rev-parse", "HEAD")
		Expect(os.MkdirAll(filepath.Join(directory, "webapp/dist/assets"), 0755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, "webapp/dist/index.html"), []byte("<html>Next release</html>"), 0644)).To(Succeed())
		output, err = prepare("v0.0.2")
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(run("rev-parse", "main")).To(Equal(source))
		Expect(run("rev-parse", "v0.0.2^")).To(Equal(source))
		Expect(run("ls-tree", "-r", "--name-only", "main")).NotTo(ContainSubstring("webapp/dist/index.html"))
		Expect(run("tag", "--list", "v*")).To(Equal("v0.0.1\nv0.0.2"))
		source = firstSource
		output, err = prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(run("rev-parse", "main")).NotTo(Equal(firstSource))
	})
})
