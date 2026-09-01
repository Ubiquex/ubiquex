package core

import (
	"fmt"
	"sort"
)

// AddressEntry is one resource address this ledger has ever recorded,
// active or (with includeTombstoned) destroyed -- the ledger-level half of
// `ubx addresses` (UBI-48, the fourth of the read-only projection quartet).
// Deliberately narrower than FleetEntry: no Lookup (addresses' own job is
// "what can I reference," not "how would status re-observe this
// resource"), but the same Provider *ProviderRef a caller needs to fetch
// the real schema this address's own referenceable attributes are drawn
// from, at the version this address itself was actually built against --
// never whatever a stack's [providers] table happens to pin today.
type AddressEntry struct {
	Address    Address
	Kind       ProposalKind
	ProposalID string
	AcceptedAt string
	Provider   *ProviderRef

	// Tombstoned is true only when includeTombstoned was requested and
	// this address's own most recent touch was a shipped destroy -- an
	// active address is always Tombstoned: false, TombstonedBy: "".
	Tombstoned   bool
	TombstonedBy string // the shipped destroy's own proposal ID
}

// Addresses returns one AddressEntry per distinct resource address l has
// ever recorded, sorted by canonical address string. With
// includeTombstoned false this is exactly Fleet(stack)'s own address set,
// reshaped; true also returns every address whose most recent touch was a
// shipped destroy, each annotated Tombstoned/TombstonedBy rather than
// silently dropped the way Fleet's own "currently out there to watch"
// posture requires -- `ubx addresses --all`'s own job is a complete
// inventory including history, not a live-fleet view, so this deliberately
// re-walks Chain() with Fleet's own discovery rules (resolution.inputs
// plus a change proposal's own shipped create/destroy, docs/architecture.md
// -- Fleet status) rather than reusing Fleet itself, which has no
// extension point for keeping a tombstoned address around.
func (l *Ledger) Addresses(stack string, includeTombstoned bool) ([]AddressEntry, error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, fmt.Errorf("addresses: %w", err)
	}
	return l.addressesOverChain(chain, stack, includeTombstoned)
}

// AddressesAt is Addresses' own real implementation, generalized to walk
// the chain as of headID rather than always the current Head() -- UBI-227's
// own restore needs the full set of addresses live at an earlier ledger
// head (every resource the target shape actually contains), the exact
// same discovery rules Addresses already uses for the current head, just
// folded over an earlier chain. Addresses itself is a thin wrapper, the
// same relationship Chain/ChainFrom and FoldState/FoldStateAt already
// have: one real walk, never two kept in sync by hand.
func (l *Ledger) AddressesAt(headID string, stack string, includeTombstoned bool) ([]AddressEntry, error) {
	chain, err := l.ChainFrom(headID)
	if err != nil {
		return nil, fmt.Errorf("addresses: %w", err)
	}
	return l.addressesOverChain(chain, stack, includeTombstoned)
}

// addressesOverChain is the one real fold both Addresses and AddressesAt
// share -- see Addresses' own doc comment for the full account of what
// this actually discovers and why.
func (l *Ledger) addressesOverChain(chain []*Proposal, stack string, includeTombstoned bool) ([]AddressEntry, error) {
	latest := map[string]*Proposal{}
	latestShippedProvider := map[string]*ProviderRef{}
	tombstoned := map[string]bool{}
	tombstonedBy := map[string]string{}
	for _, p := range chain {
		for _, in := range p.Resolution.Inputs {
			if in.Resource == "" {
				continue
			}
			latest[in.Resource] = p
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
			_, _, _, shipped, ferr := l.shippedCreateFold(p.ID, addr)
			if ferr != nil {
				return nil, fmt.Errorf("addresses: %w", ferr)
			}
			if !shipped {
				continue
			}
			addrStr := addr.String()
			latest[addrStr] = p
			latestShippedProvider[addrStr] = createNodeProvider(raw)
			tombstoned[addrStr] = false
		}
		for i := range p.Delta.Destroys {
			d := &p.Delta.Destroys[i]
			addrStr := d.Address.String()
			_, shipped, ferr := l.shippedDestroyFold(p.ID, d.Address)
			if ferr != nil {
				return nil, fmt.Errorf("addresses: %w", ferr)
			}
			if shipped {
				tombstoned[addrStr] = true
				tombstonedBy[addrStr] = p.ID
				latest[addrStr] = p
			}
		}
	}

	entries := make([]AddressEntry, 0, len(latest))
	for addrStr, p := range latest {
		isTombstoned := tombstoned[addrStr]
		if isTombstoned && !includeTombstoned {
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
		provider := nodeProviderForAddress(p, addr)
		if provider == nil {
			provider = latestShippedProvider[addrStr]
		}
		entries = append(entries, AddressEntry{
			Address:      addr,
			Kind:         p.Kind,
			ProposalID:   p.ID,
			AcceptedAt:   acceptedAt,
			Provider:     provider,
			Tombstoned:   isTombstoned,
			TombstonedBy: tombstonedBy[addrStr],
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Address.String() < entries[j].Address.String()
	})
	return entries, nil
}
