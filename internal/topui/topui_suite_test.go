package topui

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTopUI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Top UI Suite")
}
