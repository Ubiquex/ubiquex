package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/intentprovider"
)

// newProposeCmd serves two genuinely different pipeline stages under one
// verb name, disambiguated by --from-doc:
//
//   - `ubx propose <proposal.json>` (UBI-11 stage 1, unchanged): an
//     author resolves a draft proposal and needs its canonical hash to
//     embed in a PR body's "ubx-proposal: <hash>" trailer, before anyone
//     reviews anything and long before `ubx accept --from-merge` runs.
//     Never touches a ledger -- a pure function of the file's own
//     content, same core.Hash `ubx accept`'s local tier uses internally.
//   - `ubx propose --from-doc <file>.md --stack <stack>` (UBI-41, new):
//     the md medium's own entry point -- transcribes a markdown
//     authoring document into an intent/v1 DRAFT via the configured
//     [intent] provider adapter and writes it out. Deliberately stops
//     there: it never resolves, never touches a ledger, matching
//     docs/intent-provider.md's own "each step in this trust chain is a
//     deliberate human checkpoint" posture -- the same reason `ubx
//     resolve` itself never auto-chains into `ubx accept`.
func newProposeCmd() *cobra.Command {
	var fromDoc, stack, out string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "propose [proposal.json]",
		Short: "Compute a draft proposal's canonical hash, or transcribe a markdown document into an intent/v1 draft",
		Args:  cobra.MaximumNArgs(1),
		// propose has no "finding" concept -- it either succeeds or it
		// doesn't (UBI-20 exit-code contract): 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromDoc != "" {
				if len(args) > 0 {
					return &ExitCodeError{Code: 2, Err: errors.New("propose: --from-doc and a proposal.json argument are mutually exclusive")}
				}
				return runProposeFromDoc(cmd, fromDoc, stack, out, timeout)
			}
			if len(args) != 1 {
				return &ExitCodeError{Code: 2, Err: errors.New("propose: exactly one of a proposal.json argument or --from-doc is required")}
			}

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

	cmd.Flags().StringVar(&fromDoc, "from-doc", "", "path to a markdown authoring document -- transcribes it into an intent/v1 draft via the configured [intent] provider (UBI-41)")
	cmd.Flags().StringVar(&stack, "stack", "", "target stack name (required with --from-doc)")
	cmd.Flags().StringVar(&out, "out", "", "write the intent/v1 draft here instead of stdout (only with --from-doc)")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "timeout for the intent provider's own drafting round trip (only with --from-doc)")
	return cmd
}

// runProposeFromDoc is `ubx propose --from-doc`'s own RunE body
// (docs/intent-provider.md's own md-pipeline design): read the raw
// document, redact secret-shaped material at capture (before anything
// ever leaves this machine), draft via the configured intent provider
// with retry-with-errors/hard-fail already built into DraftWithRetry,
// populate provenance, render the ambiguity content for a human to
// review, and write the draft. Never resolves, never touches a ledger.
func runProposeFromDoc(cmd *cobra.Command, docPath, stack, out string, timeout time.Duration) error {
	if stack == "" {
		return &ExitCodeError{Code: 2, Err: errors.New("propose --from-doc: --stack is required")}
	}

	raw, err := os.ReadFile(docPath)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: err}
	}

	cfg, err := LoadConfig(cmd.ErrOrStderr())
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-doc: %w", err)}
	}

	adapter, err := buildIntentAdapter(cfg)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-doc: %w", err)}
	}

	redacted, findings := intentprovider.Redact(raw)
	errOut := cmd.ErrOrStderr()
	for _, f := range findings {
		fmt.Fprintf(errOut, "warning: propose --from-doc: redacted possible secret material (%s) before sending %s to the %s adapter\n", f, docPath, adapter.Name())
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	draft, rawOutput, err := intentprovider.DraftWithRetry(ctx, adapter, stack, redacted)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-doc: %w", err)}
	}

	intentprovider.PopulateSources(draft, docPath, intentprovider.HashDocument(raw), adapter.Name(), adapter.Model(), rawOutput)

	outWriter := cmd.OutOrStdout()
	renderAmbiguity(outWriter, draft)

	data, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-doc: marshal draft: %w", err)}
	}
	if out == "" {
		fmt.Fprintln(outWriter, string(data))
		return nil
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-doc: %w", err)}
	}
	fmt.Fprintf(outWriter, "wrote draft: %s\n", out)
	return nil
}
