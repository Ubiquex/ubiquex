package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
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

// StateReader is core's own, minimal view of "something that can fetch a
// provider's schema, configure it, and read one resource's live state" —
// exactly what RunScan/VerifyFreshness need, and nothing about how a
// provider binary is launched or which wire protocol it speaks.
//
// core deliberately does not import package provider (UBI-7 follow-up:
// this used to be provider.Provider directly, which meant core depended on
// infrastructure-client internals it never actually needed to know the
// concrete shape of — it only ever passes provider/resource schema handles
// straight through to Configure/ReadResource without inspecting them, which
// is exactly what `any` captures here). provider.Client can satisfy this
// interface via a small adapter at the call site (see cli's
// providerStateReader) without provider needing to import core either.
type StateReader interface {
	// Schema returns an opaque handle for the provider's own config schema,
	// and one opaque handle per resource type it supports, keyed by type
	// name. Callers pass these straight into Configure/ReadResource; core
	// never looks inside them.
	Schema(ctx context.Context) (providerSchema any, resourceSchemas map[string]any, err error)

	// Configure performs the provider's one-time initialization.
	Configure(ctx context.Context, providerSchema any, config json.RawMessage) error

	// ReadResource fetches the live state of one resource instance.
	ReadResource(ctx context.Context, resourceSchema any, typeName string, currentState json.RawMessage) (json.RawMessage, error)
}

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
	PreviousHash string          // "" if Outcome == ScanNew
	Lookup       json.RawMessage // the lookup key used to read Address — persisted into the generated proposal, see GenerateProposal
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
func RunScan(ctx context.Context, prov StateReader, l *Ledger, req ScanRequest) (*ScanResult, error) {
	observed, hash, err := readAndFingerprint(ctx, prov, req.Address, req.ProviderConfig, req.CurrentState)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", req.Address, err)
	}

	prevHash, found, err := l.LastObservedHash(req.Address)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", req.Address, err)
	}

	res := &ScanResult{Address: req.Address, Observed: observed, ObservedHash: hash, PreviousHash: prevHash, Lookup: req.CurrentState}
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
func readAndFingerprint(ctx context.Context, prov StateReader, addr Address, providerConfig, currentState json.RawMessage) (observed json.RawMessage, hash string, err error) {
	providerSchema, resourceSchemas, err := prov.Schema(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("fetch schema: %w", err)
	}
	resourceSchema, ok := resourceSchemas[addr.Type]
	if !ok {
		return nil, "", fmt.Errorf("%w: %q", ErrUnknownResourceType, addr.Type)
	}
	if err := prov.Configure(ctx, providerSchema, providerConfig); err != nil {
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
				{Kind: "live_state", Resource: res.Address.String(), ObservedHash: res.ObservedHash, Lookup: res.Lookup},
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
// "reality changes between propose and accept"). The lookup key is read
// back from p's own resolution.inputs entry (see ResolutionInput.Lookup) —
// callers don't need to already know, or re-supply, what was used to
// generate the proposal in the first place.
func VerifyFreshness(ctx context.Context, prov StateReader, addr Address, providerConfig json.RawMessage, p *Proposal) error {
	target := addr.String()
	var recorded string
	var lookup json.RawMessage
	found := false
	for _, in := range p.Resolution.Inputs {
		if in.Resource == target {
			recorded = in.ObservedHash
			lookup = in.Lookup
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("verify freshness: no resolution.inputs entry for %s", addr)
	}
	if len(lookup) == 0 {
		return fmt.Errorf("verify freshness: resolution.inputs entry for %s has no recorded lookup key", addr)
	}

	_, fresh, err := readAndFingerprint(ctx, prov, addr, providerConfig, lookup)
	if err != nil {
		return fmt.Errorf("verify freshness: %w", err)
	}
	if fresh != recorded {
		return fmt.Errorf("%w: %s recorded %s, now %s", ErrStaleObservation, addr, recorded, fresh)
	}
	return nil
}
