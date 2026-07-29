package core

import "encoding/json"

// RedactedMarkerKey is the JSON object key UBI-23's redaction convention
// uses to mark a sensitive attribute value that's been replaced with a
// salted fingerprint rather than its real material (docs/schema.md --
// $redacted value encoding; docs/architecture.md -- Secrets). core and
// provider don't import each other -- this string is the wire-format
// convention both independently conform to, the same pattern
// IntentSource.Kind string literals already establish across
// audit/cloudtrail, audit/gcp, core.
const RedactedMarkerKey = "$redacted"

// isRedactedMarker reports whether a decoded-generic JSON object is
// exactly {"$redacted": {...}} -- provider.Redact's marker shape. Any
// other shape (additional keys, a non-object value under the key) is not
// treated as redacted.
func isRedactedMarker(v map[string]interface{}) bool {
	if len(v) != 1 {
		return false
	}
	inner, ok := v[RedactedMarkerKey]
	if !ok {
		return false
	}
	_, ok = inner.(map[string]interface{})
	return ok
}

// IsRedactedValue reports whether raw decodes to exactly
// {"$redacted": {...}} -- used by rendering/write-back code (cli, tfwrite)
// that needs to recognize a redacted attribute value without recursing
// into it or ever printing/writing its contents.
func IsRedactedValue(raw json.RawMessage) bool {
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	return isRedactedMarker(m)
}

// CountRedacted counts how many $redacted markers appear anywhere within
// raw's decoded JSON tree -- used by bulk onboarding (ubx scan --all) to
// report how many attributes were redacted across a batch. A marker is
// atomic: once found, its own contents aren't descended into further.
func CountRedacted(raw json.RawMessage) int {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0
	}
	return countRedactedValue(v)
}

func countRedactedValue(v interface{}) int {
	switch t := v.(type) {
	case map[string]interface{}:
		if isRedactedMarker(t) {
			return 1
		}
		n := 0
		for _, vv := range t {
			n += countRedactedValue(vv)
		}
		return n
	case []interface{}:
		n := 0
		for _, vv := range t {
			n += countRedactedValue(vv)
		}
		return n
	default:
		return 0
	}
}
