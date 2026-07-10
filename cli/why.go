package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
)

func newWhyCmd() *cobra.Command {
	var ledgerDir string

	cmd := &cobra.Command{
		Use:   "why <proposal-id>",
		Short: "Explain an accepted proposal: intent, delta, acceptance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ledger := core.Open(ledgerDir)
			p, err := ledger.Read(args[0])
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "proposal %s (%s)\n", p.ID, p.Kind)
			fmt.Fprintf(out, "stack:  %s\n", p.Stack)
			fmt.Fprintf(out, "status: %s\n", p.Status)
			fmt.Fprintf(out, "intent: %s\n", p.Intent.Summary)
			for _, s := range p.Intent.Sources {
				fmt.Fprintf(out, "  source: %s %s (content_hash=%s)\n", s.Kind, s.Ref, s.ContentHash)
			}
			if p.Acceptance != nil {
				fmt.Fprintf(out, "accepted by %v via %s at %s\n", p.Acceptance.Approvers, p.Acceptance.Method, p.Acceptance.AcceptedAt)
			}
			fmt.Fprintf(out, "blast radius: +%d ~%d -%d\n", p.BlastRadius.Creates, p.BlastRadius.Modifies, p.BlastRadius.Destroys)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	return cmd
}
