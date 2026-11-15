package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
)

// newRenderCmd generates blueprint override statements from drift
// (UBI-86): for one drifted resource (--from-drift) or every drifted
// resource in a stack (--sync-overrides), an override in the calling
// stack's own SDK language. Mechanical, never AI.
//
// It used to be the command's secondary mode. Its primary one was a
// projection of current state, the canonical D2 diagram or, with --md, a
// markdown document, with --check/--out for committing a render and
// diffing it in CI. Those went with the diagram and mdrender packages
// (UBI-301): with the agent authoring and the ledger recording, nobody
// was left to read a committed projection. Override generation stays
// because it was never about the projection. It sat behind --md only
// because it grew out of the markdown medium, and it now stands on its
// own flags. Whether it belongs here, or anywhere, is part of the open
// blueprint question.
func newRenderCmd() *cobra.Command {
	var (
		ledgerDir       string
		stack           string
		fromDrift       string
		syncOverrides   bool
		providerPath    string
		source          string
		providerVersion string
		providerConfig  string
		timeout         time.Duration
	)

	cmd := &cobra.Command{
		Use:           "render",
		Short:         "Generate blueprint override statements from drift, in the calling stack's own SDK language",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromDrift != "" && syncOverrides {
				return &ExitCodeError{Code: 2, Err: errors.New("render: --from-drift and --sync-overrides are mutually exclusive")}
			}
			if fromDrift == "" && !syncOverrides {
				return &ExitCodeError{Code: 2, Err: errors.New("render: pass --from-drift <address> for one drifted resource, or --sync-overrides for every drifted resource in the stack")}
			}

			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)
			if stack == "" {
				return &ExitCodeError{Code: 2, Err: errors.New("render: --stack is required -- override generation always covers exactly one stack")}
			}
			applyProviderDefaults(cmd, &providerPath, &source, &providerVersion, cfg)
			if err := applyProviderConfigDefault(cmd, &providerConfig, cfg); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
			}

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
			}
			defer closeLedger()

			return runRenderOverrideGeneration(cmd, cmd.OutOrStdout(), ledger, cfg, ledgerDir, stack, fromDrift,
				providerPath, source, providerVersion, providerConfig, timeout)
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack -- required (override generation always covers exactly one stack)")
	cmd.Flags().StringVar(&fromDrift, "from-drift", "", "generate a single override statement for one drifted resource's own address, in the calling stack's own SDK language (mutually exclusive with --sync-overrides)")
	cmd.Flags().BoolVar(&syncOverrides, "sync-overrides", false, "walk the whole stack's drift and generate an override statement for every drifted resource, in the calling stack's own SDK language (mechanical, zero AI)")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source)")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "{}", "JSON object configuring the provider")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "overall timeout for the drift scan")
	return cmd
}

func runRenderOverrideGeneration(
	cmd *cobra.Command,
	outWriter io.Writer,
	ledger *core.Ledger,
	cfg *Config,
	ledgerDir, stack, fromDrift string,
	providerPath, source, providerVersion, providerConfig string,
	timeout time.Duration,
) error {
	fleet, err := ledger.Fleet(stack)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
	}

	if fromDrift != "" {
		addr, ok := core.ParseAddress(fromDrift)
		if !ok {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --from-drift: %q is not a valid <stack>.<type>.<name> address", fromDrift)}
		}
		filtered := fleet[:0]
		for _, e := range fleet {
			if e.Address == addr {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --from-drift: %s not found in stack %q's own fleet", fromDrift, stack)}
		}
		fleet = filtered
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	drifted, err := scanDrift(ctx, ledger, cfg, providerPath, source, providerVersion, providerConfig, fleet)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
	}
	if fromDrift != "" && len(drifted) == 0 {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("render --from-drift: %s is not currently drifted", fromDrift)}
	}
	if len(drifted) == 0 {
		fmt.Fprintln(outWriter, "render --sync-overrides: no drift found -- nothing to generate")
		return nil
	}
	sort.Slice(drifted, func(i, j int) bool { return drifted[i].Address.String() < drifted[j].Address.String() })

	candidates, derr := autodetectMedium(ledgerDir)
	if derr != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: auto-detecting the calling stack's own authoring medium: %w", derr)}
	}
	if len(candidates) != 1 {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: requires exactly one authoring medium file in %s to auto-detect against, found %d -- this command generates syntax for the stack's OWN source, which must be unambiguous", ledgerDir, len(candidates))}
	}
	medium, merr := overrideMediumFromDetected(candidates[0])
	if merr != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", merr)}
	}

	var buf bytes.Buffer
	for _, d := range drifted {
		topLevel, err := topLevelOverrideFields(d)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --sync-overrides: %w", err)}
		}
		stmt, err := renderOverrideStatement(medium, d.Address, topLevel)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --sync-overrides: %w", err)}
		}
		buf.WriteString(stmt)
		buf.WriteString("\n")
	}

	outWriter.Write(buf.Bytes())
	return nil
}
