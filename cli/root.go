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
	root.AddCommand(newResolveCmd())
	root.AddCommand(newProposeCmd())
	root.AddCommand(newWhyCmd())
	root.AddCommand(newScanCmd())
	root.AddCommand(newWritebackCmd())
	root.AddCommand(newRevertPlanCmd())
	root.AddCommand(newShipCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newChatCmd())

	return root
}

// Execute runs the ubx root command.
func Execute() error {
	return NewRootCmd().Execute()
}
