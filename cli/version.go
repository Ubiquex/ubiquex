package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is the ubx build version. Overridden at build time via
// -ldflags "-X github.com/ubiquex/ubiquex-cli/cli.Version=...".
var Version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the ubx version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), Version)
			return nil
		},
	}
}
