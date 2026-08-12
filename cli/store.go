package cli

import (
	"github.com/spf13/cobra"
)

// newStoreCmd is a parent command, not a single leaf verb -- matching
// newSDKCmd/newProvidersCmd's own precedent (a real, grouped
// multi-subcommand feature, not a bare command needing a nested Use
// string for its own sake) -- for `ubx store gc` today, leaving room for
// a future sibling (`ubx store info`, say) without renaming anything
// already shipped.
func newStoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store",
		Short: "Operations on a remote LedgerStore itself (S3/GCS/Azure Blob), distinct from the ledger content it holds",
	}
	cmd.AddCommand(newStoreGCCmd())
	return cmd
}
