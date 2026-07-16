// Command ubx is the Ubiquex CLI entrypoint.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/ubiquex/ubiquex-cli/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		var exitErr *cli.ExitCodeError
		if errors.As(err, &exitErr) {
			if exitErr.Err != nil {
				fmt.Fprintln(os.Stderr, exitErr.Err)
			}
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
