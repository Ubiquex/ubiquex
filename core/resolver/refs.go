package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex-cli/core"
)

// Marker keys -- docs/schema.md's value-encoding conventions. $ref/$cross
// are input-only (docs/resolver.md: "never appears in the resolved
// output"); $computed/$secret/$ephemeral are the existing, founding-draft
// markers, used exactly as already drafted.
const (
	markerRef       = "$ref"
	markerCross     = "$cross"
	markerSecret    = "$secret"
	markerComputed  = "$computed"
	markerEphemeral = "$ephemeral"
)

// asMarker reports whether v is exactly {key: inner-object} -- one key,
// mirroring core.RedactedMarkerKey's own "$redacted" recognition
// convention (core/redacted.go's isRedactedMarker): any other shape
// (additional keys, a non-object value under the key) is not a marker.
func asMarker(v interface{}, key string) (map[string]interface{}, bool) {
	m, ok := v.(map[string]interface{})
	if !ok || len(m) != 1 {
		return nil, false
	}
	inner, ok := m[key]
	if !ok {
		return nil, false
	}
	innerMap, ok := inner.(map[string]interface{})
	if !ok {
		return nil, false
	}
	return innerMap, true
}

// isEphemeralMarker reports whether v is exactly {"$ephemeral": true} --
// atomic, like $redacted: a value that existed but was deliberately
// excluded from what got persisted, so there is nothing else in the
// object to represent (docs/resolver.md's own "Ephemeral" section).
func isEphemeralMarker(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	if !ok || len(m) != 1 {
		return false
	}
	b, ok := m[markerEphemeral].(bool)
	return ok && b
}

// isComputedMarker reports whether v is exactly {"$computed": {...}}.
func isComputedMarker(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	if !ok || len(m) != 1 {
		return false
	}
	_, ok = m[markerComputed]
	return ok
}

// splitRefTarget splits a "$ref"/"$cross" target string
// "<stack>.<type>.<name>.<attr-path>" into its address portion and
// attribute path -- core.ParseAddress's own SplitN(s, ".", 3) convention,
// extended one component further (the canonical address+path form
// docs/schema.md's amendment uses for both $ref.to and $cross.to).
func splitRefTarget(s string) (addr string, path string, err error) {
	parts := strings.SplitN(s, ".", 4)
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return "", "", fmt.Errorf("%w: %q is not a well-formed <stack>.<type>.<name>.<path> reference", ErrRefNotFound, s)
	}
	return parts[0] + "." + parts[1] + "." + parts[2], parts[3], nil
}

// dotGet reads the value at a dot-notation path from an already-decoded
// JSON object -- the same traversal core.DotGet performs on a
// json.RawMessage, but operating directly on a batch sibling's own
// already-resolved map[string]interface{} (never re-marshaled/re-decoded
// just to look one path up).
func dotGet(m map[string]interface{}, path string) (interface{}, bool) {
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

// scanRefEdges walks a raw (unresolved) config value and returns the
// canonical addresses of every $ref target that's also in this batch --
// the dependency-graph edges for this resource, computed before any value
// is actually resolved (docs/resolver.md's own two-pass shape: scan for
// edges, topo-sort, then resolve in that order -- resolving a ref needs to
// know whether its target is already resolved, which needs the order,
// which needs every edge first). $cross never contributes an edge (a
// neighbor stack's resources are never in this batch); $secret/$ephemeral
// are atomic and never walked into.
func scanRefEdges(v interface{}, batch map[string]*batchEntry) ([]string, error) {
	seen := map[string]bool{}
	var walk func(v interface{}) error
	walk = func(v interface{}) error {
		switch t := v.(type) {
		case map[string]interface{}:
			if inner, ok := asMarker(t, markerRef); ok {
				to, _ := inner["to"].(string)
				addrStr, _, err := splitRefTarget(to)
				if err != nil {
					return err
				}
				if _, inBatch := batch[addrStr]; inBatch {
					seen[addrStr] = true
				}
				return nil
			}
			if _, ok := asMarker(t, markerCross); ok {
				return nil
			}
			if _, ok := asMarker(t, markerSecret); ok {
				return nil
			}
			if isEphemeralMarker(t) {
				return nil
			}
			for _, vv := range t {
				if err := walk(vv); err != nil {
					return err
				}
			}
		case []interface{}:
			for _, vv := range t {
				if err := walk(vv); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(v); err != nil {
		return nil, err
	}
	edges := make([]string, 0, len(seen))
	for k := range seen {
		edges = append(edges, k)
	}
	sort.Strings(edges)
	return edges, nil
}

// unionDependsOn merges ri.DependsOn (docs/schema.md's own "Amendment:
// ResourceIntent.DependsOn", UBI-47 -- a topology-only dependency signal
// with no config-attribute opinion, the diagram medium's own real need)
// into edges, the same set scanRefEdges already derived from $ref/$cross
// markers found inside config -- one dependency graph, not two.
//
// Exactly the same two-case split scanRefEdges/resolveRef already
// established for $ref: an address inside this batch becomes a real
// dependency-graph edge (execution ordering, cycle detection, and the
// resolved output's own depends_on); an address already recorded in the
// ledger (not in this batch) is validated to exist -- via the identical
// l.FoldState lookup resolveRef's own non-batch $ref case already uses --
// but never added as an edge, since there's nothing to wait for, it
// already exists. An address that resolves to neither is ErrRefNotFound,
// the identical sentinel a dangling $ref already produces, not a new
// diagram-specific one -- a broken reference is a broken reference
// regardless of which mechanism named it. destroySet mirrors resolveRef's
// own ErrRefToDestroyTarget check: depending on something this same
// proposal is also destroying is never sound, whichever mechanism named
// the dependency.
func unionDependsOn(edges []string, dependsOn []string, batch map[string]*batchEntry, destroySet map[string]bool, l *core.Ledger, from core.Address) ([]string, error) {
	if len(dependsOn) == 0 {
		return edges, nil
	}
	seen := make(map[string]bool, len(edges))
	for _, e := range edges {
		seen[e] = true
	}
	out := append([]string(nil), edges...)
	for _, to := range dependsOn {
		if destroySet[to] {
			return nil, fmt.Errorf("%w: %s (named in %s's own depends_on)", ErrRefToDestroyTarget, to, from)
		}
		if _, inBatch := batch[to]; inBatch {
			if !seen[to] {
				seen[to] = true
				out = append(out, to)
			}
			continue
		}
		addr, ok := core.ParseAddress(to)
		if !ok {
			return nil, fmt.Errorf("%w: %q is not a valid address (named in %s's own depends_on)", ErrRefNotFound, to, from)
		}
		_, found, err := l.FoldState(addr)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: depends_on %s: %w", from, to, err)
		}
		if !found {
			return nil, fmt.Errorf("%w: %s (named in %s's own depends_on)", ErrRefNotFound, to, from)
		}
	}
	sort.Strings(out)
	return out, nil
}

// resolveValue walks a resource's raw config (or a sub-value of it) and
// replaces every $ref/$cross/$secret/$ephemeral marker per
// docs/resolver.md's own rules. path is the dot-notation attribute path to
// v itself (root call: ""), used only for $secret's IsSensitive check --
// the one place resolution needs to know exactly where in typeName's own
// schema this value sits. destroyAddrs is the current batch's own
// destroys[] set (docs/resolver.md's "Amendment (UBI-30): destroys") --
// threaded through so resolveRef can refuse a $ref into a resource this
// same proposal is removing (ErrRefToDestroyTarget); nil/empty for a
// proposal with no destroys, same as always.
func resolveValue(v interface{}, path, typeName string, l *core.Ledger, schema SchemaInspector, batch map[string]*batchEntry, destroyAddrs map[string]bool) (interface{}, []core.ResolutionInput, error) {
	switch t := v.(type) {
	case map[string]interface{}:
		if inner, ok := asMarker(t, markerRef); ok {
			return resolveRef(inner, batch, destroyAddrs, l)
		}
		if inner, ok := asMarker(t, markerCross); ok {
			return resolveCross(inner, l)
		}
		if inner, ok := asMarker(t, markerSecret); ok {
			if !schema.IsSensitive(typeName, path) {
				return nil, nil, fmt.Errorf("%w: %s.%s", ErrSecretNotSensitive, typeName, path)
			}
			return map[string]interface{}{markerSecret: inner}, nil, nil
		}
		if isEphemeralMarker(t) {
			return t, nil, nil
		}
		out := make(map[string]interface{}, len(t))
		var allInputs []core.ResolutionInput
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			rv, inputs, err := resolveValue(t[k], childPath, typeName, l, schema, batch, destroyAddrs)
			if err != nil {
				return nil, nil, err
			}
			out[k] = rv
			allInputs = append(allInputs, inputs...)
		}
		return out, allInputs, nil
	case []interface{}:
		out := make([]interface{}, len(t))
		var allInputs []core.ResolutionInput
		for i, vv := range t {
			rv, inputs, err := resolveValue(vv, path, typeName, l, schema, batch, destroyAddrs)
			if err != nil {
				return nil, nil, err
			}
			out[i] = rv
			allInputs = append(allInputs, inputs...)
		}
		return out, allInputs, nil
	default:
		return v, nil, nil
	}
}

// resolveRef resolves one $ref marker's inner {"to": "..."} -- either to a
// sibling create/modify in this same batch (already resolved, by topo
// order) or an already-ledgered resource this batch doesn't touch
// (docs/resolver.md's own two cases). A target this same proposal is also
// destroying is refused outright (ErrRefToDestroyTarget, docs/resolver.md's
// "Amendment (UBI-30): destroys") -- referencing a resource whose value is
// being removed in the same breath is never sound, and rejecting it here
// is what guarantees a same-batch modify that depends on a destroy target
// (the "handled" case in resolveDestroys) provably no longer references it
// by the time that modify's own config is resolved.
//
// A same-batch target's own IsComputed check reads target's own resolved
// provider (target.provider.Schema), not the caller's -- docs/resolver.md's
// own "Amendment (2026-07-18, UBI-43): multi-provider stacks" -- since a
// $ref can cross a provider boundary (an aws_db_instance node referenced
// by a helm_release node's own config), the schema that answers "is this
// attribute Computed" must be the REFERENCED resource's own provider, set
// on every batch entry before any value resolution begins (resolveOnce's
// own resources[] loop), never the schema of whichever entry happens to
// be resolving right now.
func resolveRef(inner map[string]interface{}, batch map[string]*batchEntry, destroyAddrs map[string]bool, l *core.Ledger) (interface{}, []core.ResolutionInput, error) {
	to, _ := inner["to"].(string)
	addrStr, attrPath, err := splitRefTarget(to)
	if err != nil {
		return nil, nil, err
	}
	if destroyAddrs[addrStr] {
		return nil, nil, fmt.Errorf("%w: %s.%s", ErrRefToDestroyTarget, addrStr, attrPath)
	}

	if target, inBatch := batch[addrStr]; inBatch {
		if target.resolvedConfig == nil {
			return nil, nil, fmt.Errorf("resolve: internal: %s referenced before it was resolved (topo order bug)", addrStr)
		}
		if target.provider.Schema.IsComputed(target.ri.Type, attrPath) {
			return map[string]interface{}{
				markerComputed: map[string]interface{}{"from": to},
			}, nil, nil
		}
		val, ok := dotGet(target.resolvedConfig, attrPath)
		if !ok {
			return nil, nil, fmt.Errorf("%w: %s.%s", ErrRefNotFound, addrStr, attrPath)
		}
		if isComputedMarker(val) {
			return nil, nil, fmt.Errorf("%w: %s.%s resolved to $computed, but %s's own schema says this attribute is concrete",
				ErrComputedWhereConcreteRequired, addrStr, attrPath, target.ri.Type)
		}
		return val, nil, nil
	}

	addr, ok := core.ParseAddress(addrStr)
	if !ok {
		return nil, nil, fmt.Errorf("%w: %q is not a valid address", ErrRefNotFound, addrStr)
	}
	state, found, err := l.FoldState(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve ref %s: %w", to, err)
	}
	if !found {
		return nil, nil, fmt.Errorf("%w: %s", ErrRefNotFound, addr)
	}
	val, ok, err := core.DotGet(state, attrPath)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("%w: %s.%s", ErrRefNotFound, addr, attrPath)
	}
	var decoded interface{}
	if err := json.Unmarshal(val, &decoded); err != nil {
		return nil, nil, fmt.Errorf("resolve ref %s: %w", to, err)
	}
	return decoded, nil, nil
}

// resolveCross resolves one $cross marker's inner
// {"ledger_dir": "...", "to": "..."} (the legacy, forever-supported,
// explicit-path shape) or {"stack": "...", "to": "..."} (UBI-32 Arc B's
// own "resolve by NAME against the base," docs/architecture.md --
// Addressing) into the neighbor's own already-applied, already-concrete
// FoldState (docs/resolver.md's own "Cross-stack refs" section),
// recording pinned_head as a new resolution.inputs entry (docs/schema.md's
// amendment). l is the CURRENT stack's own ledger -- consulted only for
// its own BaseStore()/ExternalStack() addressing metadata (set via
// core.OpenStoreForStack), never read or written to otherwise.
func resolveCross(inner map[string]interface{}, l *core.Ledger) (interface{}, []core.ResolutionInput, error) {
	ledgerDir, _ := inner["ledger_dir"].(string)
	stackName, _ := inner["stack"].(string)
	to, _ := inner["to"].(string)
	if ledgerDir != "" && stackName != "" {
		return nil, nil, fmt.Errorf("%w: $cross takes exactly one of \"ledger_dir\" or \"stack\", not both", ErrRefNotFound)
	}
	if to == "" || (ledgerDir == "" && stackName == "") {
		return nil, nil, fmt.Errorf("%w: $cross requires \"to\" and either \"ledger_dir\" (an explicit path) or \"stack\" (a name resolved against this stack's own base store)", ErrRefNotFound)
	}
	addrStr, attrPath, err := splitRefTarget(to)
	if err != nil {
		return nil, nil, err
	}
	addr, ok := core.ParseAddress(addrStr)
	if !ok {
		return nil, nil, fmt.Errorf("%w: %q is not a valid address", ErrRefNotFound, addrStr)
	}

	ref := ledgerDir
	if stackName != "" {
		base := l.BaseStore()
		if override, ok := l.ExternalStack(stackName); ok {
			base = override
		}
		if base == "" {
			return nil, nil, fmt.Errorf("%w: $cross \"stack\": %q has no base store to resolve against -- this stack's own [ledger] store is git-local (use \"ledger_dir\" directly instead), and no [ledger.external] entry names %q", ErrRefNotFound, stackName, stackName)
		}
		ref = deriveStackAddress(base, stackName)
	}

	neighbor, closeNeighbor, err := core.OpenRef(context.Background(), ref)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve cross-stack ref %s: %w", to, err)
	}
	defer closeNeighbor()
	if !neighbor.Exists() {
		return nil, nil, fmt.Errorf("%w: %s", ErrNeighborLedgerMissing, ref)
	}
	head, err := neighbor.Head()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve cross-stack ref %s: %w", to, err)
	}
	state, found, err := neighbor.FoldState(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve cross-stack ref %s: %w", to, err)
	}
	if !found {
		return nil, nil, fmt.Errorf("%w: %s not recorded in neighbor ledger %s", ErrCrossStackAddressNotFound, addr, ref)
	}
	observedHash, err := core.ObservedHash(state)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve cross-stack ref %s: %w", to, err)
	}
	val, ok, err := core.DotGet(state, attrPath)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("%w: %s.%s", ErrRefNotFound, addr, attrPath)
	}
	var decoded interface{}
	if err := json.Unmarshal(val, &decoded); err != nil {
		return nil, nil, fmt.Errorf("resolve cross-stack ref %s: %w", to, err)
	}

	// docs/resolver.md's own precise edge case: if the neighbor's own
	// FoldState at this path is itself $computed (an accepted-but-unshipped
	// change proposal there), it propagates forward unresolved -- honest,
	// not guessed. No special handling needed here: decoded already IS
	// that marker if so, passed through exactly like any other value.
	return decoded, []core.ResolutionInput{{
		Kind:         "cross_stack_pin",
		Resource:     addr.String(),
		ObservedHash: observedHash,
		PinnedHead:   head,
		LedgerDir:    ref,
	}}, nil
}

// deriveStackAddress joins base and stack into <base>/<stack>/
// (docs/architecture.md's own addressing rule) -- pure string
// manipulation, deliberately not reusing ledgerstore.WithStack's own
// URL-aware join: core/resolver stays free of anything ledgerstore-
// shaped, the same discipline core itself holds to, and this join never
// needs to be URL-aware in the first place -- the query-param
// translation only matters once core.OpenRef's own registered opener
// actually opens the resulting string, not before.
func deriveStackAddress(base, stack string) string {
	return strings.TrimSuffix(base, "/") + "/" + stack + "/"
}
