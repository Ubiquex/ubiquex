package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// newRestoreCmd is UBI-227's CLI surface for restoring a stack to an
// earlier ledger head. A restore is a normal proposal, not a rewind --
// the ledger is append-only, so restoring means appending a proposal
// whose delta produces the target head's own shape, resolved against
// CURRENT reality, signed like anything else, and shipped through the
// normal executor path (docs/architecture.md's own core thesis, applied
// here without exception).
//
// Exact-state semantics, decided (2026-08-30, Linear UBI-227): a resource
// that exists now but not in the target head is destroyed. This is a
// literal restore, not a merge. The safety comes from the proposal
// itself, not from softening the semantics -- blast radius names every
// destroy, the diff is reviewable before signing, and "ubx accept" already
// requires --confirm-destroys for any proposal with a real destroy in it,
// the same protection every other destructive change already gets. No new
// safety mechanism is added here.
//
// Reuses ordinary kind:"change" and the unmodified resolver.Resolve
// pipeline -- never a new proposal kind. core.KindRevert ("revert") is a
// real, declared, but never-implemented enum slot (core/proposal.go); it
// stays untouched rather than repurposed here, since restore's own shape
// (real creates and destroys, folded only once shipped) matches
// KindChange, not drift_revert's own modify-only, folds-on-accept
// posture. What names a restore is provenance, not kind -- the same
// mechanism "ubx promote" already established: an additive
// core.IntentSource{Kind: "restore", Ref: <target head>}, rendered by
// cli/why.go's renderIntentSource right next to "promotion" -- so "ubx
// why" can later explain that a change restored a specific earlier head
// without inferring it from a gap in history.
//
// Target-shape reconstruction: AddressesAt(head) names every address the
// target head's own shape actually contains; FoldStateAt(head, addr)
// reconstructs each one's own resolved config exactly as it was then --
// frozen literals, including any value that originally came from a
// cross-stack $cross reference (docs/architecture.md: "Cross-stack refs
// -- pinned imports... never live pointers"). Restore does not re-resolve
// those references against whatever the neighbor is now; it replays the
// pin's own already-resolved value, the same as any other historical
// attribute, and a stale-looking replayed value is visible in the diff
// before anyone signs it, same as any other proposal review.
//
// Create vs modify is NOT inferred by the resolver from an intent's own
// op:"create" -- confirmed live, not assumed: resolveOnce refuses
// ErrCreateTargetExists outright when op:"create" names an address
// FoldState already finds (the same reason an ordinary re-run of an SDK
// program against an already-shipped resource fails today; the SDK has
// no way to emit op:"modify" at all). So this command classifies each
// target address itself, against CURRENT live ledger state (not the
// target head's own), before building the synthetic intent -- this is
// the real meaning of "resolved against current reality" for a restore:
// the target's own attribute VALUES are frozen exactly as recorded, but
// whether each becomes a create, a modify, or nothing changes is computed
// fresh, against what is actually live right now.
//
// A restore of a restore needs no special handling: its own resulting
// head is an ordinary head like any other, valid as a future restore
// target the same way any other head is, and each restore's own
// IntentSource names its own specific target -- ubx why never conflates
// one restore with another.
//
// UBI-228: the target argument accepts a human-readable alias
// (resolveHeadOrAlias, cli/alias.go) as well as a raw hash -- naming a
// head rather than pasting one is exactly the interaction that ticket
// exists for. A raw hash still resolves exactly as it always has; alias
// resolution only ever runs when the argument doesn't already match the
// hash shape.
func newRestoreCmd() *cobra.Command {
	var (
		ledgerDir       string
		stack           string
		providerPath    string
		source          string
		providerVersion string
		out             string
		timeout         time.Duration
		knownDependents []string
	)

	cmd := &cobra.Command{
		Use:   "restore <head-or-alias>",
		Short: "Generate a proposal restoring a stack to an earlier ledger head",
		Long: `Reads <head-or-alias> (a real, already-recorded proposal id -- "ubx history" lists
real candidates -- or an alias assigned via "ubx alias set", UBI-228) and reconstructs
the stack's own exact shape as it existed at that head:
every resource the target head's own chain actually contains, with its own resolved
config frozen exactly as it was recorded then.

The result is resolved against CURRENT reality, not copied: a resource the target head
had but current live state does not is a create; one both have, with a different value,
is a modify; one current live state has but the target head does not is a destroy. Blast
radius and cost delta come out of the ordinary resolver path, real and reviewable, the
same as any other proposal.

This is exact-state restore, not a merge: a resource created since the target head is
destroyed, unconditionally -- the same "does not need its own special handling" posture
"ubx accept --confirm-destroys" already gives every other destructive proposal.

Never touches the ledger directly, exactly like "ubx resolve"/"ubx plan"/"ubx promote" --
the result is saved as a hash-addressed plan file under .ubx/plans/, ready for
"ubx ship <hash>" or "ubx accept" like any other proposal.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			// UBI-228: args[0] is either a real hash or an alias assigned
			// via "ubx alias set" -- resolveHeadOrAlias tries the hash
			// shape first, completely unchanged from before this ticket,
			// so a raw hash here behaves exactly as it always has.
			targetHead, resolvedStack, err := resolveHeadOrAlias(ledgerDir, stack, args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
			}
			if resolvedStack != "" {
				stack = resolvedStack
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
			}
			defer closeLedger()

			targetProposal, err := ledger.Read(targetHead)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
			}
			// The target proposal's own recorded Stack, never the --stack
			// flag/config default -- --stack only ever selects WHICH
			// ledger to open (openLedgerForStack, relevant for a remote
			// store; unused for the default git-local one), and is never
			// guaranteed to be set at all for the common local case. The
			// proposal itself already names its own real stack
			// unambiguously; using anything else here risks synthesizing
			// an intent for the wrong (or an empty) stack.
			restoreStack := targetProposal.Stack

			providers, err := loadResolveProviders(ctx, cmd, cfg, &providerPath, &source, &providerVersion)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
			}

			targetEntries, err := ledger.AddressesAt(targetHead, restoreStack, false)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
			}
			currentEntries, err := ledger.Addresses(restoreStack, false)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
			}

			targetByAddr := make(map[core.Address]bool, len(targetEntries))
			for _, e := range targetEntries {
				targetByAddr[e.Address] = true
			}

			// Exact state: every currently-live address the target head's
			// own shape does not have is destroyed, unconditionally --
			// the "Decided 2026-08-30" reading, no softening.
			var destroys []string
			for _, e := range currentEntries {
				if !targetByAddr[e.Address] {
					destroys = append(destroys, e.Address.String())
				}
			}
			sort.Strings(destroys)

			resources := make([]resolver.ResourceIntent, 0, len(targetEntries))
			for _, e := range targetEntries {
				historical, found, err := ledger.FoldStateAt(targetHead, e.Address)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %s: %w", e.Address, err)}
				}
				if !found {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %s: AddressesAt(%s) reported this address live but FoldStateAt found no state -- this should never happen; please report it", e.Address, targetHead)}
				}

				prov, err := resolver.InferProvider(providers, e.Address.Type, nil)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %s: %w", e.Address, err)}
				}

				var historicalMap map[string]interface{}
				if err := json.Unmarshal(historical, &historicalMap); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %s: decode historical state: %w", e.Address, err)}
				}
				// A schema-Computed attribute (id, arn, ...) is never
				// something a submitted config sets, create or modify
				// alike -- the resolver's own modify path already
				// auto-fills any Computed key a drafted config omits
				// from the ledger's own current value (core/resolver's
				// own OpModify comment), so dropping them here rather
				// than replaying the target head's own old computed
				// value is both safe and the existing convention, never
				// a new one invented for restore.
				config := make(map[string]interface{}, len(historicalMap))
				for k, v := range historicalMap {
					if prov.Schema.IsComputed(e.Address.Type, k) {
						continue
					}
					config[k] = v
				}
				configBytes, err := json.Marshal(config)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %s: %w", e.Address, err)}
				}

				// Create vs modify against CURRENT live ledger state, not
				// the target head's own -- see this command's own doc
				// comment for why the resolver can't infer this itself.
				currentState, existsNow, err := ledger.FoldState(e.Address)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %s: %w", e.Address, err)}
				}
				if existsNow {
					// resolver.Resolve's own OpModify path always appends
					// a Modification for op:"modify", even when the
					// diff is empty -- confirmed live, not assumed (an
					// unconditional append inside that one case, no
					// early return for "nothing actually changed").
					// Skipping unchanged resources here, before they
					// ever reach the resolver, is what makes "a resource
					// present in both and unchanged is left alone" true
					// rather than every existing resource showing a
					// spurious no-op modify. currentState is filtered the
					// SAME way configBytes already was -- comparing a
					// filtered target against an unfiltered current
					// state would make every schema-Computed attribute
					// (id, ...) look like a real diff every single time,
					// since it is present on one side and deliberately
					// absent on the other.
					var currentMap map[string]interface{}
					if err := json.Unmarshal(currentState, &currentMap); err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %s: decode current state: %w", e.Address, err)}
					}
					currentFiltered := make(map[string]interface{}, len(currentMap))
					for k, v := range currentMap {
						if prov.Schema.IsComputed(e.Address.Type, k) {
							continue
						}
						currentFiltered[k] = v
					}
					currentFilteredBytes, err := json.Marshal(currentFiltered)
					if err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %s: %w", e.Address, err)}
					}

					before, after, diffErr := core.DiffAttributes(currentFilteredBytes, configBytes)
					if diffErr != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %s: %w", e.Address, diffErr)}
					}
					if len(before) == 0 && len(after) == 0 {
						continue
					}
					resources = append(resources, resolver.ResourceIntent{
						Type:   e.Address.Type,
						Name:   e.Address.Name,
						Op:     resolver.OpModify,
						Config: configBytes,
					})
					continue
				}
				resources = append(resources, resolver.ResourceIntent{
					Type:   e.Address.Type,
					Name:   e.Address.Name,
					Op:     resolver.OpCreate,
					Config: configBytes,
				})
			}

			intent := &resolver.IntentFile{
				SchemaVersion: 1,
				Kind:          resolver.IntentFileKind,
				Stack:         restoreStack,
				Intent:        core.Intent{Summary: fmt.Sprintf("restore %s to ledger head %s", restoreStack, displayHash(targetHead, false))},
				Resources:     resources,
				Destroys:      destroys,
			}

			p, err := resolver.Resolve(ledger, providers, intent, knownDependents)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
			}

			// The one thing that names WHY this proposal exists -- a
			// provenance claim, never an equality claim, exactly like
			// "promotion" (core/proposal.go's own doc comment): nothing
			// in core.Validate or core/resolver ever reads this field,
			// and the target head advancing or being restored again
			// later never stales this proposal.
			p.Intent.Sources = append(p.Intent.Sources, core.IntentSource{
				Kind: "restore",
				Ref:  targetHead,
			})

			hash, err := core.Hash(p)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
			}
			data, err := json.MarshalIndent(p, "", "  ")
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: marshal proposal: %w", err)}
			}
			planPath, err := writePlanFile(ledgerDir, hash, data)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
			}
			if out != "" {
				if err := os.WriteFile(out, data, 0o644); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("restore: %w", err)}
				}
			}

			outWriter := cmd.OutOrStdout()
			st := newStyler(cmd)
			fmt.Fprintf(outWriter, "restoring %s -> ledger head %s\n", restoreStack, st.Hash(targetHead))
			renderPlanReceipt(outWriter, st, p, planReceiptHeader(st, p.Stack, ""), true)
			fmt.Fprintf(outWriter, "\nplan: %s\nubx-proposal: %s\nnext: %s\n", planPath, st.Blue(hash), nextShipHint([]string{hash}, p.BlastRadius.Destroys > 0))
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack to restore -- required only when .ubx/config's [ledger] store is a remote store; unused for the default git store")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version; unused if config declares [thirdparty_providers])")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source)")
	cmd.Flags().StringVar(&out, "out", "", "additionally write the full resolved proposal here (the plan is always saved under .ubx/plans/ regardless)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "timeout for provider acquisition and schema fetch")
	cmd.Flags().StringArrayVar(&knownDependents, "known-dependent", nil, "ledger_dir of a neighbor stack to check for cross-stack orphan references before destroying (repeatable)")
	return cmd
}
