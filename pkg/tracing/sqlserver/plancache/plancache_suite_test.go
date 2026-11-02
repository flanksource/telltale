package plancache

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPlanCache(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SQL Server plan cache")
}
