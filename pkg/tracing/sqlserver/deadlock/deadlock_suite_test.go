package deadlock

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestDeadlock(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Deadlock capture") }
