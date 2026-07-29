package conformance

import (
	"sort"
	"strings"
)

// LayeredVerdict is one (source, type)'s FULL picture — whatever the
// generator found (Findings) layered alongside whatever a human already
// knows (Spec, the matching hand-written conformance.Registry entry, if
// one exists) — docs/conformance-harness.md's own "Layering, never
// replacing": Spec is NEVER overwritten by Findings, and a real
// disagreement between the two is named in Contradictions, not silently
// resolved either way. A LayeredVerdict with Spec == nil is a type this
// project has never hand-verified at all — exactly the "default-
// assumption silence" the design doc's own registry-format section says
// this arc replaces: it still gets a real entry here, machine-only.
type LayeredVerdict struct {
	Source         string
	Type           string
	Spec           *TypeSpec // nil if conformance.Registry has no hand-written entry for this (source, type) at all
	Findings       []Finding // this type's own subset of a ProbeSchema/ProbeDestroyHonesty run; empty means clean per whatever tier ran
	Contradictions []string
}

// LayerFindings groups findings by (Source, Type) and layers each group
// against conformance.Registry's own matching TypeSpec (ByType), if one
// exists. Purely additive: never mutates Registry, never drops a
// Finding, never invents a verdict Registry didn't already assert.
// Sorted by (Source, Type) for the same reproducibility ProbeSchema's
// own output already guarantees — two runs against the identical
// findings slice produce byte-identical output.
func LayerFindings(findings []Finding) []LayeredVerdict {
	type key struct{ source, typ string }
	grouped := map[key][]Finding{}
	var order []key
	for _, f := range findings {
		k := key{f.Source, f.Type}
		if _, ok := grouped[k]; !ok {
			order = append(order, k)
		}
		grouped[k] = append(grouped[k], f)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].source != order[j].source {
			return order[i].source < order[j].source
		}
		return order[i].typ < order[j].typ
	})

	verdicts := make([]LayeredVerdict, 0, len(order))
	for _, k := range order {
		spec := ByType(k.source, k.typ)
		verdicts = append(verdicts, LayeredVerdict{
			Source:         k.source,
			Type:           k.typ,
			Spec:           spec,
			Findings:       grouped[k],
			Contradictions: detectContradictions(spec, grouped[k]),
		})
	}
	return verdicts
}

// detectContradictions names real, honest disagreements between a
// hand-verified TypeSpec and the generator's own findings for the same
// type — never auto-resolved either direction, per
// docs/conformance-harness.md's own "a machine verdict that contradicts
// an existing hand-verified Notes entry is a flagged discrepancy for a
// human session to resolve... never an automatic overwrite" rule. Two
// real checks, both grounded in what a contradiction here would
// actually mean:
//
//  1. spec's own hand-recorded IdentityFields (real, once-verified
//     attribute names) no longer exist in the CURRENT schema at all
//     (a Confirmed incomplete-read finding) — the most likely honest
//     explanation is the provider changed shape across a version bump
//     since the entry was hand-verified, not that the human was wrong;
//     exactly the signal "rerun on version bump" (docs/
//     conformance-harness.md) exists to catch, surfaced here even
//     before that machinery is built.
//  2. spec is RealSafe and Implemented (a real account already
//     confirmed this type works) but the generator flagged ANY
//     incomplete-read concern for it — worth a re-check against the
//     CURRENT provider version, never assumed still accurate just
//     because it was once true.
func detectContradictions(spec *TypeSpec, findings []Finding) []string {
	if spec == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, f := range findings {
		if f.Class != FindingIncompleteRead {
			continue
		}
		if f.Confidence == Confirmed && spec.Implemented && len(spec.IdentityFields) > 0 {
			add("hand-verified IdentityFields " + strings.Join(spec.IdentityFields, "/") +
				" are recorded for this type, but the CURRENT schema has no recognized identity attribute at all -- possible provider version drift since this entry was hand-verified, or the schema genuinely changed shape; needs a human session to reconcile, not auto-resolved")
		}
		if spec.Safety == RealSafe && spec.Implemented {
			add("this type is hand-verified RealSafe (a real account already confirmed it works), but the generator flagged an incomplete-read concern for the CURRENT schema -- worth a re-check against the current provider version, not assumed still accurate")
		}
	}
	return out
}
