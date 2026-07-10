package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
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

// diffAttributes computes the dot-notation attribute diff between two full
// resource states, restricted to attributes that actually changed —
// docs/schema.md's pinned Modification shape ("before/after hold only the
// attributes that changed, not full resource state"). Nested objects
// recurse (producing dot-paths); arrays and scalars are compared as
// atomic values.
func diffAttributes(beforeState, afterState json.RawMessage) (before, after map[string]json.RawMessage, err error) {
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
		if bok && aok && bIsObj && aIsObj {
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
