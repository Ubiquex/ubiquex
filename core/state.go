package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Chain returns every proposal in l, oldest (genesis) first, by walking
// Parent links back from Head(). Ledgers are expected to be small at this
// stage (foundational-slice scale); this is a straightforward linear walk,
// not an indexed lookup.
func (l *Ledger) Chain() ([]*Proposal, error) {
	head, err := l.Head()
	if err != nil {
		return nil, fmt.Errorf("chain: %w", err)
	}
	var reversed []*Proposal
	for id := head; id != ""; {
		p, err := l.Read(id)
		if err != nil {
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

// LastObservedHash returns the most recently recorded observed_hash for
// addr — the newest resolution.inputs entry, across the whole ledger
// chain, whose resource matches addr's canonical address string. found is
// false if the ledger has never recorded addr (never scanned/adopted).
func (l *Ledger) LastObservedHash(addr Address) (hash string, found bool, err error) {
	chain, err := l.Chain()
	if err != nil {
		return "", false, fmt.Errorf("last observed hash: %w", err)
	}
	target := addr.String()
	for i := len(chain) - 1; i >= 0; i-- {
		for _, in := range chain[i].Resolution.Inputs {
			if in.Resource == target {
				return in.ObservedHash, true, nil
			}
		}
	}
	return "", false, nil
}

// LastObservationTime returns the resolved_at timestamp of the proposal
// that most recently recorded addr's observed state — the same proposal
// LastObservedHash reads its hash from. Used by AttributeDrift (UBI-10) to
// bound the CloudTrail correlation window's start: "since" is when ubx last
// looked at this resource, not an arbitrary lookback. found is false if the
// ledger has never recorded addr.
func (l *Ledger) LastObservationTime(addr Address) (t time.Time, found bool, err error) {
	chain, err := l.Chain()
	if err != nil {
		return time.Time{}, false, fmt.Errorf("last observation time: %w", err)
	}
	target := addr.String()
	for i := len(chain) - 1; i >= 0; i-- {
		for _, in := range chain[i].Resolution.Inputs {
			if in.Resource == target {
				parsed, err := time.Parse(time.RFC3339, chain[i].Resolution.ResolvedAt)
				if err != nil {
					return time.Time{}, false, fmt.Errorf("last observation time: %s: bad resolved_at %q: %w", addr, chain[i].Resolution.ResolvedAt, err)
				}
				return parsed, true, nil
			}
		}
	}
	return time.Time{}, false, nil
}

// ProposalsForAddress returns every proposal in the ledger that recorded an
// observation of addr — any proposal with a resolution.inputs entry whose
// Resource matches addr's canonical string form, the same field
// LastObservedHash/LastObservationTime already key off. In practice this is
// addr's adoption proposal plus every subsequent drift_adopt, in ledger
// (oldest-first) order — addr's full recorded history. Used by `ubx why`
// (UBI-11) to resolve a resource address into its proposal chain, not just
// a single proposal ID. An empty (nil) result means addr was never
// recorded; that's not an error, callers decide what "unknown address"
// should mean at their layer.
func (l *Ledger) ProposalsForAddress(addr Address) ([]*Proposal, error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, fmt.Errorf("proposals for address: %w", err)
	}
	target := addr.String()
	var matched []*Proposal
	for _, p := range chain {
		for _, in := range p.Resolution.Inputs {
			if in.Resource == target {
				matched = append(matched, p)
				break
			}
		}
	}
	return matched, nil
}

// FoldState reconstructs the ledger's currently-recorded full state for
// addr — architecture.md's "current infrastructure = fold(applied
// proposals)", restricted to one resource. addr's adoption proposal seeds
// the state from its full snapshot (Delta.Creates); each subsequent
// drift_adopt (or any Delta.Modifies touching addr) applies its After diff
// on top, in ledger order. found is false if addr was never adopted.
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

	var current map[string]interface{}
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
				found = true
			}
		}
		for _, mod := range p.Delta.Modifies {
			if mod.Target != addr || current == nil {
				continue
			}
			for path, raw := range mod.After {
				var v interface{}
				if err := json.Unmarshal(raw, &v); err != nil {
					return nil, false, fmt.Errorf("fold state: %s: bad after[%q]: %w", addr, path, err)
				}
				dotSet(current, path, v)
			}
			found = true
		}
	}
	if !found {
		return nil, false, nil
	}
	b, err := json.Marshal(current)
	if err != nil {
		return nil, false, fmt.Errorf("fold state: %w", err)
	}
	return b, true, nil
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
