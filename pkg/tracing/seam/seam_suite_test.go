package seam_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSeam(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Agent Seam") }
