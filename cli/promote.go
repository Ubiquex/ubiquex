package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/intentprovider"
)

// newPromoteCmd is UBI-55's CLI surface for the promotion design ratified
// in docs/architecture.md's "Environments & promotion" (UBI-14):
// "promote to prod" means the source proposal's own authoring document is
// resolved AGAIN, against the target environment's own reality (its
// providers, its live state, its pins) -- never a copy of the source
// proposal, which would be stale in the target by construction. The
// target proposal's own intent.sources gains one additive "promotion"
// entry naming the source proposal and its own stack base -- evidence of
// history, not a pin: the source chain advancing later never stales the
// promoted proposal (docs/schema.md's own "Amendment: promotion
// evidence").
//
// Reuses the exact same pipeline every other entry point already uses,
// never a second implementation: draftFromDoc/draftFromDiagram (propose.go)
// to re-derive the intent from the source's own document, loadResolveProviders
// (resolve.go) for the target's own providers, resolver.Resolve for the
// re-resolution itself, writePlanFile (plan.go) so the result is
// immediately ship-able via the two-step flow -- promote never touches
// the ledger directly, matching resolve/plan's own "preview, not record"
// posture.
func newPromoteCmd() *cobra.Command {
	var (
		ledgerDir       string
		stack           string
		to              string
		toStack         string
		providerPath    string
		source          string
		providerVersion string
		out             string
		timeout         time.Duration
		knownDependents []string
		summary         string
		neighborLedgers []string
	)

	cmd := &cobra.Command{
		Use:   "promote <proposal-id> --to <target-dir>",
		Short: "Re-resolve an accepted proposal's own authoring document against a target environment, with promotion evidence",
		Long: `Reads an accepted source proposal's own document/diagram authoring source (a
markdown file or .d2 diagram named by its own intent.sources), re-resolves it AGAIN --
never copies the source proposal -- against --to <target-dir>'s own config (providers,
ledger store, live state), and stamps the result's intent.sources with an additive
{"kind":"promotion","ref":"<source proposal id>","base":"<source stack base>"} entry.

Promotion is evidence, not a pin: target values may legitimately differ from the source's
(prod is bigger), and nothing here is validated for equality -- a reviewer sees both
proposals and signs the difference knowingly. The source chain advancing later never
stales the promoted proposal.

The source proposal must be an already-accepted, ledger-recorded proposal, not an
unaccepted "ubx plan" draft -- promotion evidence vouches for something that went through
the normal accept ceremony. The source's own authoring document must still be a real,
readable .md or .d2 file, resolvable from the current working directory the same way
"ubx propose --from-doc/--from-diagram" originally read it (docs/schema.md's own
"Amendment: promotion evidence" names the two known gaps this inherits: an SDK-authored
(--from-code) source's own ref is basename-only and can't be relocated, and a
dialogue-kind (ubx chat) source's own ref is relative to the SOURCE ledger directory and
isn't portable to a different target -- both refused cleanly, by name, rather than
silently mishandled).

Never touches a ledger -- exactly like "ubx resolve"/"ubx plan", this only ever previews;
the result is saved as a hash-addressed plan file under --to's own .ubx/plans/, ready for
"ubx ship <hash>" or "ubx accept" against the target.`,
		Args: cobra.ExactArgs(1),
		// promote has no "finding" concept, the same audit outcome as
		// resolve/plan (UBI-20 exit-code contract): it either resolves or
		// it doesn't. 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return &ExitCodeError{Code: 2, Err: errors.New("promote: --to <target-dir> is required")}
			}

			sourceCfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
			}
			applyStackDefault(cmd, &stack, sourceCfg)

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			sourceLedger, closeSource, err := openLedgerForStack(ctx, ledgerDir, stack, sourceCfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
			}
			defer closeSource()

			p, err := sourceLedger.Read(args[0])
			if err != nil {
				if errors.Is(err, core.ErrProposalNotFound) {
					if _, planErr := readPlanFile(ledgerDir, args[0]); planErr == nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %s is an unaccepted \"ubx plan\" draft, not an accepted proposal -- promotion evidence vouches for a proposal that went through the normal accept ceremony, so promote refuses to build on one that hasn't; accept it first (e.g. \"ubx ship %s\"), then promote the resulting id", args[0], args[0])}
					}
				}
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
			}

			authSource, err := findPromotableSource(p)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
			}

			targetCfg, err := loadConfigFromDir(to, cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: target %s: %w", to, err)}
			}
			targetStack := p.Stack
			if toStack != "" {
				targetStack = toStack
			}

			// UBI-85: same fix as `ubx plan --from-doc` -- a promote that
			// re-drafts from the source document against a TARGET stack
			// that already has some or all of these resources (a repeat
			// promotion, most commonly) has the identical bug otherwise:
			// re-declaring already-promoted resources as fresh creates.
			// Read-only, closed immediately; the later openLedgerForStack
			// call below (unchanged) opens its own separate handle for
			// the real resolve step.
			var knownResources map[string]json.RawMessage
			{
				preLedger, closePreLedger, kerr := openLedgerForStack(ctx, to, targetStack, targetCfg)
				if kerr != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", kerr)}
				}
				knownResources, kerr = knownResourcesForStack(preLedger, targetStack)
				closePreLedger()
				if kerr != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", kerr)}
				}
			}

			var intent *resolver.IntentFile
			switch ext := strings.ToLower(filepath.Ext(authSource.Ref)); ext {
			case ".md":
				intent, err = draftFromDoc(cmd, targetCfg, authSource.Ref, targetStack, timeout, knownResources)
			case ".d2":
				intent, err = draftFromDiagram(cmd, targetCfg, authSource.Ref, targetStack, summary, neighborLedgers, timeout)
			default:
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("%s's own document source (%q) has an unrecognized extension %q -- expected .md or .d2", p.ID, authSource.Ref, ext)}
			}
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: re-resolve %s: %w", authSource.Ref, err)}
			}

			providers, err := loadResolveProviders(ctx, cmd, targetCfg, &providerPath, &source, &providerVersion)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
			}

			targetLedger, closeTarget, err := openLedgerForStack(ctx, to, targetStack, targetCfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
			}
			defer closeTarget()

			np, err := resolver.Resolve(targetLedger, providers, intent, knownDependents)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
			}

			np.Intent.Sources = append(np.Intent.Sources, core.IntentSource{
				Kind: "promotion",
				Ref:  p.ID,
				Base: sourceStackBase(sourceLedger, ledgerDir),
			})

			hash, err := core.Hash(np)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
			}
			data, err := json.MarshalIndent(np, "", "  ")
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: marshal proposal: %w", err)}
			}
			planPath, err := writePlanFile(to, hash, data)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
			}
			if out != "" {
				if err := os.WriteFile(out, data, 0o644); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
				}
			}

			outWriter := cmd.OutOrStdout()
			st := newStyler(cmd)
			fmt.Fprintf(outWriter, "promoted %s -> %s (%s)\n", st.Hash(p.ID), targetStack, to)
			// UBI-72's own show_defaults toggle never applies here --
			// promote re-resolves fresh against the target's own live
			// state/providers, never through an LLM, so Intent.Assumptions/
			// Defaults are always empty.
			renderPlanReceipt(outWriter, st, np, planReceiptHeader(st, np.Stack, ""), true)
			fmt.Fprintf(outWriter, "\nplan: %s\nubx-proposal: %s\nnext: %s\n", planPath, st.Blue(hash), nextShipHint([]string{hash}, np.BlastRadius.Destroys > 0))
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing the SOURCE proposal's own ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack's ledger to open the source proposal from -- required only when .ubx/config's [ledger] store is a remote store; unused for the default git store")
	cmd.Flags().StringVar(&to, "to", "", "target environment's own directory -- its own .ubx/config, ledger, and providers, independent of --ledger-dir (required)")
	cmd.Flags().StringVar(&toStack, "to-stack", "", "target stack name, if it differs from the source proposal's own stack name (defaults to the source's own stack -- the common case: same stack, different environment directory)")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary for the target (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address for the target, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version; unused if --to's own config declares [providers])")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire for the target (required with --source)")
	cmd.Flags().StringVar(&out, "out", "", "additionally write the full resolved proposal here (the plan is always saved under --to's own .ubx/plans/ regardless)")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "timeout for re-drafting (a .md source's own intent-provider round trip) and for the target's own provider acquisition/schema fetch -- one shared budget for the whole command")
	cmd.Flags().StringArrayVar(&knownDependents, "known-dependent", nil, "ledger_dir of a neighbor stack to check for cross-stack orphan references before destroying, in the TARGET's own graph (repeatable)")
	cmd.Flags().StringVar(&summary, "summary", "", "intent.summary override for a .d2-sourced proposal (only relevant when the source's own document is a diagram; defaults to a generated summary naming the target stack and resource count)")
	cmd.Flags().StringArrayVar(&neighborLedgers, "neighbor-ledger", nil, "<stack>=<path> mapping a diagram's own cross-stack reference to a real ledger directory in the TARGET's own graph, overriding the \"../<stack>\" convention (repeatable, only relevant for a .d2-sourced proposal)")
	return cmd
}

// findPromotableSource picks the one intent.sources entry ubx promote can
// actually re-resolve from: the "document" kind (a .md or .d2 file still
// reachable from disk), stamped by ubx propose --from-doc/--from-diagram
// or ubx plan's own equivalent modes. A "promotion" source itself is
// never picked (it names a proposal id, not a file -- re-promoting an
// already-promoted proposal re-resolves its own ORIGINAL document again,
// see docs/schema.md's own "Amendment: promotion evidence" for why the
// chain is walked one hop at a time rather than flattened). "dialogue"
// and SDK-authored "document" sources are named, known gaps (same
// amendment) -- refused with the specific reason rather than silently
// mishandled.
func findPromotableSource(p *core.Proposal) (*core.IntentSource, error) {
	for i := range p.Intent.Sources {
		if p.Intent.Sources[i].Kind == intentprovider.SourceKindDocument {
			ref := p.Intent.Sources[i].Ref
			switch strings.ToLower(filepath.Ext(ref)) {
			case ".md", ".d2":
				return &p.Intent.Sources[i], nil
			default:
				return nil, fmt.Errorf("%s's own document source (%q) was authored via an SDK program -- --from-code stamps only the entry file's own basename (goeval/tseval/pyeval's own stampDocumentSource), discarding the directory needed to relocate it, so promote can't re-evaluate it (docs/schema.md's own \"Amendment: promotion evidence\" names this gap; a follow-up to give SDK-authored sources a relocatable ref is recommended, not built this session)", p.ID, ref)
			}
		}
	}
	for i := range p.Intent.Sources {
		if p.Intent.Sources[i].Kind == intentprovider.SourceKindDialogue {
			return nil, fmt.Errorf("%s was drafted via \"ubx chat\" (a captured dialogue) -- its own ref (%s) is relative to the SOURCE ledger directory, and re-stamping the identical ref against a different target ledger would point at the wrong (or nonexistent) file, a broken provenance claim; promote refuses rather than emit one (docs/schema.md's own \"Amendment: promotion evidence\" names this gap; a follow-up to give dialogue captures a portable ref is recommended, not built this session)", p.ID, p.Intent.Sources[i].Ref)
		}
	}
	return nil, fmt.Errorf("%s has no re-resolvable authoring source in intent.sources -- promote needs a \"document\" source (from ubx propose --from-doc/--from-diagram, or ubx plan's own equivalent) to re-resolve against the target", p.ID)
}

// sourceStackBase names the promotion source's own "base" (docs/schema.md's
// own "Amendment: promotion evidence"): the remote LedgerStore address if
// one is configured, or the source's own --ledger-dir for the default
// git-local store, which carries no separate base-store concept of its
// own -- either way, the "staging" half of why's own "promoted from
// staging/8f3c…" rendering.
func sourceStackBase(l *core.Ledger, ledgerDir string) string {
	if b := l.BaseStore(); b != "" {
		return b
	}
	return ledgerDir
}
