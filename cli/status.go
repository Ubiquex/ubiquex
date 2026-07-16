package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/provider"
)

// newStatusCmd is UBI-17's fleet status: a read-only report over every
// resource the ledger already knows about (docs/architecture.md — Fleet
// status), reusing core.Ledger.Fleet for discovery and, with --drift,
// core.RunScan's own ObservedHash(FoldState) baseline per resource, fed
// each resource's own persisted resolution.inputs[].lookup.
func newStatusCmd() *cobra.Command {
	var (
		ledgerDir       string
		stack           string
		drift           bool
		providerPath    string
		source          string
		providerVersion string
		providerConfig  string
		timeout         time.Duration
	)

	cmd := &cobra.Command{
		Use:   "status [flags]",
		Short: "Report every resource the ledger knows about, optionally comparing against live state",
		Long: `Walks every resource the ledger has ever recorded (all stacks by default, or one via --stack)
and reports its address and latest recorded state. Ledger-only by default -- fast, no provider, no
credentials needed. With --drift, also reads each resource's live state (reusing its own recorded
resolution.inputs lookup key, exactly like ubx scan would) and classifies it clean, drifted, or
unreadable; a failure on one resource is recorded and the walk continues, never aborting the report.

Exit code is a CI contract: 0 if clean (or ledger-only, which has nothing to report drift on), 1 if
anything drifted, 2 if anything was unreadable or the command failed outright. Whichever is worse
always wins if more than one applies.`,
		// A drifted/unreadable outcome is a normal, working-as-designed
		// report, not a misuse of the command -- dumping the full flag
		// usage block for it (cobra's default on any RunE error) would be
		// pure noise for a CI-facing exit-code contract. SilenceErrors
		// avoids a doubled message too: cobra would otherwise print its
		// own "Error: ..." here, then cmd/ubx/main.go prints
		// ExitCodeError.Err again to build the specific exit code.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
			}
			// Deliberately NOT applying a config default for --stack here:
			// its absence means "every stack" (docs/architecture.md — Fleet
			// status), a meaningfully different thing from scan's "required
			// and missing." A configured default stack would silently turn
			// bare `ubx status` from "show everything" into "show just my
			// one stack," which is exactly the opposite of what makes it
			// useful as a fleet-wide report.
			applyProviderDefaults(cmd, &providerPath, &source, &providerVersion, cfg)
			if err := applyProviderConfigDefault(cmd, &providerConfig, cfg); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
			}

			ledger := core.Open(ledgerDir)
			fleet, err := ledger.Fleet(stack)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
			}

			out := cmd.OutOrStdout()

			if !drift {
				for _, e := range fleet {
					fmt.Fprintf(out, "%s: %s %s (accepted %s)\n", e.Address, e.Kind, shortID(e.ProposalID), e.AcceptedAt)
				}
				fmt.Fprintf(out, "%d resource(s) (ledger-only, no live comparison)\n", len(fleet))
				return nil
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			path, _, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
			}
			client, err := provider.Launch(ctx, path)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
			}
			defer client.Close()
			stateReader := newStateReader(client.Provider)

			var driftedCount, unreadableCount int
			for _, e := range fleet {
				if len(e.Lookup) == 0 {
					unreadableCount++
					fmt.Fprintf(out, "unreadable: %s: %s %s (accepted %s) -- no lookup key recorded for this resource "+
						"(authored before the resolution.inputs lookup amendment, or never had one)\n",
						e.Address, e.Kind, shortID(e.ProposalID), e.AcceptedAt)
					continue
				}

				res, err := core.RunScan(ctx, stateReader, ledger, core.ScanRequest{
					Address:        e.Address,
					ProviderConfig: json.RawMessage(providerConfig),
					CurrentState:   e.Lookup,
				})
				if err != nil {
					unreadableCount++
					fmt.Fprintf(out, "unreadable: %s: %s %s (accepted %s) -- %v\n",
						e.Address, e.Kind, shortID(e.ProposalID), e.AcceptedAt, err)
					continue
				}

				switch res.Outcome {
				case core.ScanUnchanged:
					fmt.Fprintf(out, "clean: %s: %s %s (accepted %s)\n", e.Address, e.Kind, shortID(e.ProposalID), e.AcceptedAt)
				case core.ScanDrifted:
					driftedCount++
					fmt.Fprintf(out, "drifted: %s: %s %s (accepted %s)\n", e.Address, e.Kind, shortID(e.ProposalID), e.AcceptedAt)
				default:
					// ScanNew: the ledger has a resolution.inputs entry for
					// this address (that's how Fleet found it) but FoldState
					// can't reconstruct any prior state for it -- a
					// malformed/hand-authored proposal, not something a
					// well-formed scan-generated ledger ever produces (see
					// docs/architecture.md).
					unreadableCount++
					fmt.Fprintf(out, "unreadable: %s: %s %s (accepted %s) -- ledger has no reconstructable prior state for this address\n",
						e.Address, e.Kind, shortID(e.ProposalID), e.AcceptedAt)
				}
			}

			fmt.Fprintf(out, "%d resource(s), %d drifted, %d unreadable\n", len(fleet), driftedCount, unreadableCount)

			switch {
			case unreadableCount > 0:
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %d resource(s) unreadable (see above)", unreadableCount)}
			case driftedCount > 0:
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("status: %d resource(s) drifted (see above)", driftedCount)}
			default:
				return nil
			}
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "restrict the report to one stack (default: every stack the ledger holds)")
	cmd.Flags().BoolVar(&drift, "drift", false, "also compare each resource against live state (requires --provider, or --source+--provider-version)")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source; only used with --drift)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version; only used with --drift)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire, e.g. 6.54.0 (required with --source; only used with --drift)")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "{}", "JSON object configuring the provider, e.g. {\"region\":\"us-east-1\"} (only used with --drift)")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "overall timeout for the fleet walk (only used with --drift)")

	return cmd
}
