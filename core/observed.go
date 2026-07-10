package core

import (
	"encoding/json"
	"fmt"
)

// ObservedHash computes a stable content fingerprint of a provider's
// ReadResource result: SHA-256 of canonical (sorted-key) JSON.
//
// This is deliberately NOT the same pipeline as Hash (proposal hashing):
// observed state is real API data, not resolver-authored proposal content,
// so docs/schema.md's float-rejection rule doesn't apply to it — a cloud
// API is free to return e.g. a fractional autoscaling target, and refusing
// to fingerprint that would make drift detection unusable for any resource
// with a numeric attribute. Decoding through plain float64 (rather than
// json.Number/int64) means the same bytes always re-encode to the same
// canonical form, which is all a fingerprint needs — it doesn't need to
// preserve the original textual number formatting.
func ObservedHash(state json.RawMessage) (string, error) {
	var generic interface{}
	if err := json.Unmarshal(state, &generic); err != nil {
		return "", fmt.Errorf("observed state: %w", err)
	}
	canon, err := json.Marshal(generic) // encoding/json sorts map keys at every level
	if err != nil {
		return "", fmt.Errorf("observed state: %w", err)
	}
	return sha256Hex(canon), nil
}
