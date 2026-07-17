package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// hashDomainPrefix is prepended to canonical bytes before hashing
// (docs/schema.md — Canonical hashing, RATIFIED v1: domain separation).
const hashDomainPrefix = "ubx:proposal:v1\n"

// excludedHashFields are the exact fields excluded from hashed content per
// docs/schema.md §Canonical hashing (RATIFIED v1). Nothing else is excluded.
var excludedHashFields = []string{"id", "acceptance", "status"}

// ErrFloatRejected means a JSON float literal (or an integer literal too
// large for int64) was found in hashed content. docs/schema.md's ratified
// rule requires every number to be either an int64-representable JSON
// integer or a decimal value encoded as a JSON string — never a float
// literal — precisely to avoid needing to canonicalize float formatting
// (trailing zeros, exponents, -0, NaN/Inf) at all.
var ErrFloatRejected = errors.New("floating-point number not allowed in hashed content")

// Hash computes a Proposal's content hash per docs/schema.md's ratified
// canonical hashing rules: domain-prefixed SHA-256 over canonical
// (RFC 8785/JCS-style) JSON of the proposal minus id/acceptance/status,
// with delta arrays sorted by (stack, type, name). The canonicalization
// runs through DoubleRun, so any nondeterminism in it is a hard failure
// here rather than a silently unstable hash.
func Hash(p *Proposal) (string, error) {
	canon, err := DoubleRun(func() ([]byte, error) {
		return canonicalProposalBytes(p)
	})
	if err != nil {
		return "", err
	}
	return sha256Hex(append([]byte(hashDomainPrefix), canon...)), nil
}

// canonicalProposalBytes produces the canonical JSON encoding of a
// Proposal's hashed content.
func canonicalProposalBytes(p *Proposal) ([]byte, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal proposal: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic map[string]interface{}
	if err := dec.Decode(&generic); err != nil {
		return nil, fmt.Errorf("decode proposal: %w", err)
	}

	for _, f := range excludedHashFields {
		delete(generic, f)
	}

	if delta, ok := generic["delta"].(map[string]interface{}); ok {
		for _, field := range [...]string{"creates", "modifies", "destroys"} {
			if arr, ok := delta[field].([]interface{}); ok {
				sortDeltaElements(arr)
			}
		}
	}

	canon, err := canonicalizeNumbers(generic)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(canon); err != nil {
		return nil, fmt.Errorf("encode canonical proposal: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// canonicalizeNumbers walks a generic JSON value (as decoded with
// json.Decoder.UseNumber) and enforces the number rule everywhere in the
// tree, returning an equivalent tree where every JSON object is a
// map[string]interface{} (encoding/json.Marshal sorts map keys, at every
// nesting level, which is what makes this produce RFC 8785/JCS-style
// sorted-key output with a single top-level Marshal/Encode call) and every
// number is a plain int64.
func canonicalizeNumbers(v interface{}) (interface{}, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, vv := range val {
			cv, err := canonicalizeNumbers(vv)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = cv
		}
		return out, nil
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, vv := range val {
			cv, err := canonicalizeNumbers(vv)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = cv
		}
		return out, nil
	case json.Number:
		return canonicalizeNumber(val)
	case float64:
		// Only reachable if a caller builds a tree by hand rather than via
		// UseNumber decoding — guard against it rather than silently
		// accepting a float that slipped through.
		return nil, fmt.Errorf("%w: %v", ErrFloatRejected, val)
	default:
		return v, nil // string, bool, nil pass through unchanged
	}
}

func canonicalizeNumber(n json.Number) (interface{}, error) {
	s := n.String()
	if strings.ContainsAny(s, ".eE") {
		return nil, fmt.Errorf("%w: %s (use an int64 value, or an explicit JSON string like %q for a decimal)",
			ErrFloatRejected, s, s)
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: %s does not fit in int64", ErrFloatRejected, s)
	}
	return i, nil
}

// sortDeltaElements sorts a decoded delta.creates/modifies/destroys array
// in place, lexicographically by (stack, type, name) — docs/schema.md's
// ratified rule, which supersedes the earlier draft's dependency-order
// requirement (dependency order is an executor planning concern, not a
// hashing concern).
func sortDeltaElements(arr []interface{}) {
	sort.SliceStable(arr, func(i, j int) bool {
		si, ti, ni := deltaSortKey(arr[i])
		sj, tj, nj := deltaSortKey(arr[j])
		if si != sj {
			return si < sj
		}
		if ti != tj {
			return ti < tj
		}
		return ni < nj
	})
}

// deltaSortKey extracts a (stack, type, name) sort key from one delta
// array element, per docs/schema.md's pinned shapes: Creates elements are
// IR resource nodes with stack/type/name as direct fields; Modifies
// elements are {target: Address, before, after}, so the key comes from the
// nested target object. Destroys elements were bare Address objects
// (stack/type/name direct) in the original 2026-07-10 pin; re-pinned
// 2026-07-17 (docs/schema.md — "Amendment: destroys", UBI-30,
// SchemaVersion 2) to DestroyEntry{address, state, depends_on}, so the key
// now comes from the nested address object instead — the same "look for a
// nested object under a known key" shape Modifies' own target already
// established, not a new pattern. All three shapes are now pinned, so this
// no longer needs to guess — a map lacking these fields, or a non-object
// element, is a proposal that doesn't conform to the pinned shape and sorts
// as if empty (a hash produced from an unpinned/malformed shape is
// something wrong upstream, not something to interpret around here; schema
// validation is Validate's job, not the canonicalizer's).
func deltaSortKey(el interface{}) (stack, typ, name string) {
	obj, ok := el.(map[string]interface{})
	if !ok {
		return "", "", ""
	}
	if target, ok := obj["target"].(map[string]interface{}); ok {
		obj = target
	} else if address, ok := obj["address"].(map[string]interface{}); ok {
		obj = address
	}
	stack, _ = obj["stack"].(string)
	typ, _ = obj["type"].(string)
	name, _ = obj["name"].(string)
	return stack, typ, name
}
