package httpdecode

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/flanksource/commons/har"
	"github.com/flanksource/telltale/internal/protocol/awswire"
)

type Result struct {
	Protocol  string
	Operation string
	Details   *Details
}

type Details struct {
	Service    string      `json:"service,omitempty"`
	Region     string      `json:"region,omitempty"`
	RequestID  string      `json:"requestId,omitempty"`
	ErrorCode  string      `json:"errorCode,omitempty"`
	Repository string      `json:"repository,omitempty"`
	Bucket     string      `json:"bucket,omitempty"`
	ObjectKey  string      `json:"objectKey,omitempty"`
	Git        *Git        `json:"git,omitempty"`
	GitHub     *GitHub     `json:"github,omitempty"`
	Kubernetes *Kubernetes `json:"kubernetes,omitempty"`
	AI         *AI         `json:"ai,omitempty"`
	Partial    bool        `json:"partial,omitempty"`
	Diagnostic string      `json:"diagnostic,omitempty"`
}

type Options struct {
	RequestEncoding  string
	ResponseEncoding string
	RequestTruncated bool
	StreamOpen       bool
	GitRequest       []byte
	Scope            awswire.Scope
	InvalidScope     bool
}

type exchange struct {
	req               *http.Request
	resp              *http.Response
	query             url.Values
	host              string
	request, response body
	options           Options
}

const maxBodyBytes = 4 << 20

type body struct {
	text, encoding, media string
	truncated             bool
	size                  int64
}

func Decode(entry har.Entry, options Options) Result {
	if entry.Request.Method == http.MethodConnect {
		return Result{}
	}
	address, err := url.Parse(entry.Request.URL)
	if err != nil {
		return Result{}
	}
	req := &http.Request{Method: entry.Request.Method, URL: address, Header: headers(entry.Request.Headers)}
	resp := &http.Response{StatusCode: entry.Response.Status, Header: headers(entry.Response.Headers)}
	query := address.Query()
	for _, q := range entry.Request.QueryString {
		if !query.Has(q.Name) {
			query.Set(q.Name, q.Value)
		}
	}
	e := exchange{req: req, resp: resp, query: query, host: hostname(address), options: options}
	e.request = body{encoding: options.RequestEncoding, size: entry.Request.BodySize, truncated: options.RequestTruncated}
	if post := entry.Request.PostData; post != nil {
		e.request.text, e.request.media = post.Text, mediaType(post.MimeType)
	}
	e.request.media = first(e.request.media, mediaType(req.Header.Get("Content-Type")))
	content := entry.Response.Content
	e.response = body{text: content.Text, encoding: options.ResponseEncoding, size: content.Size, truncated: content.Truncated,
		media: first(mediaType(content.MimeType), mediaType(resp.Header.Get("Content-Type")))}
	for _, decode := range [...]func(exchange) Result{decodeGit, decodeGitHub, decodeAI, decodeKubernetes, decodeAWS} {
		if result := decode(e); result.Protocol != "" {
			return result
		}
	}
	return Result{}
}

func headers(values []har.Header) http.Header {
	header := make(http.Header, len(values))
	for _, value := range values {
		header.Add(value.Name, value.Value)
	}
	return header
}

func hostname(u *url.URL) string {
	return strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
}

func mediaType(value string) string {
	media, _, _ := mime.ParseMediaType(value)
	return media
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func observed(value string) string {
	if masked(value) {
		return ""
	}
	return value
}

func masked(value string) bool {
	return strings.Contains(value, "****") || strings.EqualFold(strings.TrimSpace(value), "[REDACTED]")
}

func (b body) bytes(d *Details) []byte {
	switch {
	case b.text == "":
		return nil
	case b.encoding == "base64" && len(b.text) <= base64.StdEncoding.EncodedLen(maxBodyBytes):
		value, err := base64.StdEncoding.DecodeString(b.text)
		if err == nil && len(value) <= maxBodyBytes {
			return value
		}
	case b.encoding == "" && len(b.text) <= maxBodyBytes:
		return []byte(b.text)
	}
	d.issue("invalid or oversized captured body")
	return nil
}

func (b body) json(d *Details, target any) bool {
	data := b.bytes(d)
	if len(data) == 0 {
		return false
	}
	if b.truncated || (b.size > int64(len(data)) && !json.Valid(data)) {
		d.Partial = true
		return false
	}
	if err := json.Unmarshal(data, target); err != nil {
		d.issue("invalid captured JSON")
		return false
	}
	return true
}

func (b body) isJSON() bool {
	return b.media == "application/json" || strings.HasSuffix(b.media, "+json")
}

func (d *Details) issue(message string) {
	d.Diagnostic = first(d.Diagnostic, message)
}
