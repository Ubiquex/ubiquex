package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
)

// newProposeCmd is UBI-11 stage 1's PR-merge acceptance flow, step one: an
// author resolves a draft proposal and needs its canonical hash to embed
// in the PR body as a "ubx-proposal: <hash>" trailer, before anyone
// reviews anything and long before `ubx accept --from-merge` runs. This
// never touches a ledger — it's a pure function of the file's own content,
// same core.Hash `ubx accept`'s local tier uses internally.
func newProposeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "propose <proposal.json>",
		Short: "Compute a draft proposal's canonical hash for a PR body's ubx-proposal trailer",
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
			if p.ID != "" || p.Acceptance != nil {
				return fmt.Errorf("propose: this proposal already has an id/acceptance -- propose is for drafts only")
			}
			if err := core.Validate(&p); err != nil {
				return fmt.Errorf("propose: %w", err)
			}

			hash, err := core.Hash(&p)
			if err != nil {
				return fmt.Errorf("propose: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "ubx-proposal: %s\n", hash)
			return nil
		},
	}
	return cmd
}
