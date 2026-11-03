package tracequery

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestTraceQuery(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Recorded trace query") }
