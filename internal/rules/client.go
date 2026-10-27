package rules

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type FetchOptions struct {
	Server, Operation string
	Query             url.Values
	Output            io.Writer
}

func Fetch(ctx context.Context, options FetchOptions) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if options.Operation != "events" && options.Operation != "status" {
		return fmt.Errorf("invalid rules operation")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(options.Server, "/")+"/api/rules/"+options.Operation+"?"+options.Query.Encode(), nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		message, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return err
		}
		return fmt.Errorf("rules %s: HTTP %d: %s", options.Operation, response.StatusCode, strings.TrimSpace(string(message)))
	}
	_, err = io.Copy(options.Output, response.Body)
	return err
}
