package resolver

import (
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

// resolveValue walks a resource's raw config (or a sub-value of it) and
// replaces every $ref/$cross/$secret/$ephemeral marker per
// docs/resolver.md's own rules. path is the dot-notation attribute path to
// v itself (root call: ""), used only for $secret's IsSensitive check --
// the one place resolution needs to know exactly where in typeName's own
// schema this value sits.
func resolveValue(v interface{}, path, typeName string, l *core.Ledger, schema SchemaInspector, batch map[string]*batchEntry) (interface{}, []core.ResolutionInput, error) {
	switch t := v.(type) {
	case map[string]interface{}:
		if inner, ok := asMarker(t, markerRef); ok {
			return resolveRef(inner, batch, l, schema)
		}
		if inner, ok := asMarker(t, markerCross); ok {
			return resolveCross(inner)
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
			rv, inputs, err := resolveValue(t[k], childPath, typeName, l, schema, batch)
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
			rv, inputs, err := resolveValue(vv, path, typeName, l, schema, batch)
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
// (docs/resolver.md's own two cases).
func resolveRef(inner map[string]interface{}, batch map[string]*batchEntry, l *core.Ledger, schema SchemaInspector) (interface{}, []core.ResolutionInput, error) {
	to, _ := inner["to"].(string)
	addrStr, attrPath, err := splitRefTarget(to)
	if err != nil {
		return nil, nil, err
	}

	if target, inBatch := batch[addrStr]; inBatch {
		if target.resolvedConfig == nil {
			return nil, nil, fmt.Errorf("resolve: internal: %s referenced before it was resolved (topo order bug)", addrStr)
		}
		if schema.IsComputed(target.ri.Type, attrPath) {
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

// resolveCross resolves one $cross marker's inner {"ledger_dir": "...",
// "to": "<stack>.<type>.<name>.<path>"} -- the neighbor's own
// already-applied, already-concrete FoldState (docs/resolver.md's own
// "Cross-stack refs" section), recording pinned_head as a new
// resolution.inputs entry (docs/schema.md's amendment).
func resolveCross(inner map[string]interface{}) (interface{}, []core.ResolutionInput, error) {
	ledgerDir, _ := inner["ledger_dir"].(string)
	to, _ := inner["to"].(string)
	if ledgerDir == "" || to == "" {
		return nil, nil, fmt.Errorf("%w: $cross requires \"ledger_dir\" and \"to\"", ErrRefNotFound)
	}
	addrStr, attrPath, err := splitRefTarget(to)
	if err != nil {
		return nil, nil, err
	}
	addr, ok := core.ParseAddress(addrStr)
	if !ok {
		return nil, nil, fmt.Errorf("%w: %q is not a valid address", ErrRefNotFound, addrStr)
	}

	neighbor := core.Open(ledgerDir)
	if !neighbor.Exists() {
		return nil, nil, fmt.Errorf("%w: %s", ErrNeighborLedgerMissing, ledgerDir)
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
		return nil, nil, fmt.Errorf("%w: %s not recorded in neighbor ledger %s", ErrCrossStackAddressNotFound, addr, ledgerDir)
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
		LedgerDir:    ledgerDir,
	}}, nil
}
