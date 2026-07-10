// Package cli implements the ubx command-line interface.
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd constructs the ubx root command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ubx",
		Short: "ubx — the Ubiquex infrastructure change management CLI",
	}

	root.AddCommand(newVersionCmd())
	root.AddCommand(newAcceptCmd())
	root.AddCommand(newWhyCmd())
	root.AddCommand(newScanCmd())

	return root
}

// Execute runs the ubx root command.
func Execute() error {
	return NewRootCmd().Execute()
}
