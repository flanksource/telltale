package traceserve_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTraceserve(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Traceserve Suite")
}
