package httpdecode

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/flanksource/commons/har"
	"github.com/flanksource/telltale/internal/protocol/awswire"
)

var awsRegion = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+)+-[0-9]+$`)

func decodeAWS(e exchange) Result {
	endpoint := awsEndpoint(e.host)
	scope, scopeErr := awswire.SigningScope(e.req)
	service := first(e.options.Scope.Service, scope.Service, endpoint.Service)
	if service == "" {
		return Result{}
	}
	d := &Details{Service: service, Region: first(e.options.Scope.Region, scope.Region, endpoint.Region,
		observed(e.resp.Header.Get("X-Amz-Bucket-Region")))}
	if scopeErr != nil || e.options.InvalidScope {
		d.issue("invalid captured AWS signing scope")
	}
	result := Result{Protocol: "aws", Details: d, Operation: awsMetadata(e, d)}
	if endpoint.Service == "s3" {
		result.Protocol = "s3"
		d.Service = "s3"
		d.Bucket, d.ObjectKey = s3Resource(e.req.URL, endpoint.bucket)
		result.Operation = s3Operation(e.req, e.query, d)
	}
	return result
}

func awsMetadata(e exchange, d *Details) string {
	request := *e.req
	address := *request.URL
	query := make(url.Values, len(e.query))
	for key, values := range e.query {
		if !signingParameter(key) {
			query[key] = values
		}
	}
	address.RawQuery, request.URL = query.Encode(), &address
	request.Header = request.Header.Clone()
	request.Header.Del("Authorization")
	if observed(request.Header.Get("X-Amz-Target")) == "" {
		request.Header.Del("X-Amz-Target")
	}
	if e.request.media != "" {
		request.Header.Set("Content-Type", e.request.media)
	}
	var requestBody, responseBody []byte
	if e.request.media == "application/x-www-form-urlencoded" {
		d.Partial = d.Partial || e.request.truncated
		if !e.request.truncated {
			requestBody = e.request.bytes(d)
		}
	}
	if e.resp.StatusCode >= 400 {
		d.Partial = d.Partial || e.response.truncated
		if !e.response.truncated {
			responseBody = e.response.bytes(d)
		}
	}
	fields, err := awswire.Decode(&request, e.resp, requestBody, responseBody)
	if err != nil {
		d.issue("invalid captured AWS payload")
	}
	d.RequestID = observed(first(fields.RequestID, e.resp.Header.Get("X-Amz-Requestid")))
	header, _, _ := strings.Cut(observed(e.resp.Header.Get("X-Amzn-ErrorType")), ":")
	d.ErrorCode = first(observed(fields.ErrorCode), header)
	return fields.Operation
}

type endpoint struct {
	Service string
	Region  string
	bucket  string
}

func awsEndpoint(host string) endpoint {
	base := host
	for _, suffix := range []string{".amazonaws.com.cn", ".amazonaws.com", ".api.aws"} {
		if prefix, ok := strings.CutSuffix(host, suffix); ok {
			base = prefix
			break
		}
	}
	if base == host {
		return endpoint{}
	}
	labels := strings.Split(base, ".")
	for i := len(labels) - 1; i >= 0; i-- {
		label, region := labels[i], ""
		if strings.HasPrefix(label, "s3-") && awsRegion.MatchString(strings.TrimPrefix(label, "s3-")) {
			region, label = strings.TrimPrefix(label, "s3-"), "s3"
		}
		if label == "s3" || label == "s3-fips" || label == "s3-accelerate" {
			if region == "" {
				region = regionIn(labels[i+1:])
			}
			return endpoint{Service: "s3", Region: region, bucket: strings.Join(labels[:i], ".")}
		}
	}
	service, region := labels[0], ""
	for i, label := range labels[1:] {
		if awsRegion.MatchString(label) {
			service, region = labels[i], label
			if service == "dualstack" && i > 0 {
				service = labels[i-1]
			}
			break
		}
	}
	return endpoint{Service: strings.TrimSuffix(service, "-fips"), Region: region}
}

func regionIn(labels []string) string {
	for _, label := range labels {
		if awsRegion.MatchString(label) {
			return label
		}
	}
	return ""
}

func s3Resource(address *url.URL, bucket string) (string, string) {
	path := strings.TrimPrefix(address.EscapedPath(), "/")
	if bucket == "" {
		bucket, path, _ = strings.Cut(path, "/")
		bucket, _ = url.PathUnescape(bucket)
	}
	key, _ := url.PathUnescape(path)
	return bucket, key
}

func s3Operation(req *http.Request, query url.Values, d *Details) string {
	if d.Bucket == "" {
		if req.Method == http.MethodGet {
			return "ListBuckets"
		}
		return ""
	}
	if d.ObjectKey != "" {
		return s3ObjectOperation(req, query)
	}
	if query.Has("delete") && req.Method == http.MethodPost {
		return "DeleteObjects"
	}
	if req.Method == http.MethodGet {
		switch {
		case query.Has("uploads"):
			return "ListMultipartUploads"
		case query.Has("versions"):
			return "ListObjectVersions"
		case query.Get("list-type") == "2":
			return "ListObjectsV2"
		case plainS3Query(query):
			return "ListObjects"
		}
	}
	if plainS3Query(query) {
		return map[string]string{http.MethodHead: "HeadBucket", http.MethodPut: "CreateBucket", http.MethodDelete: "DeleteBucket"}[req.Method]
	}
	return ""
}

func s3ObjectOperation(req *http.Request, query url.Values) string {
	if query.Has("uploadId") {
		switch req.Method {
		case http.MethodGet:
			return "ListParts"
		case http.MethodPost:
			return "CompleteMultipartUpload"
		case http.MethodDelete:
			return "AbortMultipartUpload"
		case http.MethodPut:
			if query.Has("partNumber") {
				if observed(req.Header.Get("X-Amz-Copy-Source")) != "" {
					return "UploadPartCopy"
				}
				return "UploadPart"
			}
		}
		return ""
	}
	if query.Has("uploads") && req.Method == http.MethodPost {
		return "CreateMultipartUpload"
	}
	if !plainS3Query(query) {
		return ""
	}
	if req.Method == http.MethodPut && observed(req.Header.Get("X-Amz-Copy-Source")) != "" {
		return "CopyObject"
	}
	return map[string]string{http.MethodGet: "GetObject", http.MethodHead: "HeadObject", http.MethodPut: "PutObject", http.MethodDelete: "DeleteObject"}[req.Method]
}

func plainS3Query(query url.Values) bool {
	for key := range query {
		if !strings.HasPrefix(strings.ToLower(key), "x-amz-") && !strings.HasPrefix(key, "response-") &&
			!slices.Contains([]string{"versionId", "partNumber", "prefix", "delimiter", "max-keys", "marker", "encoding-type",
				"continuation-token", "start-after", "fetch-owner", "list-type", "x-id"}, key) {
			return false
		}
	}
	return true
}

func signingParameter(name string) bool {
	switch strings.ToLower(name) {
	case "x-amz-credential", "x-amz-signature", "x-amz-security-token":
		return true
	}
	return false
}

// RedactSigning removes credential values from the stored copy only.
func RedactSigning(entry *har.Entry) {
	entry.Request.URL = redactSignedURL(entry.Request.URL)
	entry.Response.RedirectURL = redactSignedURL(entry.Response.RedirectURL)
	for i, parameter := range entry.Request.QueryString {
		if signingParameter(parameter.Name) {
			entry.Request.QueryString = slices.Clone(entry.Request.QueryString)
			for j := i; j < len(entry.Request.QueryString); j++ {
				if signingParameter(entry.Request.QueryString[j].Name) {
					entry.Request.QueryString[j].Value = "[REDACTED]"
				}
			}
			break
		}
	}
	for i, header := range entry.Response.Headers {
		if strings.EqualFold(header.Name, "Location") {
			if value := redactSignedURL(header.Value); value != header.Value {
				entry.Response.Headers = slices.Clone(entry.Response.Headers)
				entry.Response.Headers[i].Value = value
			}
		}
	}
}

func redactSignedURL(value string) string {
	if !strings.Contains(value, "?") {
		return value
	}
	address, err := url.Parse(value)
	if err != nil {
		prefix, raw, _ := strings.Cut(value, "?")
		query, redacted := redactSigningQuery(raw)
		if redacted {
			return prefix + "?" + query
		}
		return value
	}
	query, redacted := redactSigningQuery(address.RawQuery)
	if !redacted {
		return value
	}
	address.RawQuery = query
	return address.String()
}

func redactSigningQuery(raw string) (string, bool) {
	if raw == "" {
		return raw, false
	}
	items := strings.Split(raw, "&")
	redacted := false
	for index, item := range items {
		key, _, _ := strings.Cut(item, "=")
		if name, err := url.QueryUnescape(key); err == nil && signingParameter(name) {
			items[index] = key + "=%5BREDACTED%5D"
			redacted = true
		}
	}
	return strings.Join(items, "&"), redacted
}
