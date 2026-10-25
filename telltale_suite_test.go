package telltale_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTelltale(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "telltale") }
