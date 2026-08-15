package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/provider"
)

// runScanFleet is UBI-49 residual round 1's "fleet-scoped --propose": bare
// `ubx scan --propose <mode>` with a stack resolved (by flag or config)
// but no --type/--name walks every address the stack's own ledger already
// tracks (core.Ledger.Fleet -- the identical walk `ubx status` already
// performs, docs/architecture.md — Fleet status) instead of demanding a
// human name each tracked resource individually. Only ever reads
// addresses Fleet already knows about, so ScanNew never legitimately
// occurs here -- --discover (a different verb entirely) is still the only
// way to find a resource nothing has recorded yet.
//
// --json/--out/--out-dir/--surface-as are all single-resource-only
// concerns (validated by the caller before ever reaching here) -- a fleet
// walk's own output is N independent cards, not one document, and
// --surface-as's GitHub receipt is built around exactly one drift_adopt.
// surfaceFunc is runScanFleet's own optional per-drift surfacing hook
// (UBI-165). Nil means "just save the proposal", the behavior every
// caller had before drift-watch needed a fleet-wide walk that also
// opens one issue or pull request per drifted resource.
type surfaceFunc func(p *core.Proposal, addr core.Address) error

func runScanFleet(ctx context.Context, out io.Writer, st *styler, ledger *core.Ledger, ledgerDir, stack, propose string, noAttribution bool, cfg *Config, providerPath, source, providerVersion, providerConfig string, surface surfaceFunc) error {
	fleet, err := ledger.Fleet(stack)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: %w", stack, err)}
	}
	if len(fleet) == 0 {
		fmt.Fprintf(out, "0 resource(s) tracked for stack %s -- nothing to scan (run ubx plan/ubx terminate to create or adopt one first)\n", stack)
		return nil
	}

	salt, err := ledger.Salt()
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: %w", stack, err)}
	}

	var driftedCount, unreadableCount, proposalCount int
	unreadable := func(addr core.Address, reason string) {
		unreadableCount++
		fmt.Fprintf(out, "unreadable: %s: %s\n", addr, reason)
	}
	scanOne := func(app core.StateReader, pConfig json.RawMessage, pSource string, e core.FleetEntry) error {
		res, serr := core.RunScan(ctx, app, ledger, core.ScanRequest{
			Address:        e.Address,
			ProviderConfig: pConfig,
			CurrentState:   e.Lookup,
			ProviderSource: pSource,
		})
		if serr != nil {
			unreadable(e.Address, serr.Error())
			return nil
		}
		if res.Outcome != core.ScanDrifted {
			// ScanUnchanged: clean, nothing to do. ScanNew: shouldn't
			// happen (Fleet only ever returns addresses it already
			// tracks), but never treated as a hard stop if it somehow
			// does -- same defensive posture status.go's own fleet walk
			// takes for its analogous "shouldn't happen" case.
			return nil
		}
		driftedCount++
		hashes, adopt, gerr := generateProposeSaveAndRender(ctx, out, st, ledger, ledgerDir, stack, e.Address, res, propose, noAttribution, cfg.K8sAudit, pConfig, pSource)
		if gerr != nil {
			return gerr
		}
		proposalCount += len(hashes)
		// One issue/pull request per drifted resource, never one for the
		// whole sweep: a receipt is built around a single drift_adopt
		// proposal, and an operator triaging drift wants them separable.
		// A surfacing failure on one resource is reported and does not
		// abort the rest of the walk, the same "one bad stack shouldn't
		// block a fleet-wide sweep" reasoning this function already
		// applies to an unreadable resource.
		if surface != nil && adopt != nil {
			if serr := surface(adopt, e.Address); serr != nil {
				fmt.Fprintf(out, "  surfacing %s failed: %v\n", e.Address, serr)
			}
		}
		return nil
	}

	if len(cfg.Providers) > 0 {
		pool, perr := newProviderPool(salt, cfg.Providers, cfg.ProviderConfigs)
		if perr != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: %w", stack, perr)}
		}
		defer pool.Close()

		var declared []resolver.DeclaredProvider
		for _, e := range fleet {
			if len(e.Lookup) == 0 {
				unreadable(e.Address, "no lookup key recorded for this resource (authored before the resolution.inputs lookup amendment, or never had one)")
				continue
			}
			provSource, provVersion := "", ""
			switch {
			case e.Provider != nil:
				provSource, provVersion = e.Provider.Source, e.Provider.Version
			default:
				if declared == nil {
					declared, err = declaredProvidersForInference(ctx, pool, cfg.Providers)
					if err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: %w", stack, err)}
					}
				}
				winner, ierr := resolver.InferProvider(declared, e.Address.Type, nil)
				if ierr != nil {
					unreadable(e.Address, fmt.Sprintf("provider unavailable: %v", ierr))
					continue
				}
				provSource, provVersion = winner.Source, winner.Version
			}
			app, pConfig, gerr := pool.Get(ctx, provSource, provVersion)
			if gerr != nil {
				unreadable(e.Address, fmt.Sprintf("provider unavailable: %v", gerr))
				continue
			}
			if err := scanOne(app, pConfig, provSource, e); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: %w", stack, err)}
			}
		}
	} else {
		path, _, perr := resolveProviderBinary(ctx, providerPath, source, providerVersion)
		if perr != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: %w", stack, perr)}
		}
		client, cerr := provider.Launch(ctx, path)
		if cerr != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: %w", stack, cerr)}
		}
		defer client.Close()
		stateReader := newStateReader(client.Provider, salt, source)

		for _, e := range fleet {
			if len(e.Lookup) == 0 {
				unreadable(e.Address, "no lookup key recorded for this resource (authored before the resolution.inputs lookup amendment, or never had one)")
				continue
			}
			if err := scanOne(stateReader, json.RawMessage(providerConfig), source, e); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: %w", stack, err)}
			}
		}
	}

	// UBI-76: same closing-summary treatment as status --drift -- blank
	// line before, bold throughout, whole line green (affirmative-success,
	// matching ubx init) only for the fully-clean case, yellow/red
	// per-count otherwise.
	fmt.Fprintln(out)
	switch {
	case unreadableCount > 0:
		fmt.Fprintln(out, st.forceBold(fmt.Sprintf("%d resource(s) scanned, %s, %d proposal(s) saved, %s",
			len(fleet), st.Yellow(fmt.Sprintf("%d drifted", driftedCount)), proposalCount, st.Red(fmt.Sprintf("%d unreadable", unreadableCount)))))
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: %d resource(s) unreadable (see above)", stack, unreadableCount)}
	case driftedCount > 0:
		fmt.Fprintln(out, st.forceBold(fmt.Sprintf("%d resource(s) scanned, %s, %d proposal(s) saved",
			len(fleet), st.Yellow(fmt.Sprintf("%d drifted", driftedCount)), proposalCount)))
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("scan --stack %s: %d resource(s) drifted, %q proposal(s) generated (see above)", stack, driftedCount, propose)}
	default:
		fmt.Fprintln(out, st.GreenBold(fmt.Sprintf("no drift: all %d resource(s) in stack %s match the ledger", len(fleet), stack)))
		return nil
	}
}

// generateProposeSaveAndRender is the propose-mode proposal generation,
// drift_adopt attribution, plan-store save, and card rendering shared by
// both single-resource and fleet-scoped scan (UBI-49 residual round 1) --
// everything downstream of "a real ScanResult was found" that doesn't
// depend on --json/--out/--surface-as, which stay single-resource-only
// concerns their own caller (newScanCmd's RunE) still handles alone --
// jsonOut short-circuits before ever rendering a card at all, and --out
// only ever makes sense for a single generated proposal.
func generateProposeSaveAndRender(ctx context.Context, out io.Writer, st *styler, ledger *core.Ledger, ledgerDir, stack string, addr core.Address, res *core.ScanResult, propose string, noAttribution bool, k8sAudit K8sAuditConfig, providerConfig json.RawMessage, source string) ([]string, *core.Proposal, error) {
	var proposals []*core.Proposal
	switch {
	case res.Outcome == core.ScanNew:
		// --propose has no effect on a never-seen-before resource --
		// there's nothing recorded yet to revert to, so adoption is the
		// only valid resolution regardless of the flag's value.
		p, err := core.GenerateProposal(ledger, stack, res)
		if err != nil {
			return nil, nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		proposals = []*core.Proposal{p}
	case propose == "adopt":
		p, err := core.GenerateProposal(ledger, stack, res)
		if err != nil {
			return nil, nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		proposals = []*core.Proposal{p}
	case propose == "revert":
		p, err := core.GenerateRevertProposal(ledger, stack, res)
		if err != nil {
			return nil, nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		proposals = []*core.Proposal{p}
	default: // both
		adopt, err := core.GenerateProposal(ledger, stack, res)
		if err != nil {
			return nil, nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		revert, err := core.GenerateRevertProposal(ledger, stack, res)
		if err != nil {
			return nil, nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		proposals = []*core.Proposal{adopt, revert}
	}

	// CloudTrail attribution is drift_adopt-specific (docs/schema.md:
	// attribution sources attach to drift_adopt proposals only) --
	// applied to whichever generated proposal is the drift_adopt, if any.
	var adoptProposal *core.Proposal
	for _, p := range proposals {
		if p.Kind == core.KindDriftAdopt {
			adoptProposal = p
		}
	}
	if adoptProposal != nil && !noAttribution {
		attributeDrift(ctx, ledger, addr, res, adoptProposal, providerConfig, source, k8sAudit)
	}

	kindLabel := "new"
	if res.Outcome == core.ScanDrifted {
		kindLabel = "drifted"
	}
	header := st.Yellow("Drift found")
	if kindLabel == "new" {
		header = st.Green("New resource found")
	}
	fmt.Fprintf(out, "%s  %s\n\n", header, addr)

	hashes := make([]string, 0, len(proposals))
	needsConfirm := false
	for _, p := range proposals {
		b, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return nil, nil, fmt.Errorf("scan %s: marshal proposal: %w", addr, err)
		}
		hash, err := core.Hash(p)
		if err != nil {
			return nil, nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		if _, err := writePlanFile(ledgerDir, hash, b); err != nil {
			return nil, nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		hashes = append(hashes, hash)
		if p.BlastRadius.Destroys > 0 {
			needsConfirm = true
		}
		renderScanCard(out, st, p, hash)
	}
	fmt.Fprintf(out, "\n  saved to plan store            next: %s\n\n", nextShipHint(hashes, needsConfirm))
	return hashes, adoptProposal, nil
}
