package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// newPromoteCmd is UBI-55's CLI surface for the promotion design ratified
// in docs/architecture.md's "Environments & promotion" (UBI-14):
// "promote to prod" means the source proposal's own authoring source is
// resolved AGAIN, against the target environment's own reality (its
// providers, its live state, its pins) -- never a copy of the source
// proposal, which would be stale in the target by construction. The
// target proposal's own intent.sources gains one additive "promotion"
// entry naming the source proposal and its own stack base -- evidence of
// history, not a pin: the source chain advancing later never stales the
// promoted proposal (docs/schema.md's own "Amendment: promotion
// evidence").
//
// UBI-224 removed the .md/.d2 re-drafting paths this command used to
// dispatch to (draftFromDoc/draftFromDiagram, propose.go) along with the
// markdown/diagram authoring mediums themselves, and refuses to promote a
// dialogue-sourced proposal outright (promoteDialogueSourceRefusal, below)
// -- chat's own re-resolution machinery is gone with the medium that
// produced dialogue sources in the first place. An SDK-sourced proposal
// (.go/.ts/.py) is the one real re-derivable case left: reuses the exact
// same pipeline every other SDK entry point already uses, never a second
// implementation -- loadResolveProviders (resolve.go) for the target's
// own providers, resolver.Resolve for the re-resolution itself,
// writePlanFile (plan.go) so the result is immediately ship-able via the
// two-step flow -- promote never touches the ledger directly, matching
// resolve/plan's own "preview, not record" posture.
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
	)

	cmd := &cobra.Command{
		Use:   "promote <proposal-id> --to <target-dir>",
		Short: "Re-resolve an accepted proposal's own authoring source against a target environment, with promotion evidence",
		Long: `Reads an accepted source proposal's own authoring source (an SDK program, named by its
own intent.sources), re-derives the intent AGAIN against --to <target-dir>'s own config
(providers, ledger store, live state) -- never copies the source proposal, which would be
stale in the target by construction -- and stamps the result's intent.sources with an
additive {"kind":"promotion","ref":"<source proposal id>","base":"<source stack base>"}
entry.

Promotion is evidence, not a pin: target values may legitimately differ from the source's
(prod is bigger), and nothing here is validated for equality -- a reviewer sees both
proposals and signs the difference knowingly. The source chain advancing later never
stales the promoted proposal.

The source proposal must be an already-accepted, ledger-recorded proposal, not an
unaccepted "ubx plan" draft -- promotion evidence vouches for something that went through
the normal accept ceremony.

How an SDK-sourced proposal is re-derived (UBI-60): the entry file is re-read from disk
(its own ref is the exact path given at resolve time, resolvable the same "same working
directory" way) and its content_hash is re-checked FIRST -- an unchanged file is re-run
through the real evaluator against the target's own real context (UBI-81: a program can
legitimately read the target stack's own name/config and draft differently, correctly, per
target); a CHANGED file is refused outright, since that's new intent, not a promotion.

A proposal whose only re-resolvable source is a captured "ubx chat" dialogue is refused
outright (UBI-224 removed chat as an authoring medium, along with the re-resolution
machinery a dialogue-sourced promotion depended on) -- the dialogue capture itself stays
readable by "ubx why", just not re-promotable.

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

			var intent *resolver.IntentFile
			switch authSource.Kind {
			case core.SourceKindDocument:
				switch ext := strings.ToLower(filepath.Ext(authSource.Ref)); ext {
				case ".go", ".ts", ".py":
					intent, err = promoteSDKSource(ctx, cmd, authSource)
				default:
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("%s's own document source (%q) has an unrecognized extension %q -- expected .go, .ts, or .py", p.ID, authSource.Ref, ext)}
				}
			case core.SourceKindDialogue:
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("%s's own authoring source is a captured \"ubx chat\" dialogue (%s) -- chat was removed as an authoring medium (UBI-224), and promote no longer re-resolves a dialogue source; \"ubx why %s\" still explains it", p.ID, authSource.Ref, p.ID)}
			default:
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("%s's own authoring source has an unrecognized kind %q", p.ID, authSource.Kind)}
			}
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: re-resolve %s: %w", authSource.Ref, err)}
			}

			// UBI-60: SDK/dialogue sources may carry the SOURCE stack's own
			// baked-in name (an SDK program's own sdk.Stack("...") call is
			// fixed at authoring time, and a dialogue's converged draft was
			// captured against the source; unlike draftFromDoc/
			// draftFromDiagram, neither has a way to name the target stack
			// as it drafts) -- forced to targetStack here, uniformly,
			// regardless of which path produced intent, exactly matching
			// what every other path already produces by construction.
			intent.Stack = targetStack

			// UBI-60: matches resolve.go's/plan.go's own shared convergence
			// point exactly (blueprint_calls/overrides apply regardless of
			// which medium produced the intent) -- a real, pre-existing gap
			// found while building the SDK path (which needs this the same
			// way --from-code already does): promote's own .md/.d2 paths
			// never called either, so a blueprint-calling document/diagram
			// promoted via `ubx promote` silently skipped expansion
			// entirely. Fixed for all four paths at once, here, the one
			// shared point every path already converges through.
			if err := blueprint.ExpandCalls(ctx, intent); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
			}
			if err := blueprint.ApplyOverrides(intent); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("promote: %w", err)}
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
			// UBI-72's own show_defaults toggle is hardcoded true here --
			// promote's own SDK path re-resolves fresh against the target's
			// own live state/providers, so Intent.Assumptions/Defaults are
			// always empty in practice; shown in full regardless, matching
			// every other path's own posture, never silently collapsed.
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
	cmd.Flags().StringVar(&source, "source", "", "provider source address for the target, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version; unused if --to's own config declares [thirdparty_providers])")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire for the target (required with --source)")
	cmd.Flags().StringVar(&out, "out", "", "additionally write the full resolved proposal here (the plan is always saved under --to's own .ubx/plans/ regardless)")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "timeout for re-evaluating the SDK program and for the target's own provider acquisition/schema fetch -- one shared budget for the whole command")
	cmd.Flags().StringArrayVar(&knownDependents, "known-dependent", nil, "ledger_dir of a neighbor stack to check for cross-stack orphan references before destroying, in the TARGET's own graph (repeatable)")
	return cmd
}

// findPromotableSource picks the one intent.sources entry ubx promote
// might be able to re-derive from: a "document" kind (a .go/.ts/.py file
// still reachable from disk -- UBI-60 closed the SDK-authored gap, see
// promoteSDKSource's own doc comment) or a "dialogue" kind (a captured
// ubx chat session, from before UBI-224 removed chat as an authoring
// medium -- the caller refuses this kind outright rather than
// re-resolving it, see newPromoteCmd's own RunE). A "promotion" source
// itself is never picked (it names a proposal id, not a file --
// re-promoting an already-promoted proposal re-resolves its own
// ORIGINAL source again, see docs/schema.md's own "Amendment: promotion
// evidence" for why the chain is walked one hop at a time rather than
// flattened). "document" is checked first, matching the search order
// UBI-55 already established (a proposal carrying both would be
// unusual, but document takes priority the same way it always has).
func findPromotableSource(p *core.Proposal) (*core.IntentSource, error) {
	for i := range p.Intent.Sources {
		if p.Intent.Sources[i].Kind == core.SourceKindDocument {
			return &p.Intent.Sources[i], nil
		}
	}
	for i := range p.Intent.Sources {
		if p.Intent.Sources[i].Kind == core.SourceKindDialogue {
			return &p.Intent.Sources[i], nil
		}
	}
	return nil, fmt.Errorf("%s has no re-resolvable authoring source in intent.sources -- promote needs a \"document\" source (from ubx resolve/plan --from-code) to re-resolve against the target", p.ID)
}

// promoteSDKSource is UBI-60's own SDK-promotion path (docs/schema.md's
// "Amendment: promotion evidence" named this a known gap through UBI-55:
// --from-code's own document source used to stamp only the entry file's
// own basename, discarding the directory needed to relocate it at all --
// closed by goeval/tseval/pyeval's own stampDocumentSource now storing
// the entry file's real given path, the same convention a .md/.d2
// source's own Ref already used).
//
// The real design decision this ticket confirmed (UBI-81's own "a
// document can legitimately produce different, correct results
// depending on which stack it's evaluated against" finding): promoting a
// frozen, already-evaluated intent blob would silently carry the
// ORIGINAL stack's own context-derived assumptions into a target where
// they might be wrong. So this re-RUNS the real, pinned program file
// through the real evaluator -- the identical mechanism `ubx resolve
// --from-code`/`ubx plan --from-code` already use (evaluateSDKProgram,
// resolve.go), never a second implementation -- against the TARGET's own
// real context, picking up whatever UBI-81 context-aware behavior the
// program itself expresses.
//
// content_hash is checked FIRST, before any evaluation: authSource's own
// hash (computed when the source proposal was originally drafted) must
// still match the file currently on disk. A match means an ordinary,
// unchanged, re-runnable document -- proceed. A mismatch means the file
// was edited since -- that's new intent, never silently promoted; refuse
// by name, naming both hashes, exactly the same "explicit refusal,
// never a silent guess" posture this whole command already holds to.
func promoteSDKSource(ctx context.Context, cmd *cobra.Command, authSource *core.IntentSource) (*resolver.IntentFile, error) {
	data, err := os.ReadFile(authSource.Ref)
	if err != nil {
		return nil, fmt.Errorf("re-read SDK program %s: %w", authSource.Ref, err)
	}
	sum := sha256.Sum256(data)
	currentHash := "sha256:" + hex.EncodeToString(sum[:])
	if currentHash != authSource.ContentHash {
		return nil, fmt.Errorf("%s has changed since the source proposal was drafted (was %s, now %s) -- promote only re-runs an UNCHANGED program; a changed program is new intent, not a promotion. Draft it fresh instead, e.g. \"ubx plan --from-code %s\"", authSource.Ref, authSource.ContentHash, currentHash, authSource.Ref)
	}

	canon, receipts, blueprintRefs, err := evaluateSDKProgram(ctx, authSource.Ref)
	if err != nil {
		return nil, err
	}
	for _, r := range receipts {
		fmt.Fprintln(cmd.OutOrStdout(), r)
	}

	var intent resolver.IntentFile
	if err := json.Unmarshal(canon, &intent); err != nil {
		return nil, fmt.Errorf("parse evaluated intent: %w", err)
	}

	// Matches resolve.go's own --from-code sequence exactly (per-language
	// blueprint direct-call provenance completion).
	switch strings.ToLower(filepath.Ext(authSource.Ref)) {
	case ".go":
		if err := blueprint.StampDirectCallProvenance(ctx, authSource.Ref, &intent); err != nil {
			return nil, err
		}
	case ".ts":
		if err := blueprint.StampDirectCallProvenanceTS(ctx, authSource.Ref, &intent); err != nil {
			return nil, err
		}
	case ".py":
		if err := blueprint.StampDirectCallProvenancePy(&intent, blueprintRefs); err != nil {
			return nil, err
		}
	}
	return &intent, nil
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
