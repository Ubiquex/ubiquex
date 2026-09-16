package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Chain returns every proposal in l, oldest (genesis) first, by walking
// Parent links back from Head(). A thin wrapper over ChainFrom -- see its
// own doc comment for why that split exists and why it's the one real
// walk, not two.
func (l *Ledger) Chain() ([]*Proposal, error) {
	head, err := l.Head()
	if err != nil {
		return nil, fmt.Errorf("chain: %w", err)
	}
	return l.ChainFrom(head)
}

// ChainFrom returns every proposal from genesis up to and including
// headID, oldest first, by walking Parent links back from headID. This is
// Chain()'s own real implementation, generalized to start anywhere in the
// ledger rather than always the current Head() -- UBI-227's own restore
// needs to reconstruct the ledger's truth as of an arbitrary historical
// head, not just the current one, and every derived-state fold (FoldState,
// Addresses) that used to hard-code "start from Head()" now threads its
// own starting head through to this one walk instead of reimplementing
// it. Deliberately one implementation: two independent walks of the same
// Parent chain is exactly the shape that silently diverged before in this
// codebase (UBI-197, UBI-233) -- Chain() itself is now nothing more than
// "resolve Head(), then call this."
//
// Ledgers are expected to be small at this stage (foundational-slice
// scale); this is a straightforward linear walk, not an indexed lookup.
func (l *Ledger) ChainFrom(headID string) ([]*Proposal, error) {
	var reversed []*Proposal
	for id := headID; id != ""; {
		p, err := l.Read(id)
		if err != nil {
			if len(reversed) == 0 && errors.Is(err, ErrProposalNotFound) {
				return nil, fmt.Errorf("chain: %w: %s", ErrBrokenLedgerHead, l.brokenHeadDetail(id, true))
			}
			return nil, fmt.Errorf("chain: %w", err)
		}
		reversed = append(reversed, p)
		id = p.Parent
	}
	chain := make([]*Proposal, len(reversed))
	for i, p := range reversed {
		chain[len(reversed)-1-i] = p
	}
	return chain, nil
}

// createNodeAddress decodes a Delta.Creates entry's stack/type/name fields
// -- shared by every UBI-29 discovery fold below and FoldState, since both
// adoption's {stack,type,name,state} shape and a change proposal's
// {stack,type,name,config,depends_on} shape (docs/schema.md's amendment)
// carry these three fields identically. ok is false if raw isn't shaped
// like a resource node at all, or any of the three fields is empty.
func createNodeAddress(raw json.RawMessage) (addr Address, ok bool) {
	var node map[string]interface{}
	if err := json.Unmarshal(raw, &node); err != nil {
		return Address{}, false
	}
	s, _ := node["stack"].(string)
	ty, _ := node["type"].(string)
	nm, _ := node["name"].(string)
	if s == "" || ty == "" || nm == "" {
		return Address{}, false
	}
	return Address{Stack: s, Type: ty, Name: nm}, true
}

// createNodeProvider decodes a change-proposal create node's own
// "provider" key (docs/schema.md's "Amendment: the provider field
// returns", UBI-43) -- nil, not an error, for a create node predating
// that amendment (the field is additive) or a raw value that doesn't
// decode into the expected {source, version} shape at all. Mirrors
// createNodeAddress's own permissive, never-erroring decode style: a
// node this can't make sense of contributes no provider information,
// same as one that was never resolver-produced at all.
func createNodeProvider(raw json.RawMessage) *ProviderRef {
	var node struct {
		Provider *ProviderRef `json:"provider"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil
	}
	return node.Provider
}

// isChangeCreateNode reports whether raw is a change-proposal create node
// (docs/schema.md's amendment: {stack,type,name,config,depends_on}) rather
// than adoption's own {stack,type,name,state} shape -- recognized by the
// "config" key's presence, never confused with "state".
func isChangeCreateNode(raw json.RawMessage) bool {
	var node map[string]interface{}
	if err := json.Unmarshal(raw, &node); err != nil {
		return false
	}
	_, ok := node["config"]
	return ok
}

// LastObservedHash returns the most recently recorded observed_hash for
// addr — the newest resolution.inputs entry, across the whole ledger
// chain, whose resource matches addr's canonical address string; falling
// back (UBI-29) to a change proposal's own shipped-create apply record
// (docs/schema.md's "Amendment: apply-record lookup key + Fleet
// discovery") when no resolution.inputs entry exists at all -- a create
// was never observed via a resolution input, but its real applied result
// is exactly as good a baseline hash. found is false if the ledger has
// never recorded addr at all (never scanned/adopted/shipped).
func (l *Ledger) LastObservedHash(addr Address) (hash string, found bool, err error) {
	chain, err := l.Chain()
	if err != nil {
		return "", false, fmt.Errorf("last observed hash: %w", err)
	}
	target := addr.String()
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		for _, in := range p.Resolution.Inputs {
			if in.Resource == target {
				return in.ObservedHash, true, nil
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
			result, _, _, shipped, ferr := l.shippedCreateFold(p.ID, addr)
			if ferr != nil {
				return "", false, fmt.Errorf("last observed hash: %w", ferr)
			}
			if !shipped {
				continue
			}
			h, herr := ObservedHash(result)
			if herr != nil {
				return "", false, fmt.Errorf("last observed hash: %s: %w", addr, herr)
			}
			return h, true, nil
		}
	}
	return "", false, nil
}

// LastObservationTime returns the resolved_at timestamp of the proposal
// that most recently recorded addr's observed state — the same proposal
// LastObservedHash reads its hash from. Used by AttributeDrift (UBI-10) to
// bound the CloudTrail correlation window's start: "since" is when ubx last
// looked at this resource, not an arbitrary lookback. Falls back (UBI-29)
// to a shipped create's own apply-record "applied" transition timestamp --
// a more precise "since" than resolved_at would be anyway, since it's the
// exact moment the resource actually came to exist, not when the proposal
// creating it was resolved (possibly much earlier). found is false if the
// ledger has never recorded addr.
func (l *Ledger) LastObservationTime(addr Address) (t time.Time, found bool, err error) {
	chain, err := l.Chain()
	if err != nil {
		return time.Time{}, false, fmt.Errorf("last observation time: %w", err)
	}
	target := addr.String()
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		for _, in := range p.Resolution.Inputs {
			if in.Resource == target {
				parsed, err := time.Parse(time.RFC3339, p.Resolution.ResolvedAt)
				if err != nil {
					return time.Time{}, false, fmt.Errorf("last observation time: %s: bad resolved_at %q: %w", addr, p.Resolution.ResolvedAt, err)
				}
				return parsed, true, nil
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
			_, _, appliedAt, shipped, ferr := l.shippedCreateFold(p.ID, addr)
			if ferr != nil {
				return time.Time{}, false, fmt.Errorf("last observation time: %w", ferr)
			}
			if !shipped || appliedAt == "" {
				continue
			}
			parsed, perr := time.Parse(time.RFC3339, appliedAt)
			if perr != nil {
				return time.Time{}, false, fmt.Errorf("last observation time: %s: bad applied-at %q: %w", addr, appliedAt, perr)
			}
			return parsed, true, nil
		}
	}
	return time.Time{}, false, nil
}

// ProposalsForAddress returns every proposal in the ledger that recorded an
// observation of addr — any proposal with a resolution.inputs entry whose
// Resource matches addr's canonical string form, the same field
// LastObservedHash/LastObservationTime already key off — plus (UBI-29) the
// change proposal that created addr, once shipped, even though a create
// never gets a resolution.inputs entry for its own address. In practice
// this is addr's adoption (or shipped-create) proposal plus every
// subsequent drift_adopt/modify, in ledger (oldest-first) order — addr's
// full recorded history, `ubx why <address>`'s own genesis chain. An empty
// (nil) result means addr was never recorded; that's not an error, callers
// decide what "unknown address" should mean at their layer.
func (l *Ledger) ProposalsForAddress(addr Address) ([]*Proposal, error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, fmt.Errorf("proposals for address: %w", err)
	}
	target := addr.String()
	var matched []*Proposal
	for _, p := range chain {
		found := false
		for _, in := range p.Resolution.Inputs {
			if in.Resource == target {
				found = true
				break
			}
		}
		if !found && p.Kind == KindChange {
			for _, raw := range p.Delta.Creates {
				a, ok := createNodeAddress(raw)
				if !ok || a != addr || !isChangeCreateNode(raw) {
					continue
				}
				_, _, _, shipped, ferr := l.shippedCreateFold(p.ID, addr)
				if ferr != nil {
					return nil, fmt.Errorf("proposals for address: %w", ferr)
				}
				if shipped {
					found = true
				}
				break
			}
		}
		if found {
			matched = append(matched, p)
		}
	}
	return matched, nil
}

// FoldState reconstructs the ledger's currently-recorded full state for
// addr — architecture.md's "current infrastructure = fold(applied
// proposals)", restricted to one resource. addr's adoption proposal seeds
// the state from its full snapshot (Delta.Creates); each subsequent
// drift_adopt (or any Delta.Modifies touching addr) applies its After diff
// on top, in ledger order. A change proposal's own create (UBI-29,
// docs/schema.md's amendment) seeds state the same way, once shipped --
// from that proposal's own apply record (the resource's real, concrete
// post-apply attributes), never from Delta.Creates' own config (which may
// still carry now-stale $computed markers, or simply predates the real
// applied values entirely). found is false if addr was never adopted, or
// was created via a change proposal that hasn't shipped (successfully) yet.
//
// A shipped Delta.Destroys entry (UBI-30, docs/schema.md's "Amendment:
// destroys" -- the tombstone posture) folds addr back to !found, the
// mirror image of a shipped create seeding it: current is reset to nil
// the moment shippedDestroyFold confirms the destroy actually landed
// (destroyed or already_absent, gated the same per-resource way as a
// create), continuing the SAME chain walk rather than stopping there --
// a later proposal creating a new resource under the identical address
// (a real, legitimate lifecycle: tear down, rebuild under the same name)
// correctly re-seeds current/found from scratch, exactly as if addr had
// never been recorded before that later create at all. The ledger's own
// chain is never rewritten to make this true -- only FoldState's own
// derived, current-truth view folds through the tombstone; `ubx why`'s
// full biography (docs/schema.md's own "permanent history" posture) keeps
// showing the destroy proposal, and everything before and after it,
// unchanged.
//
// Accepted limit (UBI-7 follow-up, decided rather than left open): this is
// an O(chain length) linear walk via Chain(), with no index by address.
// That's a deliberate choice for the current scale — one stack, resources
// scanned individually by explicit CLI address — not an oversight to
// silently carry forward. Revisit (e.g. a per-address materialized index,
// updated incrementally on Append rather than recomputed on every read)
// once M1-2's auto-discovery makes "how many proposals touch this address"
// and "how many addresses does this ledger track" both grow past what a
// full walk on every scan/accept comfortably handles.
func (l *Ledger) FoldState(addr Address) (state json.RawMessage, found bool, err error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, false, fmt.Errorf("fold state: %w", err)
	}
	r, ferr := l.foldStateOverChain(chain, addr)
	return r.State, r.Found, ferr
}

// foldResult is what one fold over the chain knows about an address.
//
// A struct rather than a widening return list, because the readings are
// only meaningful together: State is what the address holds, Sources is
// what declared that state, and LastContributor is the proposal that set
// it. Three callers each want one of them, and the guarantee worth
// keeping is that all three describe the SAME operation.
type foldResult struct {
	State           json.RawMessage
	Sources         []IntentSource
	LastContributor *Proposal
	Found           bool
}

// LastContributor is the proposal that last set what this address holds.
//
// Not merely the last proposal that mentions the address: the fold's own
// gating applies, so an accepted-but-never-shipped change is not a
// contributor, and neither is a shipped-but-failed one. What comes back is
// the proposal whose effect is the state a reader sees now.
func (l *Ledger) LastContributor(addr Address) (*Proposal, bool, error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, false, fmt.Errorf("last contributor: %w", err)
	}
	r, err := l.foldStateOverChain(chain, addr)
	if err != nil {
		return nil, false, err
	}
	return r.LastContributor, r.LastContributor != nil, nil
}

// FoldSources reports the provenance of the address's CURRENT state: the
// sources recorded by the last operation that actually contributed to what
// FoldState returns.
//
// It is the same walk, not a second one. A resource's provenance and its
// state are two readings of one fold, so they cannot disagree about which
// operation they last took from -- that invariant is what makes
// DestroyEntry.Sources checkable rather than argued, and it is true by
// construction here rather than by two implementations kept in step.
//
// found follows FoldState's own: false means the ledger does not currently
// carry this address at all. A found resource with nil sources is a
// different and ordinary answer, meaning nothing in a declaration claims
// it: hand-written, never blueprint-produced, or produced by one and since
// re-declared by hand.
func (l *Ledger) FoldSources(addr Address) (sources []IntentSource, found bool, err error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, false, fmt.Errorf("fold sources: %w", err)
	}
	r, err := l.foldStateOverChain(chain, addr)
	return r.Sources, r.Found, err
}

// FoldSourcesAt is FoldSources as of an earlier head, standing in the same
// relation to it that FoldStateAt does to FoldState, and for the same
// caller: UBI-227's restore rebuilds a target address's config as it
// existed at an earlier head, and has to rebuild its provenance from that
// same head or lose it.
//
// Losing it is not hypothetical. Until this existed, restore emitted every
// reconstructed resource with no sources at all. A restore's modifies go
// through the resolver, which sets Provider unconditionally, so the fold
// read them as a hand-written re-declaration and CLEARED the blueprint
// provenance of every resource a restore touched. Its creates dropped it
// the same way, by carrying no sources key.
//
// So the one command whose purpose is putting a stack back the way it was
// silently destroyed the record of what had declared it. Invisible until
// UBI-284 gave a destroy somewhere to lose provenance from.
func (l *Ledger) FoldSourcesAt(headID string, addr Address) (sources []IntentSource, found bool, err error) {
	chain, err := l.ChainFrom(headID)
	if err != nil {
		return nil, false, fmt.Errorf("fold sources: %w", err)
	}
	r, ferr := l.foldStateOverChain(chain, addr)
	return r.Sources, r.Found, ferr
}

// FoldStateAt is FoldState's own real implementation, generalized to fold
// over the chain as of headID rather than always the current Head() --
// UBI-227's own restore reconstructs a target address's config as it
// existed at an earlier ledger head, using the exact same fold rules
// FoldState already uses for the current head (shipped-create/shipped-
// modify/shipped-destroy gating, drift_revert's own immediate-on-accept
// fold, all identical, all in the one shared helper below). FoldState
// itself is a thin wrapper: ChainFrom(Head()) is Chain(), so
// FoldStateAt(Head(), addr) and FoldState(addr) are the same computation
// by construction, not by two implementations kept in sync by hand.
func (l *Ledger) FoldStateAt(headID string, addr Address) (state json.RawMessage, found bool, err error) {
	chain, err := l.ChainFrom(headID)
	if err != nil {
		return nil, false, fmt.Errorf("fold state: %w", err)
	}
	r, ferr := l.foldStateOverChain(chain, addr)
	return r.State, r.Found, ferr
}

// restatesDeclaration reports whether a Modification re-states the
// resource's declaration, and so replaces its provenance, rather than
// merely recording something observed about a resource whose declaration
// nobody touched.
//
// The distinction is real and the fold depends on it. core/scan.go's own
// drift_adopt says "the cloud changed, record it"; it does not say "this
// resource is no longer blueprint-managed". A fold that took provenance
// from the last state-contributing operation regardless would erase a
// blueprint reference every time a resource drifted and was adopted. A
// hand-written modify is the opposite case and must clear it: re-stating a
// declaration to nothing is a decision, re-stating nothing at all is not.
//
// # This is an inference, written down rather than hidden
//
// Nothing in the schema says "this entry restates a declaration". The
// honest options were a new boolean field, which is a second hashed-content
// change on the heels of Sources' own, or reading Proposal.Kind, which puts
// the signal on the proposal rather than the entry and quietly mis-sorts
// any kind added later. This reads Provider instead, which is not a
// coincidence but a structural consequence: a record-only modify has
// nothing to apply and therefore no provider to apply it with, so
// core/scan.go leaves it nil at both its construction sites, and
// core/resolver sets it unconditionally on every Modification it produces.
// TestProviderDiscriminatesRestatement enumerates every producer so a new
// one that breaks the correspondence fails a test rather than silently
// mis-folding.
//
// # Why the pre-Provider gap is empty rather than merely unlikely
//
// Provider is itself additive (UBI-43), so a modify resolved before it
// existed has nil Provider and reads here as record-only. That would be a
// real hole if such a modify could ever appear where provenance exists to
// clear, and it cannot: Provider was added 2026-07-18 and resource-level
// Sources 2026-08-05, eighteen days later. A create can only carry sources
// if it was written on or after the later date; the fold ignores every
// modify preceding the create that seeds it (the current == nil guard
// below); and the ledger is append-only, so chain order is write order.
// Any modify this function's answer can actually change therefore sits
// after a create written after 2026-08-05, hence after 2026-07-18, hence
// carries Provider. Where the inference could be wrong there is no
// provenance to get wrong.
//
// That reasoning is load-bearing and rests on the amendment ordering, so it
// is stated here rather than left to be re-derived. If Sources is ever
// back-filled onto older creates the argument lapses and this needs a real
// field.
func restatesDeclaration(mod *Modification) bool { return mod.Provider != nil }

// createNodeSources pulls the provenance off a resolved create node.
// Creates are opaque json.RawMessage, so this is a second targeted decode
// rather than a read of the generic map the fold already built: "sources"
// has a pinned shape and decoding it as one keeps that shape in the type
// system instead of in a chain of interface{} assertions.
func createNodeSources(raw json.RawMessage) []IntentSource {
	var carrier struct {
		Sources []IntentSource `json:"sources"`
	}
	if err := json.Unmarshal(raw, &carrier); err != nil {
		return nil // not shaped like a resource node -- not our concern here
	}
	return carrier.Sources
}

// foldStateOverChain is the one real fold both FoldState and FoldStateAt
// share -- see FoldState's own doc comment for the full account of what
// this actually computes and why it's an O(chain length) walk, not an
// indexed lookup.
//
// It also returns the provenance of whatever state it arrived at, updated
// at exactly the points state itself is contributed, so FoldSources is a
// second reading of this one walk rather than a parallel implementation.
//
// It reports three readings of one walk, not three walks: the state, the
// provenance of that state, and the proposal that last contributed to it.
// They cannot disagree about which operation they came from, which is what
// makes each one checkable against the others.
func (l *Ledger) foldStateOverChain(chain []*Proposal, addr Address) (foldResult, error) {
	var (
		current map[string]interface{}
		sources []IntentSource
		last    *Proposal
		found   bool
	)
	for _, p := range chain {
		for _, raw := range p.Delta.Creates {
			var node map[string]interface{}
			if err := json.Unmarshal(raw, &node); err != nil {
				continue // not shaped like a resource node -- not our concern here
			}
			s, _ := node["stack"].(string)
			ty, _ := node["type"].(string)
			nm, _ := node["name"].(string)
			if s != addr.Stack || ty != addr.Type || nm != addr.Name {
				continue
			}
			if st, ok := node["state"].(map[string]interface{}); ok {
				current = st
				sources = createNodeSources(raw)
				last = p
				found = true
				continue
			}
			if _, ok := node["config"]; ok {
				result, _, _, shipped, ferr := l.shippedCreateFold(p.ID, addr)
				if ferr != nil {
					return foldResult{}, fmt.Errorf("fold state: %s: %w", addr, ferr)
				}
				if shipped {
					var seed map[string]interface{}
					if err := json.Unmarshal(result, &seed); err != nil {
						return foldResult{}, fmt.Errorf("fold state: %s: bad provider_result: %w", addr, err)
					}
					current = seed
					sources = createNodeSources(raw)
					last = p
					found = true
				}
				// Not yet shipped (or never applied successfully): leave
				// found as it was -- exactly like "not yet adopted."
			}
		}
		for _, mod := range p.Delta.Modifies {
			if mod.Target != addr || current == nil {
				continue
			}
			// UBI-89 P1: mirrors the create case's own "state" (trust
			// immediately) vs "config" (gate on shippedCreateFold) split
			// immediately above -- a drift_adopt's own Modifies entry is
			// record-only, exactly like an adoption's own "state"-shaped
			// create: Accept alone IS its complete lifecycle, no separate
			// `ubx ship` ever follows (cli/ship.go's own isRecordOnlyKind),
			// so it's trusted the moment it's accepted, same as always.
			// drift_revert is ALSO deliberately excluded here -- confirmed
			// via docs/architecture.md's own "Revert path" section, load-
			// bearing, not incidental: "accepting a drift_revert is a
			// decision to change cloud... ubx itself never applies it" --
			// FoldState folding a drift_revert's own `after` (the ledger's
			// restore target) immediately upon acceptance, independent of
			// whether/when it's ever actually shipped, IS the documented
			// design (TestRunScan_AfterRevertAccepted_ManualCorrection_
			// ScanClean's own explicit contract: accept declares the
			// ledger's truth, a real cloud apply is a separate, optional,
			// later act). Gating it here would silently break that.
			//
			// Only a kind:change modify (core/resolver's own OpModify,
			// `ubx plan`/`ubx resolve`/`ubx propose`'s own forward-looking
			// "make this happen" proposals -- the founder's own exact
			// UBI-89 repro shape) is gated: a modify only counts as
			// "current truth" once it actually, successfully shipped
			// (shippedModifyFold's own doc comment has the full incident
			// this fixes). Not yet shipped, or shipped-but-failed (e.g. a
			// VerifyFreshness refusal): current stays exactly as it was,
			// mirroring the create case's own "Not yet shipped... leave
			// found as it was -- exactly like 'not yet adopted.'"
			if p.Kind == KindChange {
				_, shipped, ferr := l.shippedModifyFold(p.ID, addr)
				if ferr != nil {
					return foldResult{}, fmt.Errorf("fold state: %s: %w", addr, ferr)
				}
				if !shipped {
					continue
				}
			}
			for path, raw := range mod.After {
				var v interface{}
				if err := json.Unmarshal(raw, &v); err != nil {
					return foldResult{}, fmt.Errorf("fold state: %s: bad after[%q]: %w", addr, path, err)
				}
				dotSet(current, path, v)
			}
			// Provenance follows the declaration, not the state change: a
			// drift record moves state without saying anything about what
			// declares the resource, so it passes through. See
			// restatesDeclaration.
			if restatesDeclaration(&mod) {
				sources = mod.Sources
			}
			last = p
			found = true
		}
		for i := range p.Delta.Destroys {
			d := &p.Delta.Destroys[i]
			if d.Address != addr {
				continue
			}
			_, shipped, ferr := l.shippedDestroyFold(p.ID, addr)
			if ferr != nil {
				return foldResult{}, fmt.Errorf("fold state: %s: %w", addr, ferr)
			}
			if shipped {
				current = nil
				sources = nil
				last = nil
				found = false
			}
			// Not yet shipped: leave current/found exactly as they were --
			// an accepted-but-unshipped destroy removes nothing yet,
			// mirroring a change proposal's own accepted-but-unshipped
			// create (above).
		}
	}
	if !found {
		return foldResult{}, nil
	}
	b, err := json.Marshal(current)
	if err != nil {
		return foldResult{}, fmt.Errorf("fold state: %w", err)
	}
	return foldResult{State: b, Sources: sources, LastContributor: last, Found: true}, nil
}

// dotSet applies a dot-notation path update onto a generic decoded-JSON
// map, creating intermediate objects as needed (docs/schema.md — Delta
// element shapes: Modification.Before/After are dot-notation keyed).
func dotSet(m map[string]interface{}, path string, val interface{}) {
	parts := strings.Split(path, ".")
	for i, part := range parts {
		if i == len(parts)-1 {
			m[part] = val
			return
		}
		next, ok := m[part].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			m[part] = next
		}
		m = next
	}
}

// dotDelete removes a dot-notation path from a generic decoded-JSON map --
// dotSet's own inverse, for a path that must be REMOVED rather than set to
// a new value. A missing intermediate object means the path doesn't exist
// in m at all; a no-op, not an error (ApplyAfter, its only caller, already
// knows the path existed in some prior observed state, not necessarily in
// the specific state m it's being applied onto).
func dotDelete(m map[string]interface{}, path string) {
	parts := strings.Split(path, ".")
	for i, part := range parts {
		if i == len(parts)-1 {
			delete(m, part)
			return
		}
		next, ok := m[part].(map[string]interface{})
		if !ok {
			return
		}
		m = next
	}
}

// DotGet reads the value at a dot-notation path out of a resource's
// observed state, returned as its own canonical json.RawMessage — shared
// by core/resolver (UBI-27, resolving a $ref/$cross marker to a concrete
// literal from a sibling's or a neighbor ledger's already-known state) the
// same way dotSet/dotDelete are already shared internally by ApplyAfter.
// found is false if any segment of the path is missing.
func DotGet(state json.RawMessage, path string) (value json.RawMessage, found bool, err error) {
	var m map[string]interface{}
	if err := json.Unmarshal(state, &m); err != nil {
		return nil, false, fmt.Errorf("dot get: decode state: %w", err)
	}
	v, ok := dotGetGeneric(m, path)
	if !ok {
		return nil, false, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false, fmt.Errorf("dot get: %w", err)
	}
	return b, true, nil
}

func dotGetGeneric(m map[string]interface{}, path string) (interface{}, bool) {
	parts := strings.Split(path, ".")
	cur := interface{}(m)
	for _, part := range parts {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		v, ok := mm[part]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// DiffAttributes computes the dot-notation attribute diff between two full
// resource states, restricted to attributes that actually changed —
// docs/schema.md's pinned Modification shape ("before/after hold only the
// attributes that changed, not full resource state"). Nested objects
// recurse (producing dot-paths); arrays and scalars are compared as
// atomic values. Exported (2026-07-17, UBI-27) so core/resolver can reuse
// it for a "modify" intent's before/after, the same mechanism
// GenerateProposal/GenerateRevertProposal already use for drift -- one
// diff mechanism, three callers, not three.
func DiffAttributes(beforeState, afterState json.RawMessage) (before, after map[string]json.RawMessage, err error) {
	var b, a map[string]interface{}
	if err := json.Unmarshal(beforeState, &b); err != nil {
		return nil, nil, fmt.Errorf("diff attributes: decode before: %w", err)
	}
	if err := json.Unmarshal(afterState, &a); err != nil {
		return nil, nil, fmt.Errorf("diff attributes: decode after: %w", err)
	}
	before = map[string]json.RawMessage{}
	after = map[string]json.RawMessage{}
	if err := diffObjects("", b, a, before, after); err != nil {
		return nil, nil, fmt.Errorf("diff attributes: %w", err)
	}
	return before, after, nil
}

func diffObjects(prefix string, b, a map[string]interface{}, beforeOut, afterOut map[string]json.RawMessage) error {
	keys := make(map[string]struct{}, len(b)+len(a))
	for k := range b {
		keys[k] = struct{}{}
	}
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range keys {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		bv, bok := b[k]
		av, aok := a[k]
		bObj, bIsObj := bv.(map[string]interface{})
		aObj, aIsObj := av.(map[string]interface{})
		// A $redacted marker (UBI-23, docs/schema.md's value-encoding
		// amendment) is atomic -- never recursed into, even though it
		// decodes to a map[string]interface{} like any other nested
		// object. Recursing would produce a spurious sub-path diff
		// (attr.$redacted.sha256: <hash1> -> <hash2>) instead of the
		// whole-attribute-level change a redacted value changing
		// actually is.
		if bok && aok && bIsObj && aIsObj && !isRedactedMarker(bObj) && !isRedactedMarker(aObj) {
			if err := diffObjects(path, bObj, aObj, beforeOut, afterOut); err != nil {
				return err
			}
			continue
		}
		if bok == aok && reflect.DeepEqual(bv, av) {
			continue
		}
		if bok {
			raw, err := json.Marshal(bv)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			beforeOut[path] = raw
		}
		if aok {
			raw, err := json.Marshal(av)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			afterOut[path] = raw
		}
	}
	return nil
}
