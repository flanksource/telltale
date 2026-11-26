package http_test

import (
	"encoding/json"
	"strings"

	"github.com/flanksource/commons/har"
	"github.com/flanksource/telltale/pkg/tracing/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("HTTP event summaries show decoded operations and resource paths",
	func(method, address, summary string) {
		wire, err := json.Marshal(http.Exchange{
			Metadata: http.Metadata{Direction: http.Outgoing},
			Entry: har.Entry{
				StartedDateTime: "2026-01-02T03:04:05Z",
				Request:         har.Request{Method: method, URL: address},
				Response:        har.Response{Status: 200, StatusText: "OK"},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		exchange, err := http.DecodeRecord(wire)
		Expect(err).NotTo(HaveOccurred())
		plain := strings.Join(strings.Fields(exchange.Pretty().String()), " ")
		Expect(plain).To(HavePrefix("→ " + summary + " 200 OK"))
		Expect(exchange.Request.Method).To(Equal(method))
		Expect(exchange.Request.URL).To(Equal(address))
		Expect(http.ToEntry(exchange).Request).To(Equal(exchange.Request))
	},
	Entry("Git", "GET", "https://git.example/repositories/relay.git/info/refs?service=git-upload-pack", "git info/refs /repositories/relay.git"),
	Entry("GitHub", "GET", "https://api.github.com/repos/example/relay/issues/42", "github GET /repos/{owner}/{repo}/issues/{number} /repos/example/relay/issues/42"),
	Entry("Kubernetes watch", "GET", "https://cluster.example/api/v1/namespaces/demo/pods/relay?watch=1", "kubernetes WATCH /demo/pods/relay"),
	Entry("Kubernetes subresource", "GET", "https://cluster.example/api/v1/namespaces/demo/pods/relay/log?tailLines=50", "kubernetes GET /demo/pods/relay/log"),
	Entry("Anthropic", "POST", "https://api.anthropic.com/v1/messages", "anthropic messages /v1/messages"),
	Entry("OpenAI", "POST", "https://api.openai.com/v1/responses", "openai responses /v1/responses"),
	Entry("virtual-hosted S3", "GET", "https://reports.s3.eu-west-1.amazonaws.com/monthly//summary%20final.csv", "s3 GetObject /reports/monthly//summary%20final.csv"),
	Entry("path-style S3", "GET", "https://s3.eu-west-1.amazonaws.com/reports?list-type=2", "s3 ListObjectsV2 /reports"),
	Entry("AWS query", "GET", "https://sts.eu-west-1.amazonaws.com/?Action=GetCallerIdentity", "aws GetCallerIdentity /"),
	Entry("AWS with an unknown operation", "POST", "https://execute-api.us-east-1.amazonaws.com/jobs?mode=once", "aws POST /jobs"),
	Entry("ordinary HTTP", "GET", "https://example.com/orders/7?view=full", "GET https://example.com/orders/7?view=full"),
)
