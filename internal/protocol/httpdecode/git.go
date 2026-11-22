package httpdecode

import (
	"errors"
	"net/http"
	"strings"

	"github.com/flanksource/telltale/internal/protocol/gitwire"
)

type Git struct {
	Service           string `json:"service"`
	RequestedVersion  string `json:"requestedVersion,omitempty"`
	Version           string `json:"version,omitempty"`
	Command           string `json:"command,omitempty"`
	RefsObserved      int    `json:"refsObserved,omitempty"`
	UpdatesObserved   int    `json:"updatesObserved,omitempty"`
	PackBytesObserved int    `json:"packBytesObserved,omitempty"`
}

func gitService(value string) bool {
	return value == "git-upload-pack" || value == "git-receive-pack"
}

func gitRPC(path string) string {
	index := strings.LastIndexByte(path, '/')
	if index >= 0 && gitService(path[index+1:]) {
		return path[index+1:]
	}
	return ""
}

func GitRequest(req *http.Request) bool {
	service := gitRPC(req.URL.Path)
	return req.Method == http.MethodPost && service != "" &&
		mediaType(req.Header.Get("Content-Type")) == "application/x-"+service+"-request"
}

func decodeGit(e exchange) Result {
	path := e.req.URL.Path
	service, operation, repo := "", "", ""
	switch {
	case e.req.Method == http.MethodGet && strings.HasSuffix(path, "/info/refs"):
		service = e.query.Get("service")
		if !gitService(service) {
			return Result{}
		}
		operation, repo = "info/refs", strings.TrimSuffix(path, "/info/refs")
	case e.req.Method == http.MethodPost:
		service = gitRPC(path)
		if service == "" || (e.request.media != "application/x-"+service+"-request" && e.response.media != "application/x-"+service+"-result") {
			return Result{}
		}
		operation, repo = service, strings.TrimSuffix(path, "/"+service)
	default:
		return Result{}
	}
	git := &Git{Service: service}
	for parameter := range strings.SplitSeq(e.req.Header.Get("Git-Protocol"), ":") {
		if version, ok := strings.CutPrefix(parameter, "version="); ok && (version == "1" || version == "2") {
			git.RequestedVersion = version
		}
	}
	d := &Details{Git: git, Repository: strings.TrimPrefix(repo, "/")}
	if e.request.media == "application/x-"+service+"-request" {
		data := e.options.GitRequest
		if len(data) == 0 {
			data = e.request.bytes(d)
		}
		git.consume(data, e.request.truncated || (len(data) > 0 && int64(len(data)) < e.request.size), d)
	}
	if e.response.media == "application/x-"+service+"-advertisement" || e.response.media == "application/x-"+service+"-result" {
		git.consume(e.response.bytes(d), e.response.truncated, d)
	}
	return Result{Protocol: "git", Operation: operation, Details: d}
}

func (g *Git) consume(data []byte, truncated bool, d *Details) {
	d.Partial = d.Partial || truncated
	decoder := gitwire.Decoder{Emit: func(event gitwire.Event) {
		switch event.Kind {
		case "version":
			if event.Text == "1" || event.Text == "2" {
				g.Version = event.Text
			}
		case "command":
			switch event.Text {
			case "ls-refs", "fetch", "object-info", "bundle-uri":
				g.Command = event.Text
			}
		case "ref":
			g.RefsObserved++
		case "update":
			g.UpdatesObserved++
		case "pack":
			g.PackBytesObserved += event.Bytes
		case "error":
			d.ErrorCode = "git-error"
		}
	}}
	_, err := decoder.Write(data)
	if err == nil {
		err = decoder.Close()
	}
	if err != nil && (!truncated || !errors.Is(err, gitwire.ErrIncomplete)) {
		d.issue("invalid captured Git packet")
	}
}
