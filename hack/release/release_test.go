package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
		cmd.Env = append(os.Environ(), "SOURCE_SHA="+source, "RELEASE_TAG="+tag)
		return cmd.CombinedOutput()
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
		run("add", ".gitignore")
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
		Expect(run("diff-tree", "--no-commit-id", "--name-only", "-r", "v0.0.1")).To(Equal("webapp/dist/assets/app.js\nwebapp/dist/index.html"))
		Expect(run("show", "v0.0.1:webapp/dist/index.html")).To(ContainSubstring("Telltale"))
		tagCommit := run("rev-parse", "v0.0.1")
		output, err = prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(run("rev-parse", "v0.0.1")).To(Equal(tagCommit))
		Expect(run("rev-parse", "main")).To(Equal(source))
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
		Expect(run("ls-tree", "-r", "--name-only", "main")).To(Equal(".gitignore\nREADME.md"))
		Expect(run("tag", "--list", "v*")).To(Equal("v0.0.1\nv0.0.2"))
		source = firstSource
		output, err = prepare("v0.0.1")
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(run("rev-parse", "main")).NotTo(Equal(firstSource))
	})
})
