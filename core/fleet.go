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
}

// Fleet returns one FleetEntry per distinct resource address the ledger has
// ever recorded — discovered via resolution.inputs[].resource, the same
// field Ledger.LastObservedHash/LastObservationTime/ProposalsForAddress
// already key off (docs/architecture.md — Fleet status) — sorted by
// canonical address string for stable, readable output. If stack is
// non-empty, only addresses in that stack are returned; addresses from
// every stack the ledger holds are returned otherwise (a single ledger
// directory can legitimately hold an interleaved chain spanning multiple
// stacks — see docs/architecture.md).
//
// The walk is a single pass over Chain(): later proposals overwrite
// earlier ones for the same address, so the result reflects each
// resource's most recently recorded state, not its first. A
// resolution.inputs entry whose Resource string doesn't parse as a valid
// address (docs/schema.md's canonical "<stack>.<type>.<name>" form) is
// skipped rather than guessed at — the same defensive posture
// ParseAddress's own ok return already establishes elsewhere (e.g. `ubx
// why`).
func (l *Ledger) Fleet(stack string) ([]FleetEntry, error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, fmt.Errorf("fleet: %w", err)
	}

	latest := map[string]*Proposal{}
	latestInput := map[string]ResolutionInput{}
	for _, p := range chain {
		for _, in := range p.Resolution.Inputs {
			if in.Resource == "" {
				continue
			}
			latest[in.Resource] = p
			latestInput[in.Resource] = in
		}
	}

	entries := make([]FleetEntry, 0, len(latest))
	for addrStr, p := range latest {
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
		entries = append(entries, FleetEntry{
			Address:    addr,
			Kind:       p.Kind,
			ProposalID: p.ID,
			AcceptedAt: acceptedAt,
			Lookup:     latestInput[addrStr].Lookup,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Address.String() < entries[j].Address.String()
	})
	return entries, nil
}
