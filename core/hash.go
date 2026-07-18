package core

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
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

var hashFormat = regexp.MustCompile(`^[0-9a-f]{64}$`)

// IsHash reports whether s has the shape of a well-formed proposal/apply
// hash (64 lowercase hex characters) -- exported so a LedgerStore
// implementation living outside this package (e.g. a gocloud.dev/blob-
// backed one in the sibling ledgerstore package, which core stays
// dependency-free of) can validate a head-pointer object's own content
// without duplicating this pattern.
func IsHash(s string) bool { return hashFormat.MatchString(s) }
