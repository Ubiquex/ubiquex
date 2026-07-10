package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
)

func newAcceptCmd() *cobra.Command {
	var ledgerDir string

	cmd := &cobra.Command{
		Use:   "accept <proposal.json>",
		Short: "Accept a hand-written proposal (local signing) and append it to the ledger",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}

			var p core.Proposal
			if err := json.Unmarshal(data, &p); err != nil {
				return fmt.Errorf("parse proposal: %w", err)
			}

			ledger := core.Open(ledgerDir)
			accepted, err := core.Accept(ledger, &p)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "accepted %s (stack %s)\n", accepted.ID, accepted.Stack)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	return cmd
}
