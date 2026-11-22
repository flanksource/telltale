package httpdecode

import (
	"net/url"
	"strconv"
	"strings"
)

type GitHub struct {
	Route              string `json:"route"`
	OperationName      string `json:"operationName,omitempty"`
	Item               string `json:"item,omitempty"`
	RateLimitRemaining *int64 `json:"rateLimitRemaining,omitempty"`
	RateLimitReset     *int64 `json:"rateLimitReset,omitempty"`
}

func decodeGitHub(e exchange) Result {
	path := e.req.URL.EscapedPath()
	if e.host != "api.github.com" {
		evidence := observed(e.resp.Header.Get("X-GitHub-Request-Id")) != "" ||
			observed(e.req.Header.Get("X-GitHub-Api-Version")) != "" ||
			strings.Contains(e.req.Header.Get("Accept"), "application/vnd.github") ||
			strings.Contains(e.response.media, "application/vnd.github")
		if !evidence {
			return Result{}
		}
		switch {
		case path == "/api/v3" || strings.HasPrefix(path, "/api/v3/"):
			path = strings.TrimPrefix(path, "/api/v3")
		case path == "/api/graphql":
			path = "/graphql"
		default:
			return Result{}
		}
	}
	g := &GitHub{Route: first(path, "/"),
		RateLimitRemaining: number(e.resp.Header.Get("X-RateLimit-Remaining")),
		RateLimitReset:     number(e.resp.Header.Get("X-RateLimit-Reset"))}
	d := &Details{GitHub: g, RequestID: observed(e.resp.Header.Get("X-GitHub-Request-Id"))}
	operation := githubOperation(e, d)
	githubError(e, d)
	return Result{Protocol: "github", Operation: operation, Details: d}
}

func githubOperation(e exchange, d *Details) string {
	g := d.GitHub
	if g.Route == "/graphql" {
		var request struct {
			OperationName string `json:"operationName"`
		}
		if e.request.isJSON() && e.request.json(d, &request) {
			g.OperationName = request.OperationName
		}
		return first(g.OperationName, "GraphQL")
	}
	parts := strings.Split(strings.TrimPrefix(g.Route, "/"), "/")
	if len(parts) >= 3 && parts[0] == "repos" && parts[1] != "" && parts[2] != "" {
		d.Repository, _ = url.PathUnescape(parts[1] + "/" + parts[2])
		parts[1], parts[2] = "{owner}", "{repo}"
		if len(parts) >= 5 {
			item, _ := url.PathUnescape(parts[4])
			switch parts[3] {
			case "issues", "pulls", "milestones":
				if number(item) != nil {
					g.Item, parts[4] = item, "{number}"
				}
			case "commits", "releases", "branches":
				g.Item, parts[4] = item, "{id}"
			}
		}
		g.Route = "/" + strings.Join(parts, "/")
	}
	return e.req.Method + " " + g.Route
}

func githubError(e exchange, d *Details) {
	if !e.response.isJSON() || (e.resp.StatusCode < 400 && d.GitHub.Route != "/graphql") {
		return
	}
	var response struct {
		Errors []struct {
			Type       string `json:"type"`
			Code       string `json:"code"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if e.response.json(d, &response) && len(response.Errors) > 0 {
		failure := response.Errors[0]
		d.ErrorCode = first(failure.Code, failure.Type, failure.Extensions.Code, "github-error")
	}
}

func number(s string) *int64 {
	value, err := strconv.ParseInt(s, 10, 64)
	if err != nil || value < 0 {
		return nil
	}
	return &value
}
