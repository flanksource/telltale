package awswire

import (
	"net/http"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAWS(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "AWS metadata") }

var _ = Describe("AWS metadata", func() {
	It("reads signing scope and JSON operation without modifying signed requests", func() {
		req, err := http.NewRequest("POST", "https://service.example.com/path%2Fkey?x=1", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=client/20261102/eu-west-1/dynamodb/aws4_request, SignedHeaders=host, Signature=secret")
		req.Header.Set("X-Amz-Target", "DynamoDB_20120810.GetItem")
		before := req.Clone(req.Context())
		got, err := Decode(req, nil, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(Metadata{Service: "dynamodb", Region: "eu-west-1", Operation: "GetItem"}))
		Expect(req).To(Equal(before))
	})
	It("reads presigned query scope and query operations", func() {
		req, err := http.NewRequest("GET", "https://service.example.com/?X-Amz-Credential=client%2F20261102%2Fus-east-1%2Fsts%2Faws4_request&Action=GetCallerIdentity", nil)
		Expect(err).NotTo(HaveOccurred())
		got, err := Decode(req, nil, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(Metadata{Service: "sts", Region: "us-east-1", Operation: "GetCallerIdentity"}))
	})
	It("reads form operations and XML errors", func() {
		req, err := http.NewRequest("POST", "https://service.example.com/", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		resp := &http.Response{StatusCode: 403, Header: http.Header{"X-Amzn-Requestid": []string{"request-1"}}}
		got, err := Decode(req, resp, []byte("Action=DescribeInstances&Version=2016-11-15"), []byte("<Response><Errors><Error><Code>Denied</Code></Error></Errors></Response>"))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(Metadata{Operation: "DescribeInstances", RequestID: "request-1", ErrorCode: "Denied"}))
	})
	It("does not invent REST operation names", func() {
		req, err := http.NewRequest("GET", "https://bucket.s3.us-east-1.amazonaws.com/object", nil)
		Expect(err).NotTo(HaveOccurred())
		got, err := Decode(req, nil, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Operation).To(BeEmpty())
	})
	It("reports malformed signing scopes and structured payloads", func() {
		req, err := http.NewRequest("GET", "https://service.example.com/?X-Amz-Credential=broken", nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = Decode(req, nil, nil, nil)
		Expect(err).To(HaveOccurred())
		req.URL.RawQuery = ""
		_, err = Decode(req, &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": []string{"application/json"}}}, nil, []byte("{"))
		Expect(err).To(HaveOccurred())
	})
})
