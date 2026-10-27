package groups_test

import (
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Git repository identity", func() {
	It("joins main and linked worktrees through a relative common directory", func() {
		root := GinkgoT().TempDir()
		main, linked := filepath.Join(root, "acme"), filepath.Join(root, "feature")
		common := filepath.Join(main, ".git")
		gitDir := filepath.Join(common, "worktrees", "feature")
		writeFile(filepath.Join(common, "HEAD"), "ref: refs/heads/main\n")
		writeFile(filepath.Join(linked, ".git"), "gitdir: ../acme/.git/worktrees/feature\n")
		writeFile(filepath.Join(gitDir, "commondir"), "../..\n")
		writeFile(filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feature\n")
		mainRepo, linkedRepo := groups.RepoOf(main), groups.RepoOf(linked)
		Expect(mainRepo.Error).To(BeEmpty())
		Expect(linkedRepo.Error).To(BeEmpty())
		Expect(linkedRepo.GitCommonDir).To(Equal(mainRepo.GitCommonDir))
		Expect(mainRepo.GitCommonDir).To(Equal(common))
		Expect(linkedRepo.Root).To(Equal(linked))
		Expect(linkedRepo.Branch).To(Equal("feature"))
	})

	It("canonicalizes a checkout reached through a symlink", func() {
		root := GinkgoT().TempDir()
		checkout, alias := filepath.Join(root, "acme"), filepath.Join(root, "alias")
		writeFile(filepath.Join(checkout, ".git", "HEAD"), "ref: refs/heads/main\n")
		Expect(os.Symlink(checkout, alias)).To(Succeed())
		Expect(groups.RepoOf(alias)).To(Equal(groups.RepoOf(checkout)))
	})

	It("reports malformed Git pointers and common directory metadata", func() {
		root := GinkgoT().TempDir()
		writeFile(filepath.Join(root, ".git"), "not a gitdir pointer\n")
		Expect(groups.RepoOf(root).Error).To(ContainSubstring("gitdir"))
		Expect(groups.RepoOf(root).GitCommonDir).To(BeEmpty())
		writeFile(filepath.Join(root, ".git"), "gitdir: metadata\n")
		writeFile(filepath.Join(root, "metadata", "HEAD"), "ref: refs/heads/main\n")
		writeFile(filepath.Join(root, "metadata", "commondir"), "missing\n")
		Expect(groups.RepoOf(root).Error).To(ContainSubstring("common"))
	})
})
