package agentops_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/jvm/agentops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAgentOps(t *testing.T) {
	time.Local = time.UTC
	RegisterFailHandler(Fail)
	RunSpecs(t, "JVM agent operation results")
}

func plain(pretty interface{ Pretty() api.Text }) []string {
	return strings.Split(pretty.Pretty().String(), "\n")
}

func rows[T api.TableProvider](items []T) []map[string]any {
	result := make([]map[string]any, len(items))
	for i, item := range items {
		result[i] = item.Row()
	}
	return result
}

var _ = Describe("result registry", func() {
	It("decodes every operation of this slice and refuses a result that is not its own", func() {
		Expect(agentops.Results).To(HaveLen(11))
		for op, decode := range agentops.Results {
			_, err := decode(json.RawMessage(`{}`))
			Expect(err).To(HaveOccurred(), op)
			Expect(err.Error()).To(ContainSubstring(op))
		}
	})
})
