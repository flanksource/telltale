// Package auto starts the agent when configured through the environment.
package auto

import (
	"log"
	"net/http"
	"os"

	telltale "github.com/flanksource/telltale/sdk"
)

var agent *telltale.Agent

func init() {
	if os.Getenv("TELLTALE_AGENT") != "1" && os.Getenv("TELLTALE_URL") == "" {
		return
	}
	transport := http.DefaultClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	http.DefaultClient.Transport = telltale.Transport(transport)
	var err error
	agent, err = telltale.Start(telltale.Options{})
	if err != nil {
		log.Printf("telltale agent startup failed: %v", err)
	}
}
