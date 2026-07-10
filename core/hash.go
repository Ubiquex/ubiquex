package core

import (
	"crypto/sha256"
	"encoding/hex"
)

// sha256Hex returns the lowercase hex-encoded SHA-256 digest of b. Proposal
// IDs are this digest in full (64 hex chars) — no truncation — since a
// short display form is explicitly a presentation concern (docs/schema.md:
// "id is a content hash... short form for display"), not part of the
// canonical identity itself.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
