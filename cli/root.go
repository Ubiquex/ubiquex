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
		// One place rather than per-command, since the question "is this
		// binary older than the checkout I am in" has the same answer for
		// all of them, and a check that has to be remembered at each call
		// site is one that will be missed at some of them: the hard
		// refusal it complements sits at exactly one, and the two
		// incidents since both landed on commands it does not cover.
		//
		// To STDERR, deliberately: several commands emit JSON on stdout
		// and a warning there would corrupt it for anything parsing it.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			warnIfBinaryOlderThanCheckout(cmd.ErrOrStderr())
		},
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
	root.AddCommand(newServerCmd())
	root.AddCommand(newSDKCmd())
	root.AddCommand(newBlueprintCmd())
	root.AddCommand(newRenderCmd())
	root.AddCommand(newVerifyCmd())
	root.AddCommand(newBlameCmd())
	root.AddCommand(newStatsCmd())
	root.AddCommand(newAddressesCmd())
	root.AddCommand(newPromoteCmd())
	root.AddCommand(newTerminateCmd())
	root.AddCommand(newDestroyCmd())
	root.AddCommand(newProvidersCmd())
	root.AddCommand(newStoreCmd())
	root.AddCommand(newHistoryCmd())
	root.AddCommand(newRestoreCmd())
	root.AddCommand(newAliasCmd())

	return root
}

// Execute runs the ubx root command.
func Execute() error {
	return NewRootCmd().Execute()
}
