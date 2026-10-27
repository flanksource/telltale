package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"

	telltale "github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
)

type probe struct{}

func (probe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "fleet-test"} }
func (probe) Arm(_ context.Context, _ json.RawMessage, sink telltale.Sink) (func() error, error) {
	sink.TryEmit(map[string]string{"message": "armed"})
	return func() error { return nil }, nil
}

func main() {
	telltale.RegisterProbe(probe{})
	agent, err := telltale.Start(telltale.Options{ConfigDir: os.Getenv("AGENT_CONFIG_DIR"), Listen: os.Getenv("AGENT_LISTEN")})
	if err != nil {
		log.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if err := agent.Close(); err != nil {
		log.Fatal(err)
	}
}
