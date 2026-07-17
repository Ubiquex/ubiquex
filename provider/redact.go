package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// redactedMarkerKey matches core.RedactedMarkerKey (docs/schema.md --
// $redacted value encoding, UBI-23). provider and core don't import each
// other; this string is the wire-format convention both independently
// conform to, the same pattern IntentSource.Kind string literals already
// establish across cloudtrail/gcpaudit/core.
const redactedMarkerKey = "$redacted"

// Redact walks block's Sensitive flags over observed (a resource's
// decoded live state, as returned by Provider.ReadResource) and replaces
// each flagged attribute's whole value with a salted fingerprint --
// {"$redacted": {"sha256": "<hex>"}} -- before it's handed back to core
// (docs/architecture.md -- Secrets). core never sees real sensitive
// material; it only ever sees the resulting JSON shape.
//
// UBI-24: redaction is the UNION of the provider's own schema flags
// (via block, walked as before) and SensitiveOverrides, a small
// ubx-owned table keyed by (source, typeName) naming additional
// attribute paths to redact regardless of what the schema says -- the
// provider's schema is a floor, never a ceiling. An override can only
// ADD an attribute to what gets redacted; nothing can remove one the
// schema itself flags Sensitive.
//
// salt should be stable for the lifetime of a ledger directory (see
// core.Ledger.Salt) -- the same real value always redacts to the same
// hash given the same salt, which is what lets drift detection keep
// working over redacted attributes (unchanged -> same hash -> no drift;
// changed -> different hash -> drift fires).
func Redact(source, typeName string, block Block, salt []byte, observed json.RawMessage) (json.RawMessage, error) {
	var decoded map[string]interface{}
	if err := json.Unmarshal(observed, &decoded); err != nil {
		return nil, fmt.Errorf("redact: decode observed state: %w", err)
	}
	if err := redactBlock(block, salt, decoded); err != nil {
		return nil, fmt.Errorf("redact: %w", err)
	}
	for _, path := range OverridePathsFor(source, typeName) {
		if err := redactOverridePath(decoded, strings.Split(path, "."), salt); err != nil {
			return nil, fmt.Errorf("redact: override %s: %w", path, err)
		}
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("redact: %w", err)
	}
	return out, nil
}

// redactOverridePath walks container by path, redacting whatever it
// finds at the end in place -- independent of provider.Block/Attribute
// entirely, since an override exists precisely for an attribute the
// schema can't (or doesn't) mark Sensitive (docs/architecture.md's
// "Sensitive overrides"). container must be a map[string]interface{} or
// a []interface{} (json.Unmarshal's own decoded-generic shapes).
//
// If the current node is a JSON array (a NestingList/NestingSet block,
// or -- as with helm_release's own metadata -- a compound
// list(object(...))-typed attribute; this function doesn't distinguish
// the two, since both decode to the same JSON shape), the SAME
// (unconsumed) remaining path is applied to every element, matching
// Redact's own List/Set handling generalized to an arbitrary path depth
// rather than one fixed nesting level.
//
// A missing intermediate key, or a path segment that doesn't match the
// node's actual shape, is a silent no-op -- an override path that
// doesn't apply to a given observed state (e.g. a field the schema
// itself omitted this time) is not an error. A value already redacted
// (by the schema-driven pass above) is left alone rather than re-hashed
// -- overrides only ever ADD redaction, they never need to re-apply it.
func redactOverridePath(container interface{}, path []string, salt []byte) error {
	if len(path) == 0 {
		return nil
	}
	switch c := container.(type) {
	case map[string]interface{}:
		key := path[0]
		val, ok := c[key]
		if !ok || val == nil {
			return nil
		}
		if len(path) > 1 {
			return redactOverridePath(val, path[1:], salt)
		}
		if isRedactedMarker(val) {
			return nil
		}
		redacted, err := redactValue(salt, val)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		c[key] = redacted
		return nil
	case []interface{}:
		for _, item := range c {
			if err := redactOverridePath(item, path, salt); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// isRedactedMarker reports whether v is already a {"$redacted": {...}}
// object -- mirrors core.IsRedactedValue's own shape check (provider and
// core don't import each other; this is the same wire-format convention
// both independently conform to).
func isRedactedMarker(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	if !ok || len(m) != 1 {
		return false
	}
	inner, ok := m[redactedMarkerKey]
	if !ok {
		return false
	}
	_, ok = inner.(map[string]interface{})
	return ok
}

// redactBlock redacts m in place, per block's own Attributes/NestedBlocks.
func redactBlock(block Block, salt []byte, m map[string]interface{}) error {
	for _, a := range block.Attributes {
		if !a.Sensitive {
			continue
		}
		v, ok := m[a.Name]
		if !ok || v == nil {
			continue
		}
		redacted, err := redactValue(salt, v)
		if err != nil {
			return fmt.Errorf("%s: %w", a.Name, err)
		}
		m[a.Name] = redacted
	}

	for _, nb := range block.NestedBlocks {
		v, ok := m[nb.TypeName]
		if !ok || v == nil {
			continue
		}
		if err := redactNested(nb, salt, v); err != nil {
			return fmt.Errorf("%s: %w", nb.TypeName, err)
		}
	}
	return nil
}

// redactNested applies nb's own Block recursively over v, shaped per
// nb.Nesting -- Single/Group is a bare object, List/Set is an array of
// objects, Map is an object keyed by string each holding a nested object
// (matching ctyvalue.go's blockObjectType, the same shapes real config
// encode/decode already uses).
func redactNested(nb NestedBlock, salt []byte, v interface{}) error {
	switch nb.Nesting {
	case NestingSingle, NestingGroup:
		obj, ok := v.(map[string]interface{})
		if !ok {
			return nil
		}
		return redactBlock(nb.Block, salt, obj)
	case NestingList, NestingSet:
		arr, ok := v.([]interface{})
		if !ok {
			return nil
		}
		for _, item := range arr {
			obj, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if err := redactBlock(nb.Block, salt, obj); err != nil {
				return err
			}
		}
		return nil
	case NestingMap:
		objMap, ok := v.(map[string]interface{})
		if !ok {
			return nil
		}
		for _, item := range objMap {
			obj, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if err := redactBlock(nb.Block, salt, obj); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// redactValue builds the {"$redacted": {"sha256": "<hex>"}} marker for v.
// v is already decoded-generic (from Redact's own json.Unmarshal), so
// re-marshaling it is already canonical -- sorted object keys, no
// insignificant whitespace, the same convention core.ObservedHash uses.
func redactValue(salt []byte, v interface{}) (map[string]interface{}, error) {
	canon, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write(salt)
	h.Write(canon)
	return map[string]interface{}{
		redactedMarkerKey: map[string]interface{}{
			"sha256": hex.EncodeToString(h.Sum(nil)),
		},
	}, nil
}
