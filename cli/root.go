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
	root.AddCommand(newPlanCmd())
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
	root.AddCommand(newSDKCmd())
	root.AddCommand(newRenderCmd())
	root.AddCommand(newVerifyCmd())
	root.AddCommand(newBlameCmd())
	root.AddCommand(newStatsCmd())
	root.AddCommand(newAddressesCmd())
	root.AddCommand(newPromoteCmd())
	root.AddCommand(newTerminateCmd())
	root.AddCommand(newDestroyCmd())

	return root
}

// Execute runs the ubx root command.
func Execute() error {
	return NewRootCmd().Execute()
}
