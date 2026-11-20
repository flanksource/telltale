// Command signing-connection writes a connection whose signingKey secret is a
// new RSA key, for a Deployer's spec.auth.issuer. The key never leaves the
// file: agents get only its public JWKS. `telltale jvm deploy` creates the
// default issuer (connection://telltale/issuer) the same way on first use.
//
//	go run ./hack/signing-connection <name> <dir>
package main

import (
	"fmt"
	"os"

	"github.com/flanksource/telltale/pkg/deploy"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: signing-connection <name> <dir>")
	}
	name, dir := args[0], args[1]
	path, err := deploy.WriteIssuer(dir, "", name)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s: issuer connection://%s\n", path, name)
	return nil
}
