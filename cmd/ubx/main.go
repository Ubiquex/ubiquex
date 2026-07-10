// Command ubx is the Ubiquex CLI entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/ubiquex/ubiquex-cli/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
