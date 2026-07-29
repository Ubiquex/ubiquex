package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/core/resolver"
	"github.com/ubiquex/ubiquex-cli/sdkeval"
)

// newPlanCmd is UBI-49's own terraform-shaped fusion of propose+resolve+
// preview into one command (docs/architecture.md's "Two-step fusion"
// amendment): takes any medium input this codebase already knows how to
// turn into an ubx:intent/v1 document (a hand-written intent file,
// --from-code's TypeScript SDK, --from-doc's markdown draft, or
// --from-diagram's .d2 topology), resolves it through the exact same,
// unmodified core/resolver.Resolve every other entry point already uses,
// renders the full receipt (delta, cost_delta, blast radius, assumptions/
// questions) for a human to review right here, and saves the result as a
// hash-addressed local plan file `ubx ship <hash>` can later pick up for
// inline local-tier acceptance. Never touches the ledger itself -- exactly
// like `ubx resolve` and `ubx propose` today, this is preview-only; only
// `ubx accept`/`ubx ship` ever record anything.
//
// The md/diagram media's own established "draft, then a separate human
// checkpoint before resolving" posture (docs/intent-provider.md,
// docs/diagram-medium.md) is deliberately NOT reproduced here: `ubx plan`
// is a new, additional fast path for the sandbox/solo workflow, and its
// own receipt (rendered before anything is saved or shipped) already IS
// that checkpoint, now covering the full resolved proposal -- delta, cost,
// blast radius, and any ambiguity content together -- rather than the
// draft's ambiguity content alone. `ubx propose --from-doc`/`--from-
// diagram`'s own draft-only, stop-before-resolving behavior is completely
// unchanged for teams who want that extra checkpoint as a separate step.
func newPlanCmd() *cobra.Command {
	var (
		ledgerDir       string
		providerPath    string
		source          string
		providerVersion string
		out             string
		timeout         time.Duration
		knownDependents []string
		fromCode        string
		fromDoc         string
		fromDiagram     string
		stack           string
		summary         string
		neighborLedgers []string
	)

	cmd := &cobra.Command{
		Use:   "plan [intent-file]",
		Short: "Resolve any medium input into a draft proposal, render its full receipt, and save it as a hash-addressed plan for `ubx ship`",
		Long: `Fuses "ubx propose" + "ubx resolve" + a preview render into one command -- the
terraform-shaped, two-step half of this project's own workflow (plan, then "ubx ship <hash>").

Exactly one input is required: a hand-written ubx:intent/v1 file (the positional argument),
--from-code <entry>.ts (a TypeScript SDK program, evaluated through the same hermetic Deno
evaluator "ubx resolve --from-code" uses), --from-doc <file>.md --stack <stack> (a markdown
authoring document, transcribed via the configured [intent] provider adapter exactly like
"ubx propose --from-doc"), or --from-diagram <file>.d2 --stack <stack> (a D2 diagram's own
topology, parsed exactly like "ubx propose --from-diagram").

The result resolves through the identical, unmodified core/resolver.Resolve every other entry
point already uses -- same invariants, same orphan/pin checks, same failure modes. Its full
receipt (delta, cost_delta, blast radius, assumptions/defaults/questions) renders to the
terminal for review, and the resolved-but-unaccepted proposal is written to
.ubx/plans/<hash>.json, keyed by the exact hash "ubx ship <hash>" will later look for.

This command never touches a ledger -- nothing here is accepted or applied. Run "ubx ship
<hash>" to accept (local tier) and apply in one step, or run "ubx accept"/"ubx propose" by
hand against the written plan file for the four-verb ceremony (PR-merge signing, a separate
propose-time PR trailer hash, etc.).`,
		Args: cobra.MaximumNArgs(1),
		// plan has no "finding" concept, the same audit outcome as
		// propose/resolve (UBI-20 exit-code contract): it either resolves
		// or it doesn't. 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			modes := len(args)
			if fromCode != "" {
				modes++
			}
			if fromDoc != "" {
				modes++
			}
			if fromDiagram != "" {
				modes++
			}
			if modes > 1 {
				return &ExitCodeError{Code: 2, Err: errors.New("plan: an intent-file argument, --from-code, --from-doc, and --from-diagram are mutually exclusive")}
			}
			if modes == 0 {
				return &ExitCodeError{Code: 2, Err: errors.New("plan: requires exactly one of an intent-file argument, --from-code, --from-doc, or --from-diagram")}
			}

			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			var intent resolver.IntentFile
			switch {
			case fromDoc != "":
				draft, err := draftFromDoc(cmd, cfg, fromDoc, stack, timeout)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan --from-doc: %w", err)}
				}
				intent = *draft
			case fromDiagram != "":
				// draftFromDiagram already loads every declared provider
				// once for diagram.Parse's own type-inference pass; the
				// resolver.Resolve call below loads them again (schema-only,
				// no cloud calls either time) -- a real, known duplication,
				// accepted here rather than threading a providers slice
				// through draftFromDiagram's own signature, which every
				// other caller (propose.go's runProposeFromDiagram) doesn't
				// need at all.
				draft, err := draftFromDiagram(cmd, cfg, fromDiagram, stack, summary, neighborLedgers, timeout)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan --from-diagram: %w", err)}
				}
				intent = *draft
			case fromCode != "":
				canon, err := sdkeval.Evaluate(ctx, fromCode)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
				}
				if err := json.Unmarshal(canon, &intent); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: parse evaluated intent: %w", err)}
				}
			default:
				data, err := os.ReadFile(args[0])
				if err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
				if err := json.Unmarshal(data, &intent); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: parse intent file: %w", err)}
				}
			}

			providers, err := loadResolveProviders(ctx, cmd, cfg, &providerPath, &source, &providerVersion)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}

			ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, intent.Stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}
			defer closeLedger()

			p, err := resolver.Resolve(ledger, providers, &intent, knownDependents)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}

			hash, err := core.Hash(p)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}

			data, err := json.MarshalIndent(p, "", "  ")
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: marshal proposal: %w", err)}
			}

			planPath, err := writePlanFile(ledgerDir, hash, data)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}
			if out != "" {
				if err := os.WriteFile(out, data, 0o644); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
				}
			}

			outWriter := cmd.OutOrStdout()
			renderPlanReceipt(outWriter, p)
			fmt.Fprintf(outWriter, "\nplan: %s\nubx-proposal: %s\n", planPath, hash)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/ -- also where the plan is saved, at .ubx/plans/<hash>.json")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source)")
	cmd.Flags().StringVar(&out, "out", "", "additionally write the full resolved proposal here (the plan is always saved under .ubx/plans/ regardless)")
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "timeout for provider/schema acquisition, drafting (--from-doc), and evaluation (--from-code) -- one shared budget for the whole command")
	cmd.Flags().StringArrayVar(&knownDependents, "known-dependent", nil,
		"ledger_dir of a neighbor stack to check for cross-stack orphan references before destroying (repeatable)")
	cmd.Flags().StringVar(&fromCode, "from-code", "", "evaluate a TypeScript SDK program (@ubx/sdk) instead of reading an intent file")
	cmd.Flags().StringVar(&fromDoc, "from-doc", "", "path to a markdown authoring document -- transcribes it into an intent/v1 draft via the configured [intent] provider, then resolves it")
	cmd.Flags().StringVar(&fromDiagram, "from-diagram", "", "path to a .d2 diagram -- transcribes its topology into an intent/v1 draft, then resolves it")
	cmd.Flags().StringVar(&stack, "stack", "", "target stack name (required with --from-doc or --from-diagram; the intent-file and --from-code inputs already name their own stack)")
	cmd.Flags().StringVar(&summary, "summary", "", "intent.summary for the draft (only with --from-diagram -- a diagram has no prose to derive one from; defaults to a generated summary naming the stack and resource count if omitted)")
	cmd.Flags().StringArrayVar(&neighborLedgers, "neighbor-ledger", nil, "<stack>=<path> mapping a diagram's own cross-stack reference to a real ledger directory, overriding the \"../<stack>\" convention (repeatable, only with --from-diagram)")
	return cmd
}

// renderPlanReceipt is `ubx plan`'s own human-readable preview: the same
// "make the content visible, not just the decision" posture `ubx why`
// already applies to an accepted proposal (renderProposal, why.go),
// rendered here for one that's ONLY been resolved -- nothing about it is
// accepted or signed yet, this is the review surface a human reads before
// ever running `ubx ship`.
func renderPlanReceipt(out io.Writer, p *core.Proposal) {
	fmt.Fprintf(out, "%s: %s\n", p.Stack, p.Intent.Summary)
	fmt.Fprintf(out, "delta: +%d create(s), ~%d modify(ies), -%d destroy(s)\n",
		len(p.Delta.Creates), len(p.Delta.Modifies), len(p.Delta.Destroys))
	fmt.Fprintf(out, "blast radius: +%d ~%d -%d\n", p.BlastRadius.Creates, p.BlastRadius.Modifies, p.BlastRadius.Destroys)
	if len(p.CostDelta.MonthlyUSD) > 0 {
		fmt.Fprintf(out, "cost delta: $%s/mo\n", p.CostDelta.MonthlyUSD)
	}
	renderModifies(out, p.Delta.Modifies, "")
	renderDestroys(out, p.Delta.Destroys, "", true)

	if len(p.Intent.Assumptions) == 0 && len(p.Intent.Defaults) == 0 && len(p.Intent.Questions) == 0 {
		return
	}
	fmt.Fprintln(out)
	renderNotes(out, "Assumptions", p.Intent.Assumptions)
	renderNotes(out, "Defaults", p.Intent.Defaults)
	renderQuestions(out, p.Intent.Questions)
}

// planFilePath is where `ubx plan` saves a resolved-but-unaccepted
// proposal, and where `ubx ship <hash>` looks for one when hash isn't
// already an accepted id in the ledger (ship.go's own inline-accept
// fallback) -- a local, hash-addressed store alongside the ledger's own
// .ubx/ directory (.ubx/salt, .ubx/lock), never part of the ledger itself:
// a plan is a draft, not a recorded decision, so it has no business living
// under ledger/. Keyed by content hash (docs/architecture.md's "Two-step
// fusion" amendment's own "hash-frozen" mental model) rather than any
// human-chosen name, so `ubx ship <hash>` needs nothing but the hash `ubx
// plan` already printed.
func planFilePath(ledgerDir, hash string) string {
	return filepath.Join(ledgerDir, ".ubx", "plans", hash+".json")
}

// writePlanFile saves a resolved proposal's already-marshaled JSON at its
// own content hash's canonical path, creating .ubx/plans/ if needed.
func writePlanFile(ledgerDir, hash string, data []byte) (string, error) {
	path := planFilePath(ledgerDir, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// readPlanFile reads back a plan `ubx plan` saved, for `ubx ship <hash>`'s
// own inline-accept fallback. A missing file is reported as-is (the
// caller decides how to present "no such plan or accepted proposal").
func readPlanFile(ledgerDir, hash string) (*core.Proposal, error) {
	data, err := os.ReadFile(planFilePath(ledgerDir, hash))
	if err != nil {
		return nil, err
	}
	var p core.Proposal
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse plan file: %w", err)
	}
	return &p, nil
}
