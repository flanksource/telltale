package traces_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTraces(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Traces Suite")
}
