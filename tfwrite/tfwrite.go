// Package tfwrite is UBI-11 stage 2's ".tf write-back": correcting an
// existing Terraform resource block's literal attribute values to match an
// accepted drift_adopt proposal's recorded reality — narrow-scope and
// surgical by design (see docs/architecture.md's Decision loop section):
//
//   - In scope: overwriting a literal attribute value (string, number,
//     bool, or a literal map/list of those), including nested paths like
//     "tags.hotfix", on a resource block that already exists. Also in
//     scope, since it's the single most common real drift shape (UBI-11
//     stage 2 follow-up): inserting a brand-new key into an existing
//     literal map/object attribute — someone tagging a resource in the
//     console with a key the .tf file never had — matching that object's
//     own indentation and trailing-comma style (see insert.go).
//   - Never: creating a new top-level attribute that doesn't exist at
//     all, creating/deleting/reordering blocks, reformatting a file, or
//     touching anything besides the specific drifted attribute/key.
//   - Declines (never guesses) when the current value — or, for an
//     insertion, the *parent* object being inserted into — is itself an
//     expression (a variable reference, function call, or interpolation)
//     rather than a literal, rather than silently severing it from
//     whatever it referred to.
//
// The surgical part is done with exact byte-range replacement: hclsyntax
// parses the file to find the precise byte range of the one sub-expression
// being changed (or, for an insertion, the exact byte position to splice
// new bytes into), and only that range/position is ever touched — not
// hclwrite's higher-level Body.SetAttributeValue, which regenerates an
// entire attribute's tokens and would reformat/lose comments on anything
// with internal structure (an object or list literal). hclwrite is still
// used, for exactly one thing: rendering a literal value's tokens
// (TokensForValue), so quoting/formatting for new content matches HCL
// conventions.
package tfwrite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ubiquex/ubiquex-cli/core"
)

var (
	// ErrResourceBlockNotFound means addr's resource block doesn't appear
	// at all in the file(s) searched.
	ErrResourceBlockNotFound = errors.New("no matching resource block found")

	// ErrMultipleResourceBlocks means addr matched more than one resource
	// block — across one file or several — and write-back refuses to
	// guess which one is the real target.
	ErrMultipleResourceBlocks = errors.New("more than one matching resource block found")
)

// DeclinedAttribute records one Modification.After dot-path write-back
// could not safely apply, and why — the "manual-reconciliation report"
// docs/architecture.md describes. Expression is the offending value's
// current raw source text, when there is one to show (empty if the
// attribute/key is simply absent rather than present-but-unsafe).
type DeclinedAttribute struct {
	Path       string
	Reason     string
	Expression string
}

// Report is what ApplyModification/FindAndApply actually did — Applied and
// Declined are both sorted by Path for stable, readable output.
type Report struct {
	Applied  []string
	Declined []DeclinedAttribute
}

// pendingEdit is one attribute's resolved, about-to-happen byte
// replacement — gathered from a single parse of the file before any edits
// are made, then applied in reverse byte order so earlier edits (later in
// the loop) never invalidate the byte ranges of edits still to come.
type pendingEdit struct {
	path        string
	rng         hcl.Range
	replacement []byte
}

// ApplyModification rewrites src (one .tf file's content), applying every
// dot-path in mod.After that can be safely written. addr's resource block
// must appear exactly once in src (ErrResourceBlockNotFound /
// ErrMultipleResourceBlocks otherwise — a top-level failure, since there's
// no single block to apply anything to); each individual attribute that
// can't be safely written is recorded in the returned Report and left
// completely untouched, rather than failing the whole call — one
// unsafe/absent attribute in a multi-attribute drift shouldn't block
// writing back the others.
//
// filename is used only for HCL diagnostics; it need not be a real path.
func ApplyModification(src []byte, filename string, addr core.Address, mod core.Modification) ([]byte, *Report, error) {
	block, err := findResourceBlock(src, filename, addr)
	if err != nil {
		return nil, nil, err
	}

	paths := make([]string, 0, len(mod.After))
	for path := range mod.After {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	report := &Report{}
	var edits []pendingEdit
	for _, path := range paths {
		rng, currentExpr, err := resolveTarget(block, path)
		if err != nil {
			var ik *insertableKey
			if errors.As(err, &ik) {
				insertRng, insertBytes, ierr := planInsertion(src, ik.obj, ik.key, mod.After[path])
				if ierr != nil {
					report.Declined = append(report.Declined, DeclinedAttribute{
						Path:       path,
						Reason:     ierr.Error(),
						Expression: exprText(src, currentExpr),
					})
					continue
				}
				edits = append(edits, pendingEdit{path: path, rng: insertRng, replacement: insertBytes})
				report.Applied = append(report.Applied, path)
				continue
			}
			report.Declined = append(report.Declined, DeclinedAttribute{
				Path:       path,
				Reason:     err.Error(),
				Expression: exprText(src, currentExpr),
			})
			continue
		}
		replacement, err := renderLiteral(mod.After[path])
		if err != nil {
			report.Declined = append(report.Declined, DeclinedAttribute{
				Path:       path,
				Reason:     fmt.Sprintf("new value can't be rendered as an HCL literal: %v", err),
				Expression: exprText(src, currentExpr),
			})
			continue
		}
		edits = append(edits, pendingEdit{path: path, rng: rng, replacement: replacement})
		report.Applied = append(report.Applied, path)
	}
	sort.Strings(report.Applied)

	// Stable, not just sorted: two insertions into the same object (e.g.
	// two brand-new tag keys added in one drift) resolve to the identical
	// byte offset, and a plain sort.Slice's tie-breaking isn't guaranteed
	// -- determinism matters here the same way it does everywhere else in
	// this project (see docs/schema.md's canonical hashing rules for the
	// standing precedent), so ties keep the paths' own alphabetical order
	// (the order they were appended in, from the already-sorted paths
	// slice above).
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].rng.Start.Byte > edits[j].rng.Start.Byte })
	out := append([]byte(nil), src...)
	for _, e := range edits {
		out = spliceBytes(out, e.rng, e.replacement)
	}
	return out, report, nil
}

// FindAndApply searches every top-level *.tf file in dir (matching
// Terraform's own single-directory-per-module convention — it does not
// recurse into subdirectories) for addr's resource block, and applies mod
// there. Exactly one matching block may exist across the whole directory;
// zero, or more than one — whether two blocks in one file or one block
// each in two files — is declined at this level exactly like it would be
// within a single file.
func FindAndApply(dir string, addr core.Address, mod core.Modification) (path string, newContent []byte, report *Report, err error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil {
		return "", nil, nil, fmt.Errorf("tfwrite: glob %s: %w", dir, err)
	}

	type candidate struct {
		path string
		src  []byte
	}
	var matches []candidate
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return "", nil, nil, fmt.Errorf("tfwrite: read %s: %w", f, err)
		}
		if countResourceBlocks(src, f, addr) > 0 {
			matches = append(matches, candidate{f, src})
		}
	}

	switch len(matches) {
	case 0:
		return "", nil, nil, fmt.Errorf("tfwrite: %w: %s %q in %s", ErrResourceBlockNotFound, addr.Type, addr.Name, dir)
	case 1:
		newContent, report, err := ApplyModification(matches[0].src, matches[0].path, addr, mod)
		return matches[0].path, newContent, report, err
	default:
		var names []string
		for _, m := range matches {
			names = append(names, m.path)
		}
		return "", nil, nil, fmt.Errorf("tfwrite: %w: %s %q found in %v", ErrMultipleResourceBlocks, addr.Type, addr.Name, names)
	}
}

func parseBody(src []byte, filename string) (*hclsyntax.Body, error) {
	f, diags := hclsyntax.ParseConfig(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("tfwrite: parse %s: %w", filename, diags)
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("tfwrite: %s: unexpected body type %T", filename, f.Body)
	}
	return body, nil
}

func findResourceBlock(src []byte, filename string, addr core.Address) (*hclsyntax.Block, error) {
	body, err := parseBody(src, filename)
	if err != nil {
		return nil, err
	}
	var matches []*hclsyntax.Block
	for _, block := range body.Blocks {
		if block.Type == "resource" && len(block.Labels) == 2 &&
			block.Labels[0] == addr.Type && block.Labels[1] == addr.Name {
			matches = append(matches, block)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("tfwrite: %w: %s %q in %s", ErrResourceBlockNotFound, addr.Type, addr.Name, filename)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("tfwrite: %w: %s %q in %s (%d blocks)", ErrMultipleResourceBlocks, addr.Type, addr.Name, filename, len(matches))
	}
}

func countResourceBlocks(src []byte, filename string, addr core.Address) int {
	body, err := parseBody(src, filename)
	if err != nil {
		return 0
	}
	n := 0
	for _, block := range body.Blocks {
		if block.Type == "resource" && len(block.Labels) == 2 &&
			block.Labels[0] == addr.Type && block.Labels[1] == addr.Name {
			n++
		}
	}
	return n
}

func spliceBytes(src []byte, rng hcl.Range, replacement []byte) []byte {
	out := make([]byte, 0, len(src)-(rng.End.Byte-rng.Start.Byte)+len(replacement))
	out = append(out, src[:rng.Start.Byte]...)
	out = append(out, replacement...)
	out = append(out, src[rng.End.Byte:]...)
	return out
}

// exprText renders expr's exact current source text, for a decline
// report — "naming the attribute and the expression it can't safely
// overwrite" (docs/architecture.md). expr may be nil (the attribute/key
// simply wasn't found at all), in which case there's no text to show.
func exprText(src []byte, expr hclsyntax.Expression) string {
	if expr == nil {
		return ""
	}
	rng := expr.Range()
	return string(src[rng.Start.Byte:rng.End.Byte])
}
