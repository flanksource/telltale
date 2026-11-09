package sqltop

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSQLTop(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SQL top")
}
