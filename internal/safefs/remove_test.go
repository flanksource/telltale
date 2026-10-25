package safefs

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSafeRemove(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Safe removal")
}

var _ = Describe("SafeRemove", func() {
	It("removes only a child of the allowed root", func() {
		root := GinkgoT().TempDir()
		target := filepath.Join(root, "target")
		Expect(os.Mkdir(target, 0o700)).To(Succeed())
		Expect(SafeRemove(target, RemoveContext{AllowedRoot: root})).To(Succeed())
		Expect(target).NotTo(BeADirectory())
		Expect(SafeRemove(root, RemoveContext{AllowedRoot: root})).To(HaveOccurred())
	})

	It("rejects a protected child, ancestor, and symlink escape", func() {
		root := GinkgoT().TempDir()
		protected := filepath.Join(root, "protected")
		Expect(os.Mkdir(protected, 0o700)).To(Succeed())
		guard := RemoveContext{AllowedRoot: root, Protected: []string{protected}}
		Expect(SafeRemove(protected, guard)).To(HaveOccurred())
		Expect(SafeRemove(filepath.Join(protected, "child"), guard)).To(HaveOccurred())
		Expect(SafeRemove(root, guard)).To(HaveOccurred())
		outside := GinkgoT().TempDir()
		Expect(os.Symlink(outside, filepath.Join(root, "link"))).To(Succeed())
		Expect(SafeRemove(filepath.Join(root, "link", "child"), guard)).To(HaveOccurred())
	})

	It("rejects empty paths and the working directory", func() {
		cwd, err := os.Getwd()
		Expect(err).ToNot(HaveOccurred())
		Expect(SafeRemove("", RemoveContext{AllowedRoot: cwd})).To(HaveOccurred())
		Expect(SafeRemove(cwd, RemoveContext{AllowedRoot: filepath.Dir(cwd)})).To(HaveOccurred())
	})
})
