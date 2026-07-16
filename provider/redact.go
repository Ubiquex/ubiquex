package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
// salt should be stable for the lifetime of a ledger directory (see
// core.Ledger.Salt) -- the same real value always redacts to the same
// hash given the same salt, which is what lets drift detection keep
// working over redacted attributes (unchanged -> same hash -> no drift;
// changed -> different hash -> drift fires).
func Redact(block Block, salt []byte, observed json.RawMessage) (json.RawMessage, error) {
	var decoded map[string]interface{}
	if err := json.Unmarshal(observed, &decoded); err != nil {
		return nil, fmt.Errorf("redact: decode observed state: %w", err)
	}
	if err := redactBlock(block, salt, decoded); err != nil {
		return nil, fmt.Errorf("redact: %w", err)
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("redact: %w", err)
	}
	return out, nil
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
