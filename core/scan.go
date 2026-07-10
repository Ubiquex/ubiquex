package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ubiquex/ubiquex-cli/provider"
)

var (
	// ErrResourceUnreadable means ReadResource succeeded at the RPC level
	// but returned no state (nil or JSON null) — the provider couldn't find
	// the resource, as opposed to a transport/protocol error.
	ErrResourceUnreadable = errors.New("resource unreadable: provider returned no state")

	// ErrUnknownResourceType means the requested resource type isn't in the
	// provider's schema at all.
	ErrUnknownResourceType = errors.New("unknown resource type")

	// ErrStaleObservation means a proposal's recorded observed_hash for a
	// live-state resource no longer matches reality — something changed
	// between when the proposal was generated (scan) and now (accept).
	// Accepting it would record a "before"/"after" that's no longer true.
	ErrStaleObservation = errors.New("stale observation: reality changed since this proposal was generated")
)

// ScanOutcome classifies what a scan found for one resource address.
type ScanOutcome int

const (
	// ScanUnchanged means the resource's live state matches the ledger's
	// last-recorded observed_hash — nothing to propose.
	ScanUnchanged ScanOutcome = iota
	// ScanNew means the ledger has never recorded this address.
	ScanNew
	// ScanDrifted means the ledger has recorded this address before, but
	// the live observed_hash no longer matches.
	ScanDrifted
)

// ScanResult is what one Scan/RunScan call found.
type ScanResult struct {
	Address      Address
	Outcome      ScanOutcome
	Observed     json.RawMessage
	ObservedHash string
	PreviousHash string // "" if Outcome == ScanNew
}

// ScanRequest describes one resource to scan.
type ScanRequest struct {
	Address        Address
	ProviderConfig json.RawMessage // passed to Provider.Configure
	CurrentState   json.RawMessage // passed to Provider.ReadResource — the lookup key(s) (e.g. {"id":"...","bucket":"..."})
}

// RunScan performs the full scan sequence against an already-launched,
// handshaken provider: fetch its schema, configure it, read the target
// resource, and classify the result against the ledger's last-recorded
// observed state for that address. Each step is wrapped with which step
// failed ("provider errors mid-scan" should be diagnosable, not just a bare
// error).
func RunScan(ctx context.Context, prov provider.Provider, l *Ledger, req ScanRequest) (*ScanResult, error) {
	observed, hash, err := readAndFingerprint(ctx, prov, req.Address, req.ProviderConfig, req.CurrentState)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", req.Address, err)
	}

	prevHash, found, err := l.LastObservedHash(req.Address)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", req.Address, err)
	}

	res := &ScanResult{Address: req.Address, Observed: observed, ObservedHash: hash, PreviousHash: prevHash}
	switch {
	case !found:
		res.Outcome = ScanNew
	case prevHash != hash:
		res.Outcome = ScanDrifted
	default:
		res.Outcome = ScanUnchanged
	}
	return res, nil
}

// readAndFingerprint fetches the provider's schema, configures it, reads
// addr's live state, and fingerprints it. Shared by RunScan and
// VerifyFreshness so both apply the exact same read pipeline.
func readAndFingerprint(ctx context.Context, prov provider.Provider, addr Address, providerConfig, currentState json.RawMessage) (observed json.RawMessage, hash string, err error) {
	schemas, err := prov.Schema(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("fetch schema: %w", err)
	}
	resourceSchema, ok := schemas.Resources[addr.Type]
	if !ok {
		return nil, "", fmt.Errorf("%w: %q", ErrUnknownResourceType, addr.Type)
	}
	if err := prov.Configure(ctx, schemas.Provider, providerConfig); err != nil {
		return nil, "", fmt.Errorf("configure provider: %w", err)
	}
	observed, err = prov.ReadResource(ctx, resourceSchema, addr.Type, currentState)
	if err != nil {
		return nil, "", fmt.Errorf("read resource: %w", err)
	}
	if len(observed) == 0 || string(observed) == "null" {
		return nil, "", ErrResourceUnreadable
	}
	hash, err = ObservedHash(observed)
	if err != nil {
		return nil, "", err
	}
	return observed, hash, nil
}

// GenerateProposal builds the adoption/drift_adopt proposal for a scan
// result that found something to record. Callers must check
// res.Outcome != ScanUnchanged first — there is nothing to propose
// otherwise, and GenerateProposal returns an error rather than guessing.
func GenerateProposal(l *Ledger, stack string, res *ScanResult) (*Proposal, error) {
	if res.Outcome == ScanUnchanged {
		return nil, errors.New("generate proposal: scan result is unchanged, nothing to propose")
	}

	head, err := l.Head()
	if err != nil {
		return nil, fmt.Errorf("generate proposal: %w", err)
	}

	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         stack,
		Parent:        head,
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{Kind: "live_state", Resource: res.Address.String(), ObservedHash: res.ObservedHash},
			},
		},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{},
		Status:      StatusDraft,
	}

	switch res.Outcome {
	case ScanNew:
		p.Kind = KindAdoption
		p.Intent = Intent{Summary: fmt.Sprintf("adopt existing %s into the ledger (discovered by scan)", res.Address)}
		node, err := json.Marshal(map[string]interface{}{
			"stack": res.Address.Stack,
			"type":  res.Address.Type,
			"name":  res.Address.Name,
			"state": json.RawMessage(res.Observed),
		})
		if err != nil {
			return nil, fmt.Errorf("generate proposal: %w", err)
		}
		p.Delta = Delta{Creates: []json.RawMessage{node}}

	case ScanDrifted:
		p.Kind = KindDriftAdopt
		p.Intent = Intent{Summary: fmt.Sprintf("record drift on %s observed outside the ledger", res.Address)}
		prevState, found, err := l.FoldState(res.Address)
		if err != nil {
			return nil, fmt.Errorf("generate proposal: %w", err)
		}
		if !found {
			return nil, fmt.Errorf("generate proposal: %w: %s has a prior observed_hash but no reconstructable state",
				ErrCorruptLedgerEntry, res.Address)
		}
		before, after, err := diffAttributes(prevState, res.Observed)
		if err != nil {
			return nil, fmt.Errorf("generate proposal: %w", err)
		}
		p.Delta = Delta{Modifies: []Modification{{Target: res.Address, Before: before, After: after}}}
	}

	return p, nil
}

// VerifyFreshness re-reads addr's live state and confirms it still matches
// p's recorded observed_hash for that address, blocking acceptance of a
// proposal whose claimed observation is now stale (docs/plan.md Slice 3 —
// "reality changes between propose and accept"). currentState is the same
// lookup JSON originally used to generate the proposal — Slice 3 doesn't
// persist it in the proposal itself, so callers (ubx accept
// --provider/--lookup) must supply it again.
func VerifyFreshness(ctx context.Context, prov provider.Provider, addr Address, providerConfig, currentState json.RawMessage, p *Proposal) error {
	target := addr.String()
	var recorded string
	found := false
	for _, in := range p.Resolution.Inputs {
		if in.Resource == target {
			recorded = in.ObservedHash
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("verify freshness: no resolution.inputs entry for %s", addr)
	}

	_, fresh, err := readAndFingerprint(ctx, prov, addr, providerConfig, currentState)
	if err != nil {
		return fmt.Errorf("verify freshness: %w", err)
	}
	if fresh != recorded {
		return fmt.Errorf("%w: %s recorded %s, now %s", ErrStaleObservation, addr, recorded, fresh)
	}
	return nil
}
