package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/core/resolver"
	"github.com/ubiquex/ubiquex-cli/provider"
)

// newResolveCmd is UBI-27's resolver CLI surface: a new verb, not a flag
// on `ubx propose`. `ubx propose` has one narrow, already-established job
// -- hash an already-resolved draft proposal for a PR body's trailer -- and
// explicitly refuses anything that isn't already fully resolved; folding
// intent-file resolution into it as a mode flag would conflate two
// genuinely different operations the same way this codebase never
// conflates scan/accept/ship into one multi-purpose verb. `ubx resolve`
// instead slots into the existing pipeline exactly like `ubx scan` already
// does: it reads some input (an intent file, not live drift) and produces
// a draft proposal, which `ubx propose`/`ubx accept` then take unchanged.
//
// Unlike scan/status/ship, resolve never reads live provider state at all
// -- docs/resolver.md's own contract names "live state via
// core.StateReader" as an input, but the actual implementation (UBI-27
// session 2) never needed it: a change proposal's "before" comes from the
// ledger's own FoldState, not a fresh cloud read (that happens later, at
// ship time, the same way it already does for drift_revert). A provider
// is still launched here, for exactly one reason: its schema is what
// SchemaInspector answers Computed/Sensitive/HasType questions against.
func newResolveCmd() *cobra.Command {
	var (
		ledgerDir       string
		providerPath    string
		source          string
		providerVersion string
		out             string
		timeout         time.Duration
	)

	cmd := &cobra.Command{
		Use:   "resolve <intent-file>",
		Short: "Resolve a typed ubx:intent/v1 file into a draft change proposal",
		Long: `Resolves a hand-written, machine-shaped intent file (ubx:intent/v1) into a draft
kind:"change" proposal -- creates and modifies, never destroys (v1 scope, docs/resolver.md).
Intra-stack references are checked against the ledger's own dependency graph (with real cycle
detection) and emitted in dependency order; cross-stack references are pinned against a neighbor
ledger's current head, activating neighbor-advance staleness for real once the proposal is accepted
(see "ubx accept"'s own pin re-verification).

The result is a draft: it has no id or acceptance yet. Pipe it into "ubx propose" for a PR-body
trailer hash, or "ubx accept" directly, exactly like a proposal ubx scan generates.`,
		Args: cobra.ExactArgs(1),
		// resolve has no "finding" concept, the same audit outcome as
		// propose/init/version (UBI-20 exit-code contract): it either
		// resolves or it doesn't. 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}
			applyProviderDefaults(cmd, &providerPath, &source, &providerVersion, cfg)

			data, err := os.ReadFile(args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}
			var intent resolver.IntentFile
			if err := json.Unmarshal(data, &intent); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: parse intent file: %w", err)}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			path, _, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}
			client, err := provider.Launch(ctx, path)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}
			defer client.Close()

			schemas, err := client.Provider.Schema(ctx)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: fetch provider schema: %w", err)}
			}

			ledger := core.Open(ledgerDir)
			p, err := resolver.Resolve(ledger, newSchemaInspector(schemas), &intent)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}

			out2 := cmd.OutOrStdout()
			fmt.Fprintf(out2, "resolved: %s: %d create(s), %d modify(ies)\n", intent.Stack, len(p.Delta.Creates), len(p.Delta.Modifies))

			b, err := json.MarshalIndent(p, "", "  ")
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: marshal proposal: %w", err)}
			}
			if out == "" {
				fmt.Fprintln(out2, string(b))
				return nil
			}
			if err := os.WriteFile(out, b, 0o644); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source)")
	cmd.Flags().StringVar(&out, "out", "", "write the resolved proposal here instead of stdout")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "timeout for launching the provider and fetching its schema")

	return cmd
}
