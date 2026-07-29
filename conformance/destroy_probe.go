package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/core/executor"
	"github.com/ubiquex/ubiquex-cli/provider"
)

// DestroyProbeConfig describes one destroy-honesty probe run — lie-class
// 3's own live-tier-only shape (docs/conformance-harness.md), the
// destroy-side counterpart to AdoptMutateScanDiffConfig. ProviderPath
// may be a real provider binary or, for hermetic verification of this
// probe ITSELF (never of a real cloud resource), the fakeprovider
// fixture launched with FAKEPROVIDER_APPLY_MODE=lying-destroy — see
// destroy_probe_test.go.
type DestroyProbeConfig struct {
	ProviderPath   string
	Source         string
	Version        string
	Stack          string
	Address        core.Address
	Lookup         json.RawMessage // passed to ReadResource for the initial adopt scan, unchanged across every step
	ProviderConfig json.RawMessage
	Timeout        time.Duration
	ProviderEnv    []string
}

// ProbeDestroyHonesty is lie-class 3's own live-tier probe
// (docs/conformance-harness.md — "Destroy honesty via read-back
// absence"): adopts cfg.Address via a real scan (the identical
// core.RunScan → core.GenerateProposal → core.Accept path
// RunAdoptMutateScanDiff's own first step already uses — ship doctrine,
// unchanged), then builds and accepts a real Delta.Destroys proposal and
// ships it through core/executor.Ship's own real
// shipDestroyNode/reconcileDestroyLoop path — never a raw
// ApplyResourceChange shortcut (ship doctrine's own destroy-specific
// requirement: bypassing reconcileDestroyLoop's universal post-destroy
// read-back would make this probe structurally incapable of ever
// finding another UBI-44-shaped bug, the opposite of what it exists to
// do).
//
// Returns a Finding only when the destroy actually LIED — the resulting
// ApplyRecord's own terminal Reconciliation outcome is the UBI-44-
// specific "provider_reported_success_but_present" string, the one
// value that distinguishes an active lie from an ordinary, honest
// ambiguous failure (core/executor/ship.go's own reconcileDestroyLoop).
// Returns (nil, nil) for a genuinely honest destroy (Outcome "destroyed"
// or "already_absent") AND for an ordinary non-lying failure — this
// probe's only job is catching LIES, not general destroy reliability,
// which docs/destroys-adversarial.md's own 14-row program already
// covers exhaustively; conflating "ordinary failed" with "lied" here
// would produce a false FindingDestroyLie for a resource that simply
// failed to destroy honestly (ship.go's own row 5 case), a real,
// deliberately-avoided miscategorization.
func ProbeDestroyHonesty(ctx context.Context, l *core.Ledger, cfg DestroyProbeConfig) (*Finding, error) {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	providerConfig := cfg.ProviderConfig
	if providerConfig == nil {
		providerConfig = json.RawMessage(`{}`)
	}

	salt, err := l.Salt()
	if err != nil {
		return nil, fmt.Errorf("destroy probe: ledger salt: %w", err)
	}

	// Step 1: adopt, via the real scan path -- ship doctrine for reads,
	// unchanged from RunAdoptMutateScanDiff's own identical first step.
	if err := probeAdopt(ctx, l, salt, timeout, cfg); err != nil {
		return nil, fmt.Errorf("destroy probe: adopt: %w", err)
	}

	// Step 2: fold the just-adopted state -- the same source
	// DestroyEntry.State always uses in a real destroy proposal.
	state, found, err := l.FoldState(cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("destroy probe: fold state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("destroy probe: %s not found in FoldState immediately after adopt", cfg.Address)
	}

	// Step 3: build and accept a real Delta.Destroys proposal.
	accepted, err := acceptDestroyProposal(l, cfg.Address, state, cfg.Lookup)
	if err != nil {
		return nil, fmt.Errorf("destroy probe: accept destroy: %w", err)
	}

	// Step 4: SHIP — core/executor.Ship's own real path, never a
	// shortcut. A fresh provider launch, matching every other
	// per-step-fresh-subprocess convention this harness already follows
	// (RunAdoptMutateScanDiff's own scan closure).
	shipCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	shipClient, err := provider.Launch(shipCtx, cfg.ProviderPath, provider.WithEnv(cfg.ProviderEnv...))
	if err != nil {
		return nil, fmt.Errorf("destroy probe: launch provider (ship): %w", err)
	}
	defer shipClient.Close()
	app := stateReaderAdapter{p: shipClient.Provider, salt: salt, source: cfg.Source}

	rec, err := executor.Ship(shipCtx, l, executor.SingleApplierPool(app, providerConfig), cfg.Source, accepted)
	if err != nil {
		return nil, fmt.Errorf("destroy probe: ship: %w", err)
	}

	return classifyDestroyOutcome(cfg.Source, cfg.Version, cfg.Address.Type, rec), nil
}

// probeAdopt runs the real adopt-via-scan sequence
// (core.RunScan/GenerateProposal/Accept), identical in mechanism to
// RunAdoptMutateScanDiff's own first step — factored out here rather
// than calling RunAdoptMutateScanDiff itself, since that function is
// *testing.T-coupled (asserts via t.Fatalf) and ProbeDestroyHonesty
// needs to return real errors to its own caller instead, for both
// hermetic and future live-tier bulk-run use.
func probeAdopt(ctx context.Context, l *core.Ledger, salt []byte, timeout time.Duration, cfg DestroyProbeConfig) error {
	scanCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := provider.Launch(scanCtx, cfg.ProviderPath, provider.WithEnv(cfg.ProviderEnv...))
	if err != nil {
		return fmt.Errorf("launch provider (adopt): %w", err)
	}
	defer client.Close()

	providerConfig := cfg.ProviderConfig
	if providerConfig == nil {
		providerConfig = json.RawMessage(`{}`)
	}
	res, err := core.RunScan(scanCtx, stateReaderAdapter{p: client.Provider, salt: salt, source: cfg.Source}, l, core.ScanRequest{
		Address:        cfg.Address,
		ProviderConfig: providerConfig,
		CurrentState:   cfg.Lookup,
	})
	if err != nil {
		return fmt.Errorf("adopt scan: %w", err)
	}
	if res.Outcome != core.ScanNew {
		return fmt.Errorf("adopt scan: Outcome = %v, want ScanNew (is %s already tracked from a prior run?)", res.Outcome, cfg.Address)
	}
	proposal, err := core.GenerateProposal(l, cfg.Stack, res)
	if err != nil {
		return fmt.Errorf("generate adopt proposal: %w", err)
	}
	if _, err := core.Accept(l, proposal); err != nil {
		return fmt.Errorf("accept adopt: %w", err)
	}
	return nil
}

// acceptDestroyProposal hand-builds and accepts a real kind:"change"
// proposal carrying one Delta.Destroys entry for addr — the same shape
// every existing hermetic destroy test in this codebase hand-builds
// (core/destroy_tombstone_test.go's own shipChangeDestroyForTest,
// core/executor/ship_test.go's own acceptDestroy), since core has no
// dedicated destroy-proposal generator (core.GenerateProposal/
// GenerateRevertProposal are both adopt/drift-only).
func acceptDestroyProposal(l *core.Ledger, addr core.Address, state, lookup json.RawMessage) (*core.Proposal, error) {
	hash, err := core.ObservedHash(state)
	if err != nil {
		return nil, fmt.Errorf("observed hash: %w", err)
	}
	head, err := l.Head()
	if err != nil {
		return nil, fmt.Errorf("head: %w", err)
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "destroy-honesty probe: " + addr.String()},
		Delta:         core.Delta{Destroys: []core.DestroyEntry{{Address: addr, State: state}}},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []core.ResolutionInput{
				{Kind: "destroy_target", Resource: addr.String(), ObservedHash: hash, Lookup: lookup},
			},
		},
		CostDelta:   core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: core.BlastRadius{Destroys: 1},
		Status:      core.StatusDraft,
	}
	return core.Accept(l, p)
}

// destroyLiedOutcome is the exact, literal ReconciliationAttempt.Outcome
// string core/executor/ship.go's own reconcileDestroyLoop assigns when a
// provider reports a clean ApplyResourceChange success but a real
// post-destroy read-back proves the resource is still present (UBI-44).
// The one string this probe exists to detect.
const destroyLiedOutcome = "provider_reported_success_but_present"

// classifyDestroyOutcome inspects rec's own single ResourceApply entry
// (a destroy-honesty probe always ships exactly one Delta.Destroys
// entry, so rec.Resources always has exactly one element on a
// successful Ship call) and returns a FindingDestroyLie only when its
// own terminal Reconciliation entry is destroyLiedOutcome specifically —
// never for an ordinary "failed" (ambiguous RPC failure, honestly
// reported) or a genuinely honest "destroyed"/"already_absent".
func classifyDestroyOutcome(source, version, typeName string, rec *core.ApplyRecord) *Finding {
	if rec == nil || len(rec.Resources) == 0 {
		return nil
	}
	ra := rec.Resources[0]
	if len(ra.Reconciliation) == 0 {
		return nil
	}
	last := ra.Reconciliation[len(ra.Reconciliation)-1]
	if last.Outcome != destroyLiedOutcome {
		return nil
	}
	return &Finding{
		Source: source, Version: version, Type: typeName, Verb: "destroy",
		Class: FindingDestroyLie, Tier: TierLive, Confidence: Confirmed,
		Detail: "ApplyResourceChange reported a clean destroy success, but a real post-destroy read-back (core/executor's own universal reconcileDestroyLoop) proved the resource was still present after the full retry budget -- the same shape UBI-44 found live against google_pubsub_topic",
	}
}
