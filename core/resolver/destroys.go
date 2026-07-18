package resolver

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ubiquex/ubiquex-cli/core"
)

// parseDestroyBatch validates intent's own destroys[] list and returns it
// as (address-by-key, ordered keys) -- docs/resolver.md's own "Amendment
// (UBI-30): destroys" resolve-time validation, symmetric to op:"create"/
// op:"modify"'s own presence check but the opposite direction: every
// destroy target must already be present in FoldState (ErrDestroyTargetMissing
// otherwise), duplicates within destroys[] are rejected
// (ErrDuplicateDestroy), and an address appearing in both resources[] and
// destroys[] is rejected outright (ErrDestroyResourceConflict) -- creating/
// modifying and destroying the same address in one intent file is
// structurally contradictory, never something to silently prioritize one
// way or the other. Called before any resources[] value resolution, so its
// result (the destroy-target set) can be threaded into resolveValue/
// resolveRef to refuse a $ref into a resource this same proposal is
// removing (ErrRefToDestroyTarget).
func parseDestroyBatch(l *core.Ledger, destroys []string, resourceBatch map[string]*batchEntry) (byKey map[string]core.Address, order []string, err error) {
	if len(destroys) == 0 {
		return nil, nil, nil
	}
	byKey = make(map[string]core.Address, len(destroys))
	order = make([]string, 0, len(destroys))
	for _, s := range destroys {
		addr, ok := core.ParseAddress(s)
		if !ok {
			return nil, nil, fmt.Errorf("%w: %q is not a valid address", ErrRefNotFound, s)
		}
		key := addr.String()
		if _, dup := byKey[key]; dup {
			return nil, nil, fmt.Errorf("%w: %s", ErrDuplicateDestroy, addr)
		}
		if _, conflict := resourceBatch[key]; conflict {
			return nil, nil, fmt.Errorf("%w: %s", ErrDestroyResourceConflict, addr)
		}
		_, found, ferr := l.FoldState(addr)
		if ferr != nil {
			return nil, nil, fmt.Errorf("resolve destroy %s: %w", addr, ferr)
		}
		if !found {
			return nil, nil, fmt.Errorf("%w: %s", ErrDestroyTargetMissing, addr)
		}
		byKey[key] = addr
		order = append(order, key)
	}
	return byKey, order, nil
}

// destroyAddrSet converts parseDestroyBatch's own byKey map into the plain
// membership set resolveValue/resolveRef need -- a separate, smaller type
// than map[string]core.Address purely so refs.go doesn't need to import
// anything beyond "is this address being destroyed," not what it equals.
func destroyAddrSet(byKey map[string]core.Address) map[string]bool {
	if len(byKey) == 0 {
		return nil
	}
	set := make(map[string]bool, len(byKey))
	for k := range byKey {
		set[k] = true
	}
	return set
}

// resolveDestroys finishes docs/resolver.md's own "Amendment (UBI-30):
// destroys" resolution -- resolve-time orphan protection (intra-stack via
// the ledger's own historically recorded depends_on edges, cross-stack via
// the operator-supplied knownDependents) and building each DestroyEntry's
// own full folded state plus reversed depends_on. Called after
// resourceBatch's own values are fully resolved (its own entries'
// .resolvedConfig is set), since the "handled, not orphaned" case below
// needs to know a same-batch modify exists at all, not what it resolved to
// (resolveRef's own ErrRefToDestroyTarget check already guarantees it no
// longer references a destroy target by this point).
//
// providers is docs/resolver.md's own "Amendment (2026-07-18, UBI-43):
// multi-provider stacks" input, threaded through from resolveOnce: each
// destroy target's own provider is inferred fresh against the currently
// declared set (by its own Address.Type), never inherited from whichever
// provider originally created it -- no proposal recorded that historically
// before this amendment, and trusting stale history over the stack's own
// current declaration would be exactly the silent-drift risk this project
// exists to catch. destroys[] entries carry no per-entry provider hint
// (docs/schema.md's own amendment scopes that escape hatch to
// resources[]) -- a genuinely ambiguous destroy target's type is a hard
// error, full stop.
func resolveDestroys(
	l *core.Ledger,
	byKey map[string]core.Address,
	order []string,
	resourceBatch map[string]*batchEntry,
	knownDependents []string,
	providers []DeclaredProvider,
) ([]core.DestroyEntry, []core.ResolutionInput, error) {
	if len(order) == 0 {
		return nil, nil, nil
	}

	reverseDeps, err := historicalReverseDependents(l)
	if err != nil {
		return nil, nil, err
	}

	// dependsOn[key] is the reversed-edge set feeding DestroyEntry.DependsOn
	// -- see the three-way classification in the loop below, docs/resolver.md's
	// own "Orphan protection" section.
	dependsOn := make(map[string][]string, len(order))
	for _, key := range order {
		var deps []string
		for _, depender := range reverseDeps[key] {
			_, alsoDestroyed := byKey[depender]
			_, alsoTouched := resourceBatch[depender]
			switch {
			case alsoDestroyed:
				// Mutual destroy: depender is also being destroyed in this
				// batch -- it must go first (it still depends on this
				// target up until its own destruction).
				deps = append(deps, depender)
			case alsoTouched:
				// Handled: depender is being created/modified in this same
				// batch -- its own operation (which, by construction, no
				// longer $refs this destroy target -- resolveRef's own
				// ErrRefToDestroyTarget check) must complete first.
				deps = append(deps, depender)
			default:
				dependerAddr, ok := core.ParseAddress(depender)
				if !ok {
					continue
				}
				_, stillExists, ferr := l.FoldState(dependerAddr)
				if ferr != nil {
					return nil, nil, fmt.Errorf("resolve destroy %s: %w", byKey[key], ferr)
				}
				if stillExists {
					return nil, nil, fmt.Errorf("%w: %s is still referenced by %s, which this proposal does not also destroy or update",
						ErrDestroyOrphaned, byKey[key], depender)
				}
				// depender no longer exists per the ledger's own current
				// knowledge -- nothing left to orphan.
			}
		}
		sort.Strings(deps)
		dependsOn[key] = deps
	}

	// topoSort's own DFS (shared with the creates-batch sort, graph.go)
	// unconditionally follows every edge edgesOf returns, including outside
	// nodes -- sound for creates, whose own edges are already restricted to
	// the batch (scanRefEdges' own doc comment), but not for destroys:
	// dependsOn[key] can legitimately name a same-batch MODIFY (the
	// "handled" case above), which isn't itself a destroy target and so
	// isn't in order/byKey at all. Restrict the ordering walk to
	// destroy-internal edges only -- the full dependsOn[key] (including any
	// non-destroy addresses) still ends up on the DestroyEntry itself,
	// below; only the topo-sort's own traversal needs the closed subset.
	topoOrder, err := topoSort(order, func(key string) []string {
		var withinBatch []string
		for _, dep := range dependsOn[key] {
			if _, ok := byKey[dep]; ok {
				withinBatch = append(withinBatch, dep)
			}
		}
		return withinBatch
	})
	if err != nil {
		return nil, nil, err
	}

	entries := make([]core.DestroyEntry, 0, len(topoOrder))
	var inputs []core.ResolutionInput
	for _, key := range topoOrder {
		addr := byKey[key]
		state, _, ferr := l.FoldState(addr) // presence already confirmed, parseDestroyBatch
		if ferr != nil {
			return nil, nil, fmt.Errorf("resolve destroy %s: %w", addr, ferr)
		}
		prov, perr := InferProvider(providers, addr.Type, nil) // no per-entry hint for destroys -- docs/schema.md's own amendment
		if perr != nil {
			return nil, nil, perr
		}
		entries = append(entries, core.DestroyEntry{
			Address:   addr,
			State:     state,
			DependsOn: dependsOn[key],
			Provider:  &core.ProviderRef{Source: prov.Source, Version: prov.Version},
		})

		observedHash, herr := core.ObservedHash(state)
		if herr != nil {
			return nil, nil, fmt.Errorf("resolve destroy %s: %w", addr, herr)
		}
		lookup, found, lerr := l.LastLookup(addr)
		if lerr != nil {
			return nil, nil, fmt.Errorf("resolve destroy %s: %w", addr, lerr)
		}
		if !found {
			return nil, nil, fmt.Errorf("%w: %s", ErrDestroyTargetNoLookup, addr)
		}
		inputs = append(inputs, core.ResolutionInput{
			Kind:         "destroy_target",
			Resource:     key,
			ObservedHash: observedHash,
			Lookup:       lookup,
		})

		crossInput, cerr := crossStackOrphanCheck(addr, knownDependents)
		if cerr != nil {
			return nil, nil, cerr
		}
		inputs = append(inputs, crossInput)
	}

	return entries, inputs, nil
}

// historicalReverseDependents walks l's whole chain once and returns,
// keyed by canonical address string, every OTHER address whose OWN MOST
// RECENTLY RECORDED create/modify names it via depends_on -- docs/resolver.md's
// own "orphan protection" walk ("does any live resource still depend on
// this address"), computed once for the whole destroy batch rather than
// once per target (the ledger is walked once regardless of how many
// destroy targets are being resolved, the same "linear walk, no index"
// accepted-scale posture FoldState/Fleet already take).
//
// "Most recently recorded," not "ever recorded," is load-bearing --
// discovered while implementing (a real destroy was wrongly refused
// against a dependent that had already been repointed away by a later
// modify, exactly the scenario "handled" case above exists to allow): a
// dependency, once recorded, is not permanent history the way the ledger
// entry recording it is -- a later modify to the SAME address can
// genuinely stop referencing the destroy target (resolveRef's own
// ErrRefToDestroyTarget check already guarantees the LATEST config
// provably doesn't, if it was resolved in the same batch as a destroy),
// and the orphan check must follow that address's own current
// depends_on, not its very first one. This mirrors the same "immutable
// history, current truth computed by folding over it" precedence
// FoldState/Fleet already use for state/lookup -- applied here to
// depends_on specifically, walking the chain in order so a later
// proposal's own depends_on (including an empty one) overwrites an
// earlier one for the same address.
//
// "Later," here, means a later kind:"change" proposal specifically --
// found live, not assumed, during UBI-30's own live AWS finale: a
// drift_adopt proposal recording an unrelated out-of-band correction
// (e.g. a tag change) to the SAME address was being folded into this map
// too, its own Modification.DependsOn (always empty -- that field is a
// resolver-produced, change-proposal-only concept; drift_adopt's own
// GenerateProposal never populates it, docs/schema.md's own amendment)
// silently overwriting and erasing a REAL depends_on relationship a
// change proposal had recorded earlier for the same address. The live
// symptom: destroying a queue alone (its dependent policy never touched
// since its own original create) resolved successfully instead of being
// refused, because the policy's intervening drift_adopt correction had
// wiped its own recorded dependency on the queue from this map entirely.
// Fixed by only folding Delta.Modifies from KindChange proposals -- the
// only kind whose own Modification entries ever legitimately carry
// depends_on at all; every other kind's own modifies are simply skipped
// here; not overwriting the map, not misread as "no longer depends on
// anything."
func historicalReverseDependents(l *core.Ledger) (map[string][]string, error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, fmt.Errorf("resolve destroys: %w", err)
	}
	latestDependsOn := map[string][]string{}
	for _, p := range chain {
		if p.Kind == core.KindChange {
			for _, raw := range p.Delta.Creates {
				var node map[string]interface{}
				if err := json.Unmarshal(raw, &node); err != nil {
					continue
				}
				if _, hasConfig := node["config"]; !hasConfig {
					continue // adoption's own state-keyed shape, not a change-proposal create
				}
				stack, _ := node["stack"].(string)
				typ, _ := node["type"].(string)
				name, _ := node["name"].(string)
				if stack == "" || typ == "" || name == "" {
					continue
				}
				addr := core.Address{Stack: stack, Type: typ, Name: name}.String()
				var deps []string
				if raw, ok := node["depends_on"].([]interface{}); ok {
					for _, d := range raw {
						if s, ok := d.(string); ok {
							deps = append(deps, s)
						}
					}
				}
				latestDependsOn[addr] = deps
			}
		}
		if p.Kind != core.KindChange {
			continue
		}
		for _, m := range p.Delta.Modifies {
			latestDependsOn[m.Target.String()] = append([]string(nil), m.DependsOn...)
		}
	}

	reverse := map[string][]string{}
	for addr, deps := range latestDependsOn {
		for _, dep := range deps {
			reverse[dep] = append(reverse[dep], addr)
		}
	}
	return reverse, nil
}

// crossStackOrphanCheck implements docs/resolver.md's own best-effort,
// explicit cross-stack half of orphan protection: walk every named
// neighbor ledger for a cross_stack_pin naming addr, refusing
// (ErrDestroyOrphaned) if one is found. Returns the resolution.inputs
// evidence entry to record either way -- "not_performed" (knownDependents
// empty: an honest, expected gap, never silently indistinguishable from a
// real check) or "checked_clear" (every named dir was walked, none
// pinned addr).
func crossStackOrphanCheck(addr core.Address, knownDependents []string) (core.ResolutionInput, error) {
	target := addr.String()
	if len(knownDependents) == 0 {
		return core.ResolutionInput{
			Kind:     "cross_stack_orphan_check",
			Resource: target,
			Status:   "not_performed",
		}, nil
	}
	for _, dir := range knownDependents {
		neighbor := core.Open(dir)
		if !neighbor.Exists() {
			return core.ResolutionInput{}, fmt.Errorf("%w: %s", ErrDependentLedgerMissing, dir)
		}
		chain, err := neighbor.Chain()
		if err != nil {
			return core.ResolutionInput{}, fmt.Errorf("resolve destroy %s: known_dependents %s: %w", addr, dir, err)
		}
		for _, p := range chain {
			for _, in := range p.Resolution.Inputs {
				if in.Kind == "cross_stack_pin" && in.Resource == target {
					return core.ResolutionInput{}, fmt.Errorf("%w: %s is pinned by a cross-stack reference in %s",
						ErrDestroyOrphaned, addr, dir)
				}
			}
		}
	}
	return core.ResolutionInput{
		Kind:              "cross_stack_orphan_check",
		Resource:          target,
		Status:            "checked_clear",
		CheckedLedgerDirs: append([]string(nil), knownDependents...),
	}, nil
}
