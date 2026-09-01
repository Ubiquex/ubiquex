package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
)

// newProposeCmd is UBI-11 stage 1, unchanged since before UBI-224: an
// author resolves a draft proposal and needs its canonical hash to
// embed in a PR body's "ubx-proposal: <hash>" trailer, before anyone
// reviews anything and long before `ubx accept --from-merge` runs.
// Never touches a ledger -- a pure function of the file's own content,
// the same core.Hash `ubx accept`'s local tier uses internally.
//
// UBI-224 removed this command's other two real modes,
// --from-doc (the markdown medium's own entry point, UBI-41) and
// --from-diagram (the diagram medium's own entry point, UBI-47): both
// transcribed a real authoring input into an intent/v1 draft for a
// human to review before a separate `ubx resolve` call, and both are
// gone along with the mediums that produced them. `ubx resolve
// --from-code` is the SDK's own one-step equivalent and needed no
// change here, since it never routed through this command at all.
func newProposeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "propose <proposal.json>",
		Short: "Compute a draft proposal's canonical hash",
		Args:  cobra.ExactArgs(1),
		// propose has no "finding" concept -- it either succeeds or it
		// doesn't (UBI-20 exit-code contract): 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}

			var p core.Proposal
			if err := json.Unmarshal(data, &p); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("parse proposal: %w", err)}
			}
			if p.ID != "" || p.Acceptance != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose: this proposal already has an id/acceptance -- propose is for drafts only")}
			}
			if err := core.Validate(&p); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose: %w", err)}
			}

			hash, err := core.Hash(&p)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose: %w", err)}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "ubx-proposal: %s\n", hash)
			return nil
		},
	}

	return cmd
}
