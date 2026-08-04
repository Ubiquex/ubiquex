package blueprint

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Verify recomputes dir's own content hash from its current files
// (hashFiles/buildManifest, manifest.go) and confirms it matches
// blueprint.lock.json's own declared content_hash -- the same tamper-
// evidence principle core.Hash/core.Validate already give a Proposal,
// applied here to a pulled blueprint's own files instead of ledger
// content (docs/blueprint.md). Returns the declared Manifest on success;
// a mismatch is always an error, never a partial/best-effort result.
func Verify(dir string) (*Manifest, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("blueprint verify: %w", err)
	}
	declared, err := readManifest(absDir)
	if err != nil {
		return nil, fmt.Errorf("blueprint verify: %w", err)
	}

	// Recomputed against declared.Name, not filepath.Base(absDir): the
	// content hash must stay invariant under "which directory did you
	// pull this into" -- a git clone or local copy very plausibly lands
	// somewhere named differently than the blueprint's own build-time
	// name, exactly like Slice 2's own $ref/stack-name finding
	// (docs/blueprint.md) already established for a different field.
	recomputed, err := buildManifest(absDir, declared.Name)
	if err != nil {
		return nil, fmt.Errorf("blueprint verify: %w", err)
	}

	if recomputed.ContentHash != declared.ContentHash {
		return nil, fmt.Errorf("blueprint verify: content hash mismatch for %q at %s\n  declared:   %s\n  recomputed: %s\n%s",
			declared.Name, absDir, declared.ContentHash, recomputed.ContentHash, diffFiles(declared.Files, recomputed.Files))
	}
	return declared, nil
}

// diffFiles renders a human-readable summary of which files differ
// between a manifest's own declared file hashes and a freshly recomputed
// set -- Verify's own mismatch error uses this so a tampered/corrupted
// pull names exactly what changed, not just "hashes differ somewhere."
func diffFiles(declared, recomputed map[string]string) string {
	names := map[string]bool{}
	for k := range declared {
		names[k] = true
	}
	for k := range recomputed {
		names[k] = true
	}
	sorted := make([]string, 0, len(names))
	for k := range names {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	var b strings.Builder
	for _, k := range sorted {
		d, dok := declared[k]
		r, rok := recomputed[k]
		switch {
		case dok && !rok:
			fmt.Fprintf(&b, "  - %s (declared, missing from this copy)\n", k)
		case !dok && rok:
			fmt.Fprintf(&b, "  + %s (present here, not in the declared manifest)\n", k)
		case d != r:
			fmt.Fprintf(&b, "  ~ %s (content changed)\n", k)
		}
	}
	if b.Len() == 0 {
		return "  (every declared file's own content hash still matches -- the mismatch is in the manifest's declared name)\n"
	}
	return b.String()
}
