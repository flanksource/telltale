package groups_test

import (
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func writeFile(path, content string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
}

var _ = Describe("RepoOf", func() {
	It("finds the checkout above a directory and the branch it has checked out", func() {
		root := GinkgoT().TempDir()
		writeFile(filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/feature/top\n")
		nested := filepath.Join(root, "pkg", "deep")
		Expect(os.MkdirAll(nested, 0o755)).To(Succeed())

		Expect(groups.RepoOf(nested)).To(Equal(groups.Repo{Root: root, Branch: "feature/top"}))
	})

	It("reads a worktree's branch through its .git file, and a detached head as its short commit", func() {
		root, gitDir := GinkgoT().TempDir(), GinkgoT().TempDir()
		writeFile(filepath.Join(root, ".git"), "gitdir: "+gitDir+"\n")
		writeFile(filepath.Join(gitDir, "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")

		Expect(groups.RepoOf(root)).To(Equal(groups.Repo{Root: root, Branch: "0123456"}))
	})

	It("is empty for a directory in no checkout", func() {
		Expect(groups.RepoOf(GinkgoT().TempDir())).To(Equal(groups.Repo{}))
	})
})
