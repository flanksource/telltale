package httpdecode

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/flanksource/commons/har"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHTTPDecode(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HTTP protocol decoders")
}

var _ = DescribeTable("identifies HTTP operations",
	func(method, address, protocol, operation string) {
		result := Decode(har.Entry{Request: har.Request{Method: method, URL: address}}, Options{})
		Expect(result.Protocol).To(Equal(protocol))
		Expect(result.Operation).To(Equal(operation))
	},
	Entry("Git hosted on GitHub", "GET", "https://github.com/example/repo.git/info/refs?service=git-upload-pack", "git", "info/refs"),
	Entry("GitHub REST", "GET", "https://api.github.com/repos/example/repo/issues/12", "github", "GET /repos/{owner}/{repo}/issues/{number}"),
	Entry("Kubernetes watch", "GET", "https://cluster.example/api/v1/namespaces/default/pods?watch=1", "kubernetes", "watch"),
	Entry("Anthropic Messages", "POST", "https://api.anthropic.com/v1/messages", "anthropic", "messages"),
	Entry("OpenAI Responses", "POST", "https://api.openai.com/v1/responses", "openai", "responses"),
	Entry("S3 object", "GET", "https://archive.s3.eu-west-1.amazonaws.com/file", "s3", "GetObject"),
	Entry("AWS Query", "GET", "https://sts.eu-west-1.amazonaws.com/?Action=GetCallerIdentity", "aws", "GetCallerIdentity"),
	Entry("generic versioned route", "GET", "https://example.com/api/v1/orders", "", ""),
	Entry("generic enterprise route", "GET", "https://example.com/api/v3/repos/example/repo", "", ""),
	Entry("generic Git endpoint", "POST", "https://example.com/git-upload-pack", "", ""),
	Entry("encrypted tunnel", "CONNECT", "https://api.github.com", "", ""),
	Entry("public AI hostname lookalike", "POST", "https://api.openai.com.example/v1/responses", "", ""),
)

var _ = Describe("protocol metadata", func() {
	It("counts observed Git updates and binary pack bytes without exposing either payload", func() {
		update := strings.Repeat("a", 40) + " " + strings.Repeat("b", 40) + " refs/heads/main"
		request := fmt.Sprintf("%04x%s0000PACK\xff", len(update)+4, update)
		entry := har.Entry{Request: har.Request{
			Method: "POST", URL: "https://git.example/repo.git/git-receive-pack",
			PostData: &har.PostData{MimeType: "application/x-git-receive-pack-request", Text: base64.StdEncoding.EncodeToString([]byte(request))},
		}}
		got := Decode(entry, Options{RequestEncoding: "base64"}).Details
		Expect(got).To(Equal(&Details{Repository: "repo.git", Git: &Git{Service: "git-receive-pack", UpdatesObserved: 1, PackBytesObserved: 5}}))
	})

	It("extracts GitHub Enterprise GraphQL operation, request ID, rate limits, and an HTTP 200 error", func() {
		entry := jsonExchange("https://git.example/api/graphql", `{"operationName":"ReadIssues"}`, `{"errors":[{"type":"RATE_LIMITED"}]}`)
		entry.Response.Headers = []har.Header{{Name: "x-github-request-id", Value: "gh-req"}, {Name: "X-RateLimit-Remaining", Value: "0"}}
		result := Decode(entry, Options{})
		Expect(result.Operation).To(Equal("ReadIssues"))
		Expect(result.Details).To(Equal(&Details{ErrorCode: "RATE_LIMITED", RequestID: "gh-req",
			GitHub: &GitHub{Route: "/graphql", OperationName: "ReadIssues", RateLimitRemaining: new(int64)}}))
	})

	It("identifies Kubernetes resources and error reasons on a CRD subresource", func() {
		entry := jsonExchange("https://cluster.example/apis/products.example/v1beta1/namespaces/demo/widgets/blue/status", "", `{"kind":"Status","reason":"Forbidden"}`)
		entry.Request.Method, entry.Response.Status = "PATCH", 403
		result := Decode(entry, Options{})
		Expect(result).To(Equal(Result{Protocol: "kubernetes", Operation: "patch", Details: &Details{ErrorCode: "Forbidden",
			Kubernetes: &Kubernetes{Group: "products.example", Version: "v1beta1", Namespace: "demo", Resource: "widgets", Name: "blue", Subresource: "status"}}}))
	})

	It("takes the latest Anthropic usage counters without adding cumulative values", func() {
		entry := eventExchange("https://api.anthropic.com/v1/messages",
			`{"type":"message_start","message":{"model":"claude-test","id":"msg-example","usage":{"input_tokens":10,"cache_read_input_tokens":5}}}`,
			`{"type":"message_delta","usage":{"output_tokens":2}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			`{"type":"message_stop"}`)
		got := Decode(entry, Options{}).Details
		Expect(got).To(Equal(&Details{AI: &AI{Model: "claude-test", ResponseID: "msg-example", FinishReason: "end_turn", Stream: true,
			Usage: &Usage{InputTokens: new(int64(10)), OutputTokens: new(int64(7)), CachedInputTokens: new(int64(5))}}}))
	})

	It("uses OpenAI Responses terminal usage and Chat Completions usage chunks", func() {
		responses := eventExchange("https://api.openai.com/v1/responses",
			`{"type":"response.created","response":{"id":"resp-example","model":"gpt-test","status":"in_progress"}}`,
			`{"type":"response.completed","response":{"id":"resp-example","status":"completed","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}`)
		chat := eventExchange("https://api.openai.com/v1/chat/completions",
			`{"id":"chat-example","choices":[{"index":0,"finish_reason":"stop"}],"usage":null}`,
			`{"id":"chat-example","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`, "[DONE]")
		usage := &Usage{InputTokens: new(int64(3)), OutputTokens: new(int64(4)), TotalTokens: new(int64(7))}
		Expect(Decode(responses, Options{}).Details.AI).To(Equal(&AI{Model: "gpt-test", ResponseID: "resp-example", Status: "completed", Stream: true, Usage: usage}))
		Expect(Decode(chat, Options{}).Details.AI).To(Equal(&AI{ResponseID: "chat-example", FinishReason: "stop", Stream: true, Usage: usage}))
	})

	It("retains complete SSE frames and leaves usage absent when the terminal event was cut", func() {
		entry := eventExchange("https://api.openai.com/v1/responses",
			`{"type":"response.created","response":{"id":"resp-example","model":"gpt-test"}}`)
		entry.Response.Content.Text += `data: {"type":"response.completed","response":{"usage":`
		entry.Response.Content.Truncated = true
		Expect(Decode(entry, Options{}).Details).To(Equal(&Details{Partial: true, AI: &AI{Model: "gpt-test", ResponseID: "resp-example", Stream: true}}))
	})

	It("preserves request model metadata when captured JSON is malformed", func() {
		entry := jsonExchange("https://api.openai.com/v1/responses", `{"model":"gpt-test"}`, "{ internal text")
		Expect(Decode(entry, Options{}).Details).To(Equal(&Details{AI: &AI{Model: "gpt-test"}, Diagnostic: "invalid captured JSON"}))
	})

	It("decodes a response with counters masked by the capture source", func() {
		entry := jsonExchange("https://api.anthropic.com/v1/messages", "",
			`{"id":"msg-1","model":"claude-test","usage":{"input_tokens":"4****","output_tokens":3,"cache_read_input_tokens":"****"}}`)
		Expect(Decode(entry, Options{}).Details).To(Equal(&Details{
			AI: &AI{Model: "claude-test", ResponseID: "msg-1", Usage: &Usage{OutputTokens: new(int64(3))}},
		}))
	})

	It("uses S3 path-style addressing without normalizing object slashes or decoding twice", func() {
		entry := har.Entry{Request: har.Request{
			Method: "PUT", URL: "https://s3.us-east-2.amazonaws.com/archive/a%252Fb//c%2Fd?partNumber=2&uploadId=upload-1",
		}, Response: har.Response{Status: 403, Content: har.Content{Text: "<Error><Code>AccessDenied</Code></Error>"},
			Headers: []har.Header{{Name: "x-amz-request-id", Value: "request-1"}}}}
		result := Decode(entry, Options{})
		Expect(result).To(Equal(Result{Protocol: "s3", Operation: "UploadPart", Details: &Details{
			Service: "s3", Region: "us-east-2", Bucket: "archive", ObjectKey: "a%2Fb//c/d", ErrorCode: "AccessDenied", RequestID: "request-1"}}))
	})

	It("uses SigV4 and a JSON target on private AWS endpoints", func() {
		entry := jsonExchange("https://private.example/", "", `{"__type":"example#InvalidRequest"}`)
		entry.Response.Status = 400
		entry.Request.Headers = []har.Header{
			{Name: "X-Amz-Target", Value: "Inventory_20200101.GetItem"},
			{Name: "Authorization", Value: "AWS4-HMAC-SHA256 Credential=sample/20200101/eu-west-1/dynamodb/aws4_request, Signature=example"},
		}
		Expect(Decode(entry, Options{})).To(Equal(Result{Protocol: "aws", Operation: "GetItem",
			Details: &Details{Service: "dynamodb", Region: "eu-west-1", ErrorCode: "InvalidRequest"}}))
	})
})

func jsonExchange(address, request, response string) har.Entry {
	return har.Entry{Request: har.Request{Method: "POST", URL: address, PostData: &har.PostData{MimeType: "application/json", Text: request}},
		Response: har.Response{Status: 200, Content: har.Content{MimeType: "application/json", Text: response}}}
}

func eventExchange(address string, events ...string) har.Entry {
	entry := jsonExchange(address, "", "")
	entry.Response.Content.MimeType = "text/event-stream"
	for _, event := range events {
		entry.Response.Content.Text += "data: " + event + "\r\n\r\n"
	}
	return entry
}
