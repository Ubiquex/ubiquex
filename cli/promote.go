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
		Short: "Re-resolve an accepted proposal's own authoring source against a target environment, with promotion evidence",
		Long: `Reads an accepted source proposal's own authoring source (a markdown file, a .d2
diagram, an SDK program, or a captured "ubx chat" dialogue, named by its own
intent.sources), re-derives the intent AGAIN against --to <target-dir>'s own config
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

How each authoring source is re-derived (UBI-60):
  - .md/.d2: re-read from disk and re-drafted/re-parsed exactly like "ubx propose
    --from-doc/--from-diagram" originally did -- resolvable from the current working
    directory the same way the original command was.
  - An SDK program (--from-code, .go/.ts/.py): re-read from disk (its own ref is the
    exact path given at drafting time, resolvable the same "same working directory" way)
    and its content_hash is re-checked FIRST -- an unchanged file is re-run through the
    real evaluator against the target's own real context (UBI-81: a program can
    legitimately read the target stack's own name/config and draft differently, correctly,
    per target); a CHANGED file is refused outright, since that's new intent, not a
    promotion.
  - A dialogue (ubx chat): the FINAL, converged intent/v1 draft captured in the dialogue's
    own .dlg.json (relative to the SOURCE proposal's own --ledger-dir, not the current
    working directory) is what gets re-resolved against the target -- never re-run through
    an LLM a second time. The dialogue capture itself is carried forward as pinned
    evidence in the promoted proposal's own intent.sources, not treated as a re-runnable
    artifact.

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
			switch authSource.Kind {
			case intentprovider.SourceKindDocument:
				switch ext := strings.ToLower(filepath.Ext(authSource.Ref)); ext {
				case ".md":
					intent, err = draftFromDoc(cmd, targetCfg, authSource.Ref, targetStack, timeout, knownResources)
				case ".d2":
					intent, err = draftFromDiagram(cmd, targetCfg, authSource.Ref, targetStack, summary, neighborLedgers, timeout)
				case ".go", ".ts", ".py":
					intent, err = promoteSDKSource(ctx, cmd, authSource)
				default:
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("%s's own document source (%q) has an unrecognized extension %q -- expected .md, .d2, .go, .ts, or .py", p.ID, authSource.Ref, ext)}
				}
			case intentprovider.SourceKindDialogue:
				intent, err = promoteDialogueSource(ledgerDir, p, authSource)
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
			// promote's .md/.d2/SDK paths re-resolve fresh against the
			// target's own live state/providers, never through an LLM, so
			// Intent.Assumptions/Defaults are always empty for those three.
			// The dialogue path is the one real exception (UBI-60):
			// promoteDialogueSource re-resolves the ORIGINAL converged
			// draft, Assumptions/Defaults and all, so a dialogue-sourced
			// promotion's own receipt genuinely can show real AI defaults --
			// always shown in full here regardless, matching every other
			// path's own posture, never silently collapsed.
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
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "timeout for re-drafting (a .md source's own intent-provider round trip) and for the target's own provider acquisition/schema fetch -- one shared budget for the whole command")
	cmd.Flags().StringArrayVar(&knownDependents, "known-dependent", nil, "ledger_dir of a neighbor stack to check for cross-stack orphan references before destroying, in the TARGET's own graph (repeatable)")
	cmd.Flags().StringVar(&summary, "summary", "", "intent.summary override for a .d2-sourced proposal (only relevant when the source's own document is a diagram; defaults to a generated summary naming the target stack and resource count)")
	cmd.Flags().StringArrayVar(&neighborLedgers, "neighbor-ledger", nil, "<stack>=<path> mapping a diagram's own cross-stack reference to a real ledger directory in the TARGET's own graph, overriding the \"../<stack>\" convention (repeatable, only relevant for a .d2-sourced proposal)")
	return cmd
}

// findPromotableSource picks the one intent.sources entry ubx promote can
// actually re-derive from: a "document" kind (a .md/.d2/.go/.ts/.py file
// still reachable from disk -- UBI-60 closed the SDK-authored gap, see
// promoteSDKSource's own doc comment) or a "dialogue" kind (a captured
// ubx chat session -- UBI-60 closed this gap too, see
// promoteDialogueSource's own doc comment), stamped by ubx propose/plan's
// own drafting modes or ubx chat's own /save. A "promotion" source itself
// is never picked (it names a proposal id, not a file -- re-promoting an
// already-promoted proposal re-resolves its own ORIGINAL source again,
// see docs/schema.md's own "Amendment: promotion evidence" for why the
// chain is walked one hop at a time rather than flattened). "document"
// is checked first, matching the search order UBI-55 already established
// (a proposal carrying both would be unusual, but document takes
// priority the same way it always has).
func findPromotableSource(p *core.Proposal) (*core.IntentSource, error) {
	for i := range p.Intent.Sources {
		if p.Intent.Sources[i].Kind == intentprovider.SourceKindDocument {
			return &p.Intent.Sources[i], nil
		}
	}
	for i := range p.Intent.Sources {
		if p.Intent.Sources[i].Kind == intentprovider.SourceKindDialogue {
			return &p.Intent.Sources[i], nil
		}
	}
	return nil, fmt.Errorf("%s has no re-resolvable authoring source in intent.sources -- promote needs a \"document\" source (from ubx propose --from-doc/--from-diagram/--from-code, or ubx plan's own equivalent) or a \"dialogue\" source (ubx chat) to re-resolve against the target", p.ID)
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

// promoteDialogueSource is UBI-60's own dialogue-promotion path
// (docs/schema.md's "Amendment: promotion evidence" named this a known
// gap through UBI-55: a dialogue's own Ref is relative to the SOURCE
// proposal's own --ledger-dir, never portable to an arbitrary --to
// target directory the way a .md/.d2 Ref -- read straight off the
// current working directory -- already is).
//
// The real design decision this ticket confirmed: unlike an SDK program
// (deterministic code, cheap and safe to re-run), a dialogue is NOT
// re-run through the LLM a second time -- there is no clean way to
// replay a multi-turn conversation deterministically, and doing so could
// change the drafted STRUCTURE itself, not just a context-derived value
// within it, which would defeat the whole point of promoting a reviewed
// result. Instead, the FINAL, converged intent/v1 draft the dialogue
// already produced (Dialogue.Draft, embedded in the .dlg.json at /save
// time) is what gets re-resolved against the target -- the exact same
// "re-resolve the intent, don't copy the proposal" shape every other
// promotion path already has, just skipping the re-drafting step since
// there's nothing left to draft. The dialogue capture itself (and the
// intent_provider entry naming which model produced it) is carried
// forward from the ORIGINAL accepted proposal's own intent.sources onto
// the new intent -- pinned evidence of how the draft was produced, never
// treated as something promote could re-run.
func promoteDialogueSource(ledgerDir string, p *core.Proposal, authSource *core.IntentSource) (*resolver.IntentFile, error) {
	fullPath := filepath.Join(ledgerDir, authSource.Ref)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("re-read dialogue capture %s: %w", fullPath, err)
	}
	var dlg intentprovider.Dialogue
	if err := json.Unmarshal(data, &dlg); err != nil {
		return nil, fmt.Errorf("parse dialogue capture %s: %w", fullPath, err)
	}
	if dlg.Draft == nil {
		return nil, fmt.Errorf("dialogue capture %s has no converged draft recorded", fullPath)
	}

	intent := *dlg.Draft
	for _, s := range p.Intent.Sources {
		if s.Kind == intentprovider.SourceKindDialogue || s.Kind == intentprovider.SourceKindIntentProvider {
			intent.Intent.Sources = append(intent.Intent.Sources, s)
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
