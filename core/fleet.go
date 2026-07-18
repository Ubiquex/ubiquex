package core

import (
	"encoding/json"
	"fmt"
	"sort"
)

// FleetEntry is one resource's ledger-recorded state, as `ubx status`
// reports it (docs/architecture.md — Fleet status). Kind/ProposalID/
// AcceptedAt describe the *latest* proposal that touched Address, not
// Ledger.Head() (the single global chain head, an unrelated concept) — see
// Ledger.Fleet's own doc comment for why "latest per address" and "global
// head" are different things.
type FleetEntry struct {
	Address    Address
	Kind       ProposalKind
	ProposalID string
	AcceptedAt string          // "" if the latest touching proposal was somehow never accepted (shouldn't happen via Accept/AcceptFromMerge, kept defensive)
	Lookup     json.RawMessage // from the latest resolution.inputs entry recorded for Address; nil if none was ever recorded (see docs/schema.md's lookup amendment)

	// Provider is docs/schema.md's own "Amendment: the provider field
	// returns" (UBI-43), read back the same "most recent wins, falling
	// back to the shipped create's own recorded value" way Lookup already
	// is: a KindChange proposal's own Delta.Modifies/Delta.Destroys entry
	// for Address, if the winning proposal's own latest touch names one
	// directly, else whichever provider originally created Address (a
	// later drift_adopt/drift_revert never carries one at all --
	// core/scan.go's own Modification entries never populate it -- so
	// falling through here is the common case for any address that's ever
	// drifted after its own creation, not an edge case). Nil for a
	// resource this ledger only ever adopted or drift-recorded, or one
	// created before this amendment -- see docs/executor.md's own
	// "Scan/status/fleet" section for how a caller is expected to handle
	// that: attribute it to whichever single provider the invocation's
	// own --source/--provider named, or, for a genuinely multi-provider
	// stack, infer it fresh by type against the declared set (the same
	// mechanism core/resolver already uses for a brand-new resource).
	Provider *ProviderRef
}

// Fleet returns one FleetEntry per distinct resource address the ledger has
// ever recorded and NOT since tombstoned — discovered via
// resolution.inputs[].resource, the same field Ledger.LastObservedHash/
// LastObservationTime/ProposalsForAddress already key off
// (docs/architecture.md — Fleet status), plus (UBI-29, docs/schema.md's
// "Amendment: apply-record lookup key + Fleet discovery") a change
// proposal's own shipped create, which never gets a resolution.inputs
// entry for its own address at all — sorted by canonical address string
// for stable, readable output. If stack is non-empty, only addresses in
// that stack are returned; addresses from every stack the ledger holds
// are returned otherwise (a single ledger directory can legitimately hold
// an interleaved chain spanning multiple stacks — see docs/architecture.md).
//
// The walk is a single pass over Chain(): later proposals overwrite
// earlier ones for the same address, so the result reflects each
// resource's most recently recorded state, not its first. Within one
// proposal's own turn in that pass, its resolution.inputs entries are
// folded before its own Delta.Creates -- harmless either way in practice
// (a resolution.inputs entry for an address can never exist before that
// address's own create, so there's no real ordering hazard), but keeps the
// "most recent wins" reasoning simple to state. A resolution.inputs entry
// whose Resource string doesn't parse as a valid address (docs/schema.md's
// canonical "<stack>.<type>.<name>" form) is skipped rather than guessed
// at — the same defensive posture ParseAddress's own ok return already
// establishes elsewhere (e.g. `ubx why`). A create that hasn't shipped
// (successfully) yet is not discoverable at all -- exactly like a resource
// nothing has ever adopted.
//
// A shipped Delta.Destroys entry (UBI-30) excludes its address from the
// returned entries entirely — Fleet is "what does the ledger think is
// currently out there to watch," and a tombstoned address is exactly as
// absent from that as one never recorded at all, the same posture
// FoldState's own tombstone-fold takes one level down. Tracked in the same
// single pass, chronologically: a later create/modify/live_state touch
// after a destroy (a real, legitimate recreate-under-the-same-address
// lifecycle) un-tombstones it again, keeping this in lockstep with
// FoldState's own re-seed-from-scratch behavior for the identical case.
func (l *Ledger) Fleet(stack string) ([]FleetEntry, error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, fmt.Errorf("fleet: %w", err)
	}

	latest := map[string]*Proposal{}
	latestInput := map[string]ResolutionInput{}
	latestShippedLookup := map[string]json.RawMessage{}
	latestShippedProvider := map[string]*ProviderRef{}
	tombstoned := map[string]bool{}
	for _, p := range chain {
		for _, in := range p.Resolution.Inputs {
			if in.Resource == "" {
				continue
			}
			latest[in.Resource] = p
			latestInput[in.Resource] = in
			if in.Kind != "destroy_target" {
				tombstoned[in.Resource] = false
			}
		}
		if p.Kind != KindChange {
			continue
		}
		for _, raw := range p.Delta.Creates {
			if !isChangeCreateNode(raw) {
				continue
			}
			addr, ok := createNodeAddress(raw)
			if !ok {
				continue
			}
			_, lookup, _, shipped, ferr := l.shippedCreateFold(p.ID, addr)
			if ferr != nil {
				return nil, fmt.Errorf("fleet: %w", ferr)
			}
			if !shipped {
				continue
			}
			addrStr := addr.String()
			latest[addrStr] = p
			latestShippedLookup[addrStr] = lookup
			latestShippedProvider[addrStr] = createNodeProvider(raw)
			tombstoned[addrStr] = false
		}
		for i := range p.Delta.Destroys {
			d := &p.Delta.Destroys[i]
			addrStr := d.Address.String()
			_, shipped, ferr := l.shippedDestroyFold(p.ID, d.Address)
			if ferr != nil {
				return nil, fmt.Errorf("fleet: %w", ferr)
			}
			if shipped {
				tombstoned[addrStr] = true
			}
		}
	}

	entries := make([]FleetEntry, 0, len(latest))
	for addrStr, p := range latest {
		if tombstoned[addrStr] {
			continue
		}
		addr, ok := ParseAddress(addrStr)
		if !ok {
			continue
		}
		if stack != "" && addr.Stack != stack {
			continue
		}
		var acceptedAt string
		if p.Acceptance != nil {
			acceptedAt = p.Acceptance.AcceptedAt
		}
		lookup := latestInput[addrStr].Lookup
		if len(lookup) == 0 {
			lookup = latestShippedLookup[addrStr]
		}
		provider := nodeProviderForAddress(p, addr)
		if provider == nil {
			provider = latestShippedProvider[addrStr]
		}
		entries = append(entries, FleetEntry{
			Address:    addr,
			Kind:       p.Kind,
			ProposalID: p.ID,
			AcceptedAt: acceptedAt,
			Lookup:     lookup,
			Provider:   provider,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Address.String() < entries[j].Address.String()
	})
	return entries, nil
}

// nodeProviderForAddress finds addr's own recorded provider within p's
// Delta.Modifies/Delta.Destroys specifically (never Delta.Creates -- the
// caller's own create-ships branch already handles that case, since a
// create's provider is only real once the create has actually shipped,
// the identical gating Lookup's own latestShippedLookup already applies).
// nil if p doesn't carry an explicit provider for addr at all -- p.Kind
// != KindChange, or a KindChange proposal whose only touch of addr was a
// resolution.inputs entry from an adopt/drift path with no Modification/
// DestroyEntry of its own (shouldn't happen in practice, since those
// entries are always KindAdoption/KindDriftAdopt/KindDriftRevert, but
// checked directly rather than assumed).
func nodeProviderForAddress(p *Proposal, addr Address) *ProviderRef {
	if p.Kind != KindChange {
		return nil
	}
	for i := range p.Delta.Modifies {
		if p.Delta.Modifies[i].Target == addr {
			return p.Delta.Modifies[i].Provider
		}
	}
	for i := range p.Delta.Destroys {
		if p.Delta.Destroys[i].Address == addr {
			return p.Delta.Destroys[i].Provider
		}
	}
	return nil
}

// LastLookup returns the most recently recorded lookup key for addr — the
// same "latest resolution.inputs entry, falling back to a shipped create's
// own recorded lookup" precedence Fleet already establishes across every
// address at once, scoped here to one address (the same per-address-walk
// convention LastObservedHash/LastObservationTime already use rather than
// filtering a full Fleet() call). Added for core/resolver (UBI-30,
// docs/schema.md's "Amendment: destroys"): a delta.destroys entry's own
// resolution.inputs["destroy_target"] requires a Lookup, and a destroy
// target is, by construction, already-ledgered (docs/resolver.md's own
// resolve-time presence validation) — its lookup key was already recorded
// when it was first observed/adopted/shipped, so this never derives a new
// one at need-time (Slice 3's own lesson, reused again). found is false if
// addr was never given a lookup key at all (an ancient apply record
// predating UBI-29, with no derivable "id" either) — distinct from "addr
// was never recorded," which is a FoldState/resolver concern, not this
// function's.
func (l *Ledger) LastLookup(addr Address) (lookup json.RawMessage, found bool, err error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, false, fmt.Errorf("last lookup: %w", err)
	}
	target := addr.String()
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		for _, in := range p.Resolution.Inputs {
			if in.Resource == target && len(in.Lookup) > 0 {
				return in.Lookup, true, nil
			}
		}
		if p.Kind != KindChange {
			continue
		}
		for _, raw := range p.Delta.Creates {
			a, ok := createNodeAddress(raw)
			if !ok || a != addr || !isChangeCreateNode(raw) {
				continue
			}
			_, lookup, _, shipped, ferr := l.shippedCreateFold(p.ID, addr)
			if ferr != nil {
				return nil, false, fmt.Errorf("last lookup: %w", ferr)
			}
			if shipped && len(lookup) > 0 {
				return lookup, true, nil
			}
		}
	}
	return nil, false, nil
}
