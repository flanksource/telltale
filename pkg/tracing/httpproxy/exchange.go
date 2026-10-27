package httpproxy

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/flanksource/commons/har"
	tracehttp "github.com/flanksource/telltale/pkg/tracing/http"
)

// policy is what shapes an exchange: how much of a body to keep, and which
// headers to blank beside redactedHeaders.
type policy struct {
	bodies int64
	redact []string
}

func newPolicy(bodies int64, redact []string) policy {
	return policy{bodies: bodies, redact: redact}
}

// exchangeState carries a request across goproxy's request and response
// handlers: its body is consumed by the transport before the response comes
// back, so it is captured on the way out.
type exchangeState struct {
	started time.Time
	post    *har.PostData
	comment string
}

// connectExchange is one CONNECT tunnel. Its payload stayed encrypted, so it
// has no method, path, status or body to report beyond what the proxy saw:
// the tunnel is marked tunnelled rather than given a 200 nobody sent, the 200
// being the proxy's own "Connection established".
func connectExchange(host, serverIP string, started time.Time, elapsed time.Duration, in, out int64, err error) tracehttp.Exchange {
	status, statusText := http.StatusOK, "Connection established"
	entry := har.Entry{
		StartedDateTime: started.UTC().Format(time.RFC3339Nano), Time: milliseconds(elapsed), Timings: unmeasuredTimings(elapsed),
		Request: har.Request{
			Method: http.MethodConnect, URL: "https://" + host, HTTPVersion: "HTTP/1.1", Cookies: []har.Cookie{},
			Headers: []har.Header{}, QueryString: []har.QueryString{}, HeadersSize: -1, BodySize: out,
		},
	}
	if err != nil {
		entry.Error, status, statusText = err.Error(), 0, ""
	}
	entry.Response = har.Response{
		Status: status, StatusText: statusText, HTTPVersion: "HTTP/1.1", Cookies: []har.Cookie{}, Headers: []har.Header{},
		Content: har.Content{Size: in}, HeadersSize: -1, BodySize: in,
	}
	exchange := tracehttp.FromEntry(Kind, entry)
	exchange.ServerIPAddress, exchange.Tunnelled, exchange.BytesIn, exchange.BytesOut = serverIP, true, in, out
	return exchange
}

// exchangeOf is one request and its response seen in the clear: a plain HTTP
// call, or one decrypted under mitm. An event stream's content is left to the
// streamCapture it returns, which fills it as the stream passes.
func exchangeOf(rules policy, req *http.Request, resp *http.Response, state exchangeState, err error) (tracehttp.Exchange, *streamCapture) {
	elapsed := time.Since(state.started)
	request := har.RedactRequest(req.URL, req.Header, har.HARConfig{RedactedHeaders: rules.redact})
	request.Method, request.HTTPVersion, request.Cookies = req.Method, req.Proto, []har.Cookie{}
	request.PostData, request.HeadersSize, request.BodySize = state.post, -1, req.ContentLength
	entry := har.Entry{
		StartedDateTime: state.started.UTC().Format(time.RFC3339Nano), Time: milliseconds(elapsed), Timings: unmeasuredTimings(elapsed),
		Request: request,
	}
	exchange := tracehttp.FromEntry(Kind, entry)
	comments := []string{state.comment}
	if err != nil {
		exchange.Error, exchange.Entry.Error = err.Error(), err.Error()
	}
	if resp == nil {
		exchange.Response = har.Response{Cookies: []har.Cookie{}, Headers: []har.Header{}, HeadersSize: -1, BodySize: -1}
		exchange.Comment = joinComments(comments...)
		return exchange, nil
	}
	content, encoding, comment, stream := rules.captureResponseBody(resp)
	exchange.Status, exchange.Encoding, exchange.Comment = resp.StatusCode, encoding, joinComments(append(comments, comment)...)
	exchange.Response = har.Response{
		Status: resp.StatusCode, StatusText: http.StatusText(resp.StatusCode), HTTPVersion: resp.Proto,
		Cookies: []har.Cookie{}, Headers: rules.headers(resp.Header), Content: content,
		RedirectURL: resp.Header.Get("Location"), HeadersSize: -1, BodySize: resp.ContentLength,
	}
	return exchange, stream
}

// captureResponseBody reads up to the cap and puts back what it read, so the
// client still receives the whole response. An event stream never ends while
// its page is open, so it cannot be read ahead: it is teed as it passes
// instead, into the streamCapture returned.
func (rules policy) captureResponseBody(resp *http.Response) (content har.Content, encoding, comment string, stream *streamCapture) {
	content = har.Content{Size: resp.ContentLength, MimeType: resp.Header.Get("Content-Type")}
	if rules.bodies <= 0 || resp.Body == nil {
		return content, "", "", nil
	}
	if mediaType, _, _ := mime.ParseMediaType(content.MimeType); mediaType == "text/event-stream" {
		stream = &streamCapture{limit: int(rules.bodies)}
		resp.Body = teeStream{ReadCloser: resp.Body, capture: stream}
		return content, "", "", stream
	}
	consumed, keep, truncated, err := readCapped(resp.Body, rules.bodies)
	resp.Body = replayBody{Reader: io.MultiReader(bytes.NewReader(consumed), resp.Body), Closer: resp.Body}
	if err != nil {
		return content, "", "response body capture failed: " + err.Error(), nil
	}
	if resp.ContentLength < 0 {
		content.Size = int64(len(consumed))
	}
	content.Text, encoding = encodeBody(keep)
	content.Truncated = truncated
	return content, encoding, "", nil
}

// capturePostData reads the request body before the transport does and puts
// it back. An empty body has no postData; a binary one is described rather
// than passed off as text, HAR's postData having no encoding.
func (rules policy) capturePostData(req *http.Request) (*har.PostData, string) {
	consumed, keep, truncated, err := readCapped(req.Body, rules.bodies)
	req.Body = replayBody{Reader: io.MultiReader(bytes.NewReader(consumed), req.Body), Closer: req.Body}
	switch {
	case err != nil:
		return nil, "request body capture failed: " + err.Error()
	case len(consumed) == 0:
		return nil, ""
	}
	text, encoding := encodeBody(keep)
	if encoding != "" {
		return &har.PostData{MimeType: req.Header.Get("Content-Type")}, "binary request body omitted"
	}
	post := &har.PostData{MimeType: req.Header.Get("Content-Type"), Text: text}
	if truncated {
		return post, "request body truncated at bodies"
	}
	return post, ""
}

// readCapped reads one byte past limit, to tell a body that exactly fills the
// cap from one that overflows it. consumed is everything taken off the stream,
// which must be replayed in full; keep is the prefix the exchange stores.
func readCapped(reader io.Reader, limit int64) (consumed, keep []byte, truncated bool, err error) {
	consumed, err = io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return consumed, nil, false, err
	}
	if int64(len(consumed)) > limit {
		return consumed, consumed[:limit], true, nil
	}
	return consumed, consumed, false, nil
}

// encodeBody keeps text as text and base64s what is not valid UTF-8.
func encodeBody(body []byte) (text, encoding string) {
	if len(body) == 0 {
		return "", ""
	}
	if utf8.Valid(body) {
		return string(body), ""
	}
	return base64.StdEncoding.EncodeToString(body), "base64"
}

// replayBody hands back the bytes already read, then the rest of the
// original stream, closing the original.
type replayBody struct {
	io.Reader
	io.Closer
}

// headers are header as HAR's name/value pairs, the denylist and the
// policy's names blanked, sorted so two captures compare stably.
func (rules policy) headers(header http.Header) []har.Header {
	out := har.RedactRequest(nil, header, har.HARConfig{RedactedHeaders: rules.redact}).Headers
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// unmeasuredTimings report only what was observed: the whole exchange as
// receive time, and -1, HAR's "not applicable", for the phases it did not.
func unmeasuredTimings(total time.Duration) har.Timings {
	return har.Timings{Send: -1, Wait: -1, Receive: milliseconds(total)}
}

func milliseconds(elapsed time.Duration) float64 { return float64(elapsed.Nanoseconds()) / 1e6 }

func joinComments(comments ...string) string {
	kept := comments[:0:0]
	for _, comment := range comments {
		if comment != "" {
			kept = append(kept, comment)
		}
	}
	return strings.Join(kept, "; ")
}
