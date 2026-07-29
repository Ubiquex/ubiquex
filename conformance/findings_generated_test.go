package conformance

import "testing"

// TestGeneratedFindings_WellFormed is a fast, hermetic (no network) sanity
// check on the committed findings_generated.go — every entry names one
// of the five currently-pinned providers, a real FindingClass/Tier/
// Confidence value (compile-time enforced by using the named constants
// directly in probegen, but checked again here as a real regression
// guard against a future hand-edit of the generated file, which the
// file's own header explicitly says never to do), and a non-empty
// Detail. Never re-acquires a real schema -- that's probegen's own job,
// run manually; this test only inspects the already-committed data.
func TestGeneratedFindings_WellFormed(t *testing.T) {
	if len(GeneratedFindings) == 0 {
		t.Fatal("GeneratedFindings is empty -- probegen was never run, or ran against zero providers")
	}
	validClass := map[FindingClass]bool{
		FindingIncompleteRead: true, FindingSensitiveUnderflag: true,
		FindingDestroyLie: true, FindingUndriftable: true,
	}
	validTier := map[Tier]bool{TierHermetic: true, TierLive: true}
	validConfidence := map[Confidence]bool{Candidate: true, Confirmed: true}

	seenSources := map[string]bool{}
	for i, f := range GeneratedFindings {
		if f.Source == "" || f.Type == "" || f.Verb == "" {
			t.Fatalf("entry %d: missing required field: %+v", i, f)
		}
		if !validClass[f.Class] {
			t.Fatalf("entry %d: unrecognized Class %q", i, f.Class)
		}
		if !validTier[f.Tier] {
			t.Fatalf("entry %d: unrecognized Tier %q", i, f.Tier)
		}
		if !validConfidence[f.Confidence] {
			t.Fatalf("entry %d: unrecognized Confidence %q", i, f.Confidence)
		}
		if f.Detail == "" {
			t.Fatalf("entry %d: empty Detail: %+v", i, f)
		}
		if _, ok := PinnedProviderVersions[f.Source]; !ok {
			t.Fatalf("entry %d: Source %q not in PinnedProviderVersions", i, f.Source)
		}
		if f.Version != PinnedProviderVersions[f.Source] {
			t.Fatalf("entry %d: Version %q doesn't match PinnedProviderVersions[%q] = %q -- stale generated file, re-run probegen", i, f.Version, f.Source, PinnedProviderVersions[f.Source])
		}
		seenSources[f.Source] = true
	}
	if len(seenSources) != len(PinnedProviderVersions) {
		t.Errorf("GeneratedFindings only covers %d of %d pinned providers: %v", len(seenSources), len(PinnedProviderVersions), seenSources)
	}
}

// TestAllVerdicts_LayersGeneratedFindingsUnderRegistry proves AllVerdicts
// is genuinely the base+overlay combination, not just GeneratedFindings
// renamed: every verdict whose (Source, Type) matches a real
// conformance.Registry entry must carry that entry as its own Spec
// (never nil, never a different one) -- the layering mechanism itself
// (Contradictions, grouping) is already covered by layer_test.go's own
// dedicated tests against hand-built fixtures; this test's only job is
// confirming the wiring from the real, committed GeneratedFindings data
// actually flows through LayerFindings correctly.
func TestAllVerdicts_LayersGeneratedFindingsUnderRegistry(t *testing.T) {
	verdicts := AllVerdicts()
	if len(verdicts) == 0 {
		t.Fatal("AllVerdicts() returned nothing")
	}
	checked := 0
	for _, v := range verdicts {
		spec := ByType(v.Source, v.Type)
		if spec == nil {
			continue // no hand-verified entry for this type -- correctly nil, nothing more to check
		}
		checked++
		if v.Spec == nil {
			t.Fatalf("%s/%s has a real Registry entry (%+v) but LayeredVerdict.Spec is nil", v.Source, v.Type, spec)
		}
		if v.Spec != spec {
			t.Fatalf("%s/%s: LayeredVerdict.Spec points at a different Registry entry than ByType itself returns", v.Source, v.Type)
		}
	}
	if checked == 0 {
		t.Fatal("no generated finding matched any hand-verified Registry entry at all -- the layering wiring may be broken (Source/Type mismatch), not just \"every hand-verified type happens to be clean\"")
	}
}
