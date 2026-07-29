package conformance

import "testing"

func TestLayerFindings_NoRegistryEntry_SpecNil(t *testing.T) {
	findings := []Finding{
		{Source: "hashicorp/aws", Type: "aws_totally_made_up", Class: FindingSensitiveUnderflag, Tier: TierHermetic, Confidence: Candidate},
	}
	got := LayerFindings(findings)
	if len(got) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(got))
	}
	if got[0].Spec != nil {
		t.Fatalf("Spec = %+v, want nil -- no registry entry exists for this type", got[0].Spec)
	}
	if len(got[0].Findings) != 1 {
		t.Fatalf("got %d findings in verdict, want 1", len(got[0].Findings))
	}
	if got[0].Contradictions != nil {
		t.Fatalf("Contradictions = %+v, want nil -- nothing to contradict without a Spec", got[0].Contradictions)
	}
}

func TestLayerFindings_KnownCleanType_NoContradiction(t *testing.T) {
	// aws_iam_policy is a real, hand-verified RealSafe registry entry
	// (IdentityFields: id/arn). A finding for a DIFFERENT class (not
	// incomplete-read) must never trigger a contradiction.
	findings := []Finding{
		{Source: "hashicorp/aws", Type: "aws_iam_policy", Class: FindingSensitiveUnderflag, Tier: TierHermetic, Confidence: Candidate},
	}
	got := LayerFindings(findings)
	if len(got) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(got))
	}
	v := got[0]
	if v.Spec == nil {
		t.Fatal("Spec = nil, want the real aws_iam_policy registry entry")
	}
	if v.Spec.Safety != RealSafe || !v.Spec.Implemented {
		t.Fatalf("unexpected Spec: %+v", v.Spec)
	}
	if v.Contradictions != nil {
		t.Fatalf("Contradictions = %+v, want nil", v.Contradictions)
	}
}

func TestLayerFindings_ConfirmedIncompleteRead_VsHandVerifiedIdentityFields_Contradiction(t *testing.T) {
	// Synthetic: aws_iam_policy really is hand-verified with
	// IdentityFields id/arn -- if the generator ever claimed Confirmed
	// incomplete-read for it (meaning the CURRENT schema has neither),
	// that's a genuine, real disagreement worth flagging, never silently
	// resolved either way.
	findings := []Finding{
		{Source: "hashicorp/aws", Type: "aws_iam_policy", Class: FindingIncompleteRead, Tier: TierHermetic, Confidence: Confirmed},
	}
	got := LayerFindings(findings)
	if len(got) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(got))
	}
	if len(got[0].Contradictions) == 0 {
		t.Fatal("expected at least one contradiction")
	}
}

func TestLayerFindings_RealSafeType_IncompleteReadCandidate_Contradiction(t *testing.T) {
	findings := []Finding{
		{Source: "hashicorp/aws", Type: "aws_iam_policy", Class: FindingIncompleteRead, Tier: TierHermetic, Confidence: Candidate},
	}
	got := LayerFindings(findings)
	if len(got[0].Contradictions) == 0 {
		t.Fatal("expected a contradiction: a RealSafe, hand-verified type shouldn't produce ANY incomplete-read concern")
	}
}

func TestLayerFindings_FakeOnlyType_IncompleteRead_NoContradiction(t *testing.T) {
	// A FakeOnly (never live-verified) registry entry disagreeing with
	// the generator isn't a contradiction at all -- there's no live
	// claim to contradict.
	findings := []Finding{
		{Source: "hashicorp/google", Type: "google_compute_instance", Class: FindingIncompleteRead, Tier: TierHermetic, Confidence: Confirmed},
	}
	got := LayerFindings(findings)
	if len(got) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(got))
	}
	if got[0].Spec == nil || got[0].Spec.Safety != FakeOnly {
		t.Fatalf("expected google_compute_instance to be a real, FakeOnly registry entry, got: %+v", got[0].Spec)
	}
	if got[0].Contradictions != nil {
		t.Fatalf("Contradictions = %+v, want nil -- a FakeOnly entry makes no live claim to contradict", got[0].Contradictions)
	}
}

func TestLayerFindings_Deterministic(t *testing.T) {
	findings := []Finding{
		{Source: "hashicorp/aws", Type: "aws_zzz", Class: FindingUndriftable, Tier: TierHermetic, Confidence: Candidate},
		{Source: "hashicorp/aws", Type: "aws_aaa", Class: FindingUndriftable, Tier: TierHermetic, Confidence: Candidate},
	}
	got1 := LayerFindings(findings)
	got2 := LayerFindings(findings)
	if len(got1) != 2 || len(got2) != 2 {
		t.Fatalf("got %d and %d verdicts, want 2 each", len(got1), len(got2))
	}
	if got1[0].Type != "aws_aaa" {
		t.Fatalf("first verdict's Type = %q, want \"aws_aaa\" (sorted first)", got1[0].Type)
	}
	for i := range got1 {
		if got1[i].Source != got2[i].Source || got1[i].Type != got2[i].Type {
			t.Fatalf("non-deterministic order at index %d", i)
		}
	}
}

func TestLayerFindings_MultipleFindingsSameType_Grouped(t *testing.T) {
	findings := []Finding{
		{Source: "hashicorp/aws", Type: "aws_widget", Class: FindingSensitiveUnderflag, Tier: TierHermetic, Confidence: Candidate, Detail: "a"},
		{Source: "hashicorp/aws", Type: "aws_widget", Class: FindingSensitiveUnderflag, Tier: TierHermetic, Confidence: Candidate, Detail: "b"},
	}
	got := LayerFindings(findings)
	if len(got) != 1 {
		t.Fatalf("got %d verdicts, want 1 (both findings share the same Source/Type)", len(got))
	}
	if len(got[0].Findings) != 2 {
		t.Fatalf("got %d findings in the verdict, want 2", len(got[0].Findings))
	}
}
