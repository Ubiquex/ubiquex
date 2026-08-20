package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/provider"
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
		jsonOut         bool
		all             bool
	)

	cmd := &cobra.Command{
		Use:   "status [flags]",
		Short: "Report every resource the ledger knows about, optionally comparing against live state",
		Long: `Walks every resource the ledger has ever recorded (all stacks by default, or one via --stack)
and reports its address and latest recorded state. Ledger-only by default -- fast, no provider, no
credentials needed. With --drift, also reads each resource's live state (reusing its own recorded
resolution.inputs lookup key, exactly like ubx scan would) and classifies it clean, drifted, or
unreadable; a failure on one resource is recorded and the walk continues, never aborting the report.

UBI-71: a clean resource's own detail line is hidden by default once --drift is given -- the signal
(what drifted, what's unreadable) buried in noise otherwise on a mostly-clean fleet -- collapsing to a
trailing "N clean (--all to show)" count instead. --all shows every resource's own line regardless of
classification, the pre-UBI-71 behavior.

Exit code is a CI contract: 0 if clean (or ledger-only, which has nothing to report drift on), 1 if
anything drifted, 2 if anything was unreadable or the command failed outright. Whichever is worse
always wins if more than one condition holds.

"All stacks by default" is a git-local-only capability: a remote .ubx/config [ledger] store addresses
one chain per stack, so there is no "every stack" to enumerate there -- --stack becomes required.`,
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

			// A remote store has no "every stack" to show at all -- its own
			// addressing is one chain per stack (docs/architecture.md --
			// Addressing), so openLedgerForStack requires --stack there
			// exactly like ubx ship/why's own bare-id lookup does; the
			// git-local default above (empty stack = every stack) is
			// completely unaffected, since it never reaches that check.
			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
			}
			defer closeLedger()

			fleet, err := ledger.Fleet(stack)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
			}

			out := cmd.OutOrStdout()
			st := newStyler(cmd)

			if !drift {
				resources := make([]statusResourceJSON, 0, len(fleet))
				for _, e := range fleet {
					if !jsonOut {
						// UBI-68: everything after the address's own colon --
						// kind, hash, "accepted <timestamp>" -- reads as one
						// dim metadata block, so the address itself (printed
						// outside forceDim, untouched) reads brightest.
						fmt.Fprintf(out, "%s: %s\n", e.Address, st.forceDim(fmt.Sprintf("%s %s (accepted %s)", e.Kind, st.Hash(e.ProposalID), e.AcceptedAt)))
					}
					resources = append(resources, statusResourceJSON{
						Address:    addressToJSON(e.Address),
						Kind:       string(e.Kind),
						ProposalID: e.ProposalID,
						AcceptedAt: e.AcceptedAt,
					})
				}
				if jsonOut {
					payload := statusJSON{
						Format:       jsonFormatVersion,
						DriftChecked: false,
						Resources:    resources,
						Summary:      statusSummaryJSON{Total: len(fleet)},
					}
					if err := writeJSON(out, payload); err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
					}
					return nil
				}
				fmt.Fprintf(out, "%d resource(s) (ledger-only, no live comparison)\n", len(fleet))
				return nil
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			salt, err := ledger.Salt()
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
			}

			var driftedCount, unreadableCount int
			resources := make([]statusResourceJSON, 0, len(fleet))

			// UBI-71: every per-resource line renders into this buffer
			// during the walk, not directly to `out` -- the walk itself
			// doesn't yet know the final drifted/unreadable counts the
			// header names, and printing a clean resource's own line at
			// all is conditional on --all (classifyFleetEntry/
			// unreadableNoLookup/unreadableProviderUnavailable already
			// skip printing entirely when jsonOut is true, so writing
			// into a throwaway buffer in that case is harmless -- it's
			// simply never flushed). Flushed once, after the walk
			// completes, in the exact per-resource order the walk itself
			// produced them.
			var detail bytes.Buffer
			var dw io.Writer = &detail
			if jsonOut {
				dw = io.Discard
			}

			// docs/executor.md's own "Scan/status/fleet" section: a real
			// [thirdparty_providers] table means grouping the fleet by each
			// resource's own recorded provider instead of one --source
			// flag. A legacy/adopted entry with no recorded provider of
			// its own gets one inferred fresh by type, against every
			// declared provider -- the identical mechanism `ubx resolve`
			// already uses for a brand-new resource
			// (resolver.InferProvider), built lazily here (only launching
			// every declared provider if at least one entry actually
			// needs it) rather than always paying for it up front.
			if len(cfg.ThirdpartyProviders) > 0 || len(cfg.Providers) > 0 {
				warnIfLegacyProviderFlagsGiven(cmd)
				pool, err := newProviderPool(salt, cfg.ThirdpartyProviders, cfg.Providers, cfg.ProviderConfigs)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
				}
				defer pool.Close()

				var declared []resolver.DeclaredProvider
				for _, e := range fleet {
					if len(e.Lookup) == 0 {
						unreadableCount++
						resources = append(resources, unreadableNoLookup(dw, st, jsonOut, e))
						continue
					}

					provSource, provVersion := "", ""
					switch {
					case e.Provider != nil:
						provSource, provVersion = e.Provider.Source, e.Provider.Version
					default:
						if declared == nil {
							declared, err = declaredProvidersForInference(ctx, pool, resolvedProviderVersions(cfg))
							if err != nil {
								return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
							}
						}
						winner, ierr := resolver.InferProvider(declared, e.Address.Type, nil)
						if ierr != nil {
							unreadableCount++
							resources = append(resources, unreadableProviderUnavailable(dw, st, jsonOut, e, ierr))
							continue
						}
						provSource, provVersion = winner.Source, winner.Version
					}

					app, providerConfig, gerr := pool.Get(ctx, provSource, provVersion)
					if gerr != nil {
						unreadableCount++
						resources = append(resources, unreadableProviderUnavailable(dw, st, jsonOut, e, gerr))
						continue
					}

					entry, drifted, unreadable := classifyFleetEntry(ctx, dw, st, jsonOut, all, ledger, app, providerConfig, provSource, e)
					if drifted {
						driftedCount++
					}
					if unreadable {
						unreadableCount++
					}
					resources = append(resources, entry)
				}
			} else {
				path, _, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
				}
				client, err := provider.Launch(ctx, path)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
				}
				defer client.Close()
				stateReader := newStateReader(client.Provider, salt, source)

				for _, e := range fleet {
					if len(e.Lookup) == 0 {
						unreadableCount++
						resources = append(resources, unreadableNoLookup(dw, st, jsonOut, e))
						continue
					}
					entry, drifted, unreadable := classifyFleetEntry(ctx, dw, st, jsonOut, all, ledger, stateReader, json.RawMessage(providerConfig), source, e)
					if drifted {
						driftedCount++
					}
					if unreadable {
						unreadableCount++
					}
					resources = append(resources, entry)
				}
			}

			if jsonOut {
				payload := statusJSON{
					Format:       jsonFormatVersion,
					DriftChecked: true,
					Resources:    resources,
					Summary: statusSummaryJSON{
						Total:      len(fleet),
						Drifted:    driftedCount,
						Unreadable: unreadableCount,
					},
				}
				if err := writeJSON(out, payload); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("status: %w", err)}
				}
			} else {
				// UBI-71: header names the drifted count up front (the
				// walk itself already ran, so this isn't a guess), then
				// the buffered per-resource detail flushes in its own
				// original order, then a clean-count hint when anything
				// was hidden, then the pre-existing summary line
				// (unchanged, several tests key off its exact wording).
				if stack != "" {
					fmt.Fprintf(out, "Drift  %s · %d of %d resource(s) drifted\n\n", stack, driftedCount, len(fleet))
				} else {
					fmt.Fprintf(out, "Drift  %d of %d resource(s) drifted\n\n", driftedCount, len(fleet))
				}
				out.Write(detail.Bytes())
				cleanCount := len(fleet) - driftedCount - unreadableCount
				if !all && cleanCount > 0 {
					fmt.Fprintf(out, "%d clean (--all to show)\n", cleanCount)
				}
				// UBI-76: blank line before the closing summary, bold
				// throughout, whole line green when the run is fully clean
				// (the same affirmative-success convention `ubx init`
				// already established) -- yellow/red per-count otherwise,
				// matching UBI-75's own per-count color scheme for ship's
				// closing summary.
				fmt.Fprintln(out)
				if driftedCount == 0 && unreadableCount == 0 {
					fmt.Fprintln(out, st.GreenBold(fmt.Sprintf("%d resource(s), %d drifted, %d unreadable", len(fleet), driftedCount, unreadableCount)))
				} else {
					fmt.Fprintln(out, st.forceBold(fmt.Sprintf("%d resource(s), %s, %s",
						len(fleet),
						st.Yellow(fmt.Sprintf("%d drifted", driftedCount)),
						st.Red(fmt.Sprintf("%d unreadable", unreadableCount)))))
				}
			}

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
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text")
	cmd.Flags().BoolVar(&all, "all", false, "with --drift, also show each clean resource's own line (hidden by default, UBI-71); no effect without --drift or with --json")

	return cmd
}

// statusJSON is `ubx status --json`'s payload (UBI-20 workstream 2,
// docs/exit-codes.mdx). DriftChecked distinguishes "0 drifted because
// --drift wasn't given" from "0 drifted because --drift found nothing" --
// Resources' Status field is empty in the former case, never a guess.
type statusJSON struct {
	Format       int                  `json:"format"`
	DriftChecked bool                 `json:"drift_checked"`
	Resources    []statusResourceJSON `json:"resources"`
	Summary      statusSummaryJSON    `json:"summary"`
}

type statusResourceJSON struct {
	Address    addressJSON `json:"address"`
	Kind       string      `json:"kind"`
	ProposalID string      `json:"proposal_id"`
	AcceptedAt string      `json:"accepted_at"`
	Status     string      `json:"status,omitempty"` // "clean" | "drifted" | "unreadable"; omitted if !DriftChecked
	Reason     string      `json:"reason,omitempty"` // set when Status == "unreadable"
}

type statusSummaryJSON struct {
	Total      int `json:"total"`
	Drifted    int `json:"drifted"`
	Unreadable int `json:"unreadable"`
}

// unreadableNoLookup builds e's "unreadable" entry for the "no lookup key
// recorded" case -- factored out of the single-provider walk (this
// session, UBI-43) so both it and the new multi-provider walk report the
// identical reason string rather than two copies drifting apart.
func unreadableNoLookup(out io.Writer, st *styler, jsonOut bool, e core.FleetEntry) statusResourceJSON {
	entry := statusResourceJSON{
		Address:    addressToJSON(e.Address),
		Kind:       string(e.Kind),
		ProposalID: e.ProposalID,
		AcceptedAt: e.AcceptedAt,
		Status:     "unreadable",
		Reason:     "no lookup key recorded for this resource (authored before the resolution.inputs lookup amendment, or never had one)",
	}
	if !jsonOut {
		// UBI-68: same address-brightest/metadata-dim hierarchy as the
		// ledger-only line above -- the reason after "--" stays outside
		// forceDim, exactly as named in the ticket (kind/hash/accepted
		// timestamp only).
		fmt.Fprintf(out, "unreadable: %s: %s -- %s\n",
			e.Address, st.forceDim(fmt.Sprintf("%s %s (accepted %s)", e.Kind, displayHash(e.ProposalID, false), e.AcceptedAt)), entry.Reason)
	}
	return entry
}

// unreadableProviderUnavailable builds e's "unreadable" entry when its own
// provider couldn't be resolved or launched at all (an undeclared/
// ambiguous type during inference, or a real launch failure from the
// pool) -- the multi-provider walk's own analogue of the failures
// classifyFleetEntry's own RunScan call already reports, worded to match
// core/executor/ship.go's "provider unavailable: %v" (docs/executor.md's
// own error taxonomy) since both are the same underlying condition, just
// surfaced from different call sites.
func unreadableProviderUnavailable(out io.Writer, st *styler, jsonOut bool, e core.FleetEntry, err error) statusResourceJSON {
	entry := statusResourceJSON{
		Address:    addressToJSON(e.Address),
		Kind:       string(e.Kind),
		ProposalID: e.ProposalID,
		AcceptedAt: e.AcceptedAt,
		Status:     "unreadable",
		Reason:     fmt.Sprintf("provider unavailable: %v", err),
	}
	if !jsonOut {
		// UBI-68: see unreadableNoLookup's own comment -- same hierarchy.
		fmt.Fprintf(out, "unreadable: %s: %s -- %s\n",
			e.Address, st.forceDim(fmt.Sprintf("%s %s (accepted %s)", e.Kind, displayHash(e.ProposalID, false), e.AcceptedAt)), entry.Reason)
	}
	return entry
}

// classifyFleetEntry runs one Fleet entry through core.RunScan against app
// (a single-provider stack's own already-launched stateReader, or a
// multi-provider stack's own pool.Get result for e's specific provider)
// and classifies the outcome clean/drifted/unreadable -- factored out of
// the RunE body (UBI-43 session 5) so the single- and multi-provider walks
// share one classification path instead of two copies. e must already be
// known to carry a non-empty Lookup (callers check that themselves, via
// unreadableNoLookup, before ever reaching here, since that check is
// identical in both walks and doesn't need app/providerConfig/
// providerSource at all).
func classifyFleetEntry(ctx context.Context, out io.Writer, st *styler, jsonOut, all bool, ledger *core.Ledger, app core.StateReader, providerConfig json.RawMessage, providerSource string, e core.FleetEntry) (entry statusResourceJSON, drifted, unreadable bool) {
	entry = statusResourceJSON{
		Address:    addressToJSON(e.Address),
		Kind:       string(e.Kind),
		ProposalID: e.ProposalID,
		AcceptedAt: e.AcceptedAt,
	}

	res, err := core.RunScan(ctx, app, ledger, core.ScanRequest{
		Address:        e.Address,
		ProviderConfig: providerConfig,
		CurrentState:   e.Lookup,
		ProviderSource: providerSource,
	})
	if err != nil {
		entry.Status = "unreadable"
		entry.Reason = err.Error()
		if !jsonOut {
			// UBI-68: see the ledger-only line's own comment for the hierarchy.
			fmt.Fprintf(out, "unreadable: %s: %s -- %v\n",
				e.Address, st.forceDim(fmt.Sprintf("%s %s (accepted %s)", e.Kind, st.Hash(e.ProposalID), e.AcceptedAt)), err)
		}
		return entry, false, true
	}

	switch res.Outcome {
	case core.ScanUnchanged:
		entry.Status = "clean"
		if !jsonOut && all {
			// UBI-71: hidden by default -- a clean resource's own detail
			// line is exactly the noise this finding is about (buried
			// signal on a mostly-clean fleet); the caller's own trailing
			// "N clean (--all to show)" line already covers the count
			// when this is suppressed.
			fmt.Fprintf(out, "%s %s: %s\n", st.Green("clean:"), e.Address, st.forceDim(fmt.Sprintf("%s %s (accepted %s)", e.Kind, st.Hash(e.ProposalID), e.AcceptedAt)))
		}
		return entry, false, false
	case core.ScanDrifted:
		entry.Status = "drifted"
		if !jsonOut {
			fmt.Fprintf(out, "%s %s: %s\n", st.Yellow("drifted:"), e.Address, st.forceDim(fmt.Sprintf("%s %s (accepted %s)", e.Kind, st.Hash(e.ProposalID), e.AcceptedAt)))
			// UBI-61: show WHAT drifted, not just that it did -- the same
			// attribute-level diff renderModifies already renders for a
			// drift_adopt proposal, computed here directly from
			// ScanResult.PreviousState/Observed (core.DiffAttributes,
			// already exported) rather than waiting for `ubx scan
			// --propose` to generate a proposal first.
			if before, after, derr := core.DiffAttributes(res.PreviousState, res.Observed); derr == nil {
				// UBI-63 session 4: filtered the SAME way RunScan's own
				// verdict already was -- otherwise a resource with one
				// genuinely-still-drifting attribute renders EVERY
				// attribute that happens to differ, including several
				// that are individually null<->zero-value/materialization
				// noise, coexisting on the same resource (found live: a
				// pre-fix-2 stack's real, unreconciled attachment_count
				// drift dragged tags/force_detach_policies/inline_policy
				// into the rendered diff even though those three were
				// each, on their own, already explained).
				before, after = core.FilterNormalizationNoise(before, after, res.ResourceSchema)
				for _, path := range sortedAttributePaths(before, after) {
					fmt.Fprintf(out, "    %s %s: %s -> %s\n", st.Yellow("~"), path, rawOrAbsent(before[path]), rawOrAbsent(after[path]))
				}
			}
			// UBI-49 residual round 1 finding #3: attribution is
			// scan-propose-time only (a fresh CloudTrail lookback needs a
			// window this live drift check doesn't have any business
			// running itself) -- this surfaces whatever a PRIOR scan
			// --propose/accept cycle already recorded for this exact
			// address, if any, never a live attempt for the drift being
			// shown right now. Omitted entirely when no prior adopt
			// recorded real attribution -- never a placeholder.
			if line, ok := lastRecordedAttribution(st, ledger, e.Address); ok {
				fmt.Fprintf(out, "    %s\n", line)
			}
			// UBI-49 polish: flag-free scan (residual round 1) means the
			// obvious next step is just `ubx scan --propose both` now --
			// stack already resolves from the cascade, and omitting
			// --type/--name walks the whole fleet, this address included.
			fmt.Fprintf(out, "    next: ubx scan --propose both\n")
		}
		return entry, true, false
	default:
		// ScanNew: the ledger has a resolution.inputs entry for this
		// address (that's how Fleet found it) but FoldState can't
		// reconstruct any prior state for it -- a malformed/hand-authored
		// proposal, not something a well-formed scan-generated ledger
		// ever produces (see docs/architecture.md).
		entry.Status = "unreadable"
		entry.Reason = "ledger has no reconstructable prior state for this address"
		if !jsonOut {
			fmt.Fprintf(out, "unreadable: %s: %s -- %s\n",
				e.Address, st.forceDim(fmt.Sprintf("%s %s (accepted %s)", e.Kind, displayHash(e.ProposalID, false), e.AcceptedAt)), entry.Reason)
		}
		return entry, false, true
	}
}
