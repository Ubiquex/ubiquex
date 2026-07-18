package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newConfigCmd is UBI-32 Arc A's own provenance surface
// (docs/architecture.md — "Config: cascading, per-key, child overrides
// parent": "a resolved-config view printing every effective value AND
// which file supplied it"). A cascade that merges per-key across
// however many `.ubx/config*` files exist between cwd and the
// filesystem root is powerful and, without this, invisibly wrong --
// this makes every effective value, and exactly which file won it,
// explicit rather than guessed at.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show the resolved .ubx/config cascade -- every effective value and which file supplied it",
		Long: `Walk the .ubx/config cascade from the current directory upward until a cascade ceiling stops it
(docs/architecture.md — Cascade ceiling: a root = true file, else the git repo boundary, else $HOME or the
filesystem root), merge every file found per key, and print each key's effective value alongside the exact
file that supplied it, plus ~/.ubx/config's own allowlisted personal-preference keys (User-global ~/.ubx/config)
and where the walk stopped and why.`,
		// Same UBI-20 exit-code contract as `ubx init`: no "finding"
		// concept here, just a load that either succeeds or hard-fails.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := LoadConfigResolved(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("config: %w", err)}
			}
			if len(rc.Files) == 0 && rc.UserGlobalFile == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "no .ubx/config found (checked every directory from here up to the filesystem root)")
			}
			fmt.Fprint(cmd.OutOrStdout(), renderProvenance(rc))
			return nil
		},
	}
	return cmd
}
