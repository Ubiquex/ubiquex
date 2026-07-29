package conformance

import (
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/provider"
)

func attr(name string, opts ...func(*provider.Attribute)) provider.Attribute {
	a := provider.Attribute{Name: name}
	for _, o := range opts {
		o(&a)
	}
	return a
}

func computed(a *provider.Attribute)  { a.Computed = true }
func sensitive(a *provider.Attribute) { a.Sensitive = true }
func optional(a *provider.Attribute)  { a.Optional = true }
func required(a *provider.Attribute)  { a.Required = true }

func TestProbeIdentityShape_IDPresent_NoFinding(t *testing.T) {
	block := provider.Block{Attributes: []provider.Attribute{
		attr("id", computed),
		attr("name", optional),
	}}
	if got := probeIdentityShape("acme/widget", "1.0.0", "acme_widget", block); got != nil {
		t.Fatalf("got %+v, want nil (id present)", got)
	}
}

func TestProbeIdentityShape_OnlySelfLinkPresent_NoFinding(t *testing.T) {
	block := provider.Block{Attributes: []provider.Attribute{
		attr("self_link", computed),
	}}
	if got := probeIdentityShape("acme/widget", "1.0.0", "acme_widget", block); got != nil {
		t.Fatalf("got %+v, want nil (self_link alone is a recognized candidate)", got)
	}
}

func TestProbeIdentityShape_SuffixMatchOnly_Candidate(t *testing.T) {
	// Real AWS shape, found by triaging hashicorp/aws 6.54.0's own real
	// "Confirmed" results this session: no canonical id/arn/name, but a
	// type-prefixed "guardrail_arn" is a plausible identity candidate --
	// weaker evidence than an exact canonical name, so Candidate, not
	// Confirmed, but NOT the same as zero candidates at all either.
	block := provider.Block{Attributes: []provider.Attribute{
		attr("guardrail_arn", computed),
		attr("version", optional),
	}}
	got := probeIdentityShape("hashicorp/aws", "6.54.0", "aws_bedrock_guardrail_version", block)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	f := got[0]
	if f.Class != FindingIncompleteRead || f.Tier != TierHermetic || f.Confidence != Candidate {
		t.Fatalf("unexpected finding shape: %+v", f)
	}
	if !strings.Contains(f.Detail, "guardrail_arn") {
		t.Fatalf("Detail = %q, want it to name the suffix-matched candidate", f.Detail)
	}
}

func TestProbeIdentityShape_NoCandidateAtAll_Confirmed(t *testing.T) {
	block := provider.Block{Attributes: []provider.Attribute{
		attr("size", optional),
		attr("region", required),
	}}
	got := probeIdentityShape("acme/widget", "1.0.0", "acme_widget", block)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	f := got[0]
	if f.Class != FindingIncompleteRead || f.Tier != TierHermetic || f.Confidence != Confirmed {
		t.Fatalf("unexpected finding shape: %+v", f)
	}
	if f.Source != "acme/widget" || f.Version != "1.0.0" || f.Type != "acme_widget" || f.Verb != "read" {
		t.Fatalf("unexpected finding identity: %+v", f)
	}
}

func TestProbeSensitiveEcho_ComputedSensitive_NoFinding(t *testing.T) {
	block := provider.Block{Attributes: []provider.Attribute{
		attr("secret_value", computed, sensitive),
	}}
	if got := probeSensitiveEcho("acme/widget", "1.0.0", "acme_widget", block); got != nil {
		t.Fatalf("got %+v, want nil (already Sensitive-flagged)", got)
	}
}

func TestProbeSensitiveEcho_OptionalNonComputed_NoFinding(t *testing.T) {
	// A user-authored, non-computed attribute isn't an "echo" candidate
	// at all -- the user set it themselves, it's not the provider
	// reflecting something back.
	block := provider.Block{Attributes: []provider.Attribute{
		attr("connection_notes", optional),
	}}
	if got := probeSensitiveEcho("acme/widget", "1.0.0", "acme_widget", block); got != nil {
		t.Fatalf("got %+v, want nil (not Computed)", got)
	}
}

func TestProbeSensitiveEcho_ComputedNonSensitive_NoKeywordMatch_NoFinding(t *testing.T) {
	block := provider.Block{Attributes: []provider.Attribute{
		attr("region", computed),
	}}
	if got := probeSensitiveEcho("acme/widget", "1.0.0", "acme_widget", block); got != nil {
		t.Fatalf("got %+v, want nil (no keyword match)", got)
	}
}

func TestProbeSensitiveEcho_ComputedNonSensitive_KeywordMatch_Candidate(t *testing.T) {
	block := provider.Block{Attributes: []provider.Attribute{
		attr("manifest", computed),
	}}
	got := probeSensitiveEcho("acme/widget", "1.0.0", "acme_widget", block)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	f := got[0]
	if f.Class != FindingSensitiveUnderflag || f.Tier != TierHermetic || f.Confidence != Candidate {
		t.Fatalf("unexpected finding shape: %+v", f)
	}
	if f.Verb != "read" {
		t.Fatalf("Verb = %q, want \"read\"", f.Verb)
	}
}

func TestProbeSensitiveEcho_NestedBlock_DottedPath(t *testing.T) {
	// "notes" (unlike "values") is in the keyword corpus -- matches the
	// real helm_release.metadata.notes field this project's own UBI-22
	// audit flagged by hand.
	block := provider.Block{
		NestedBlocks: []provider.NestedBlock{
			{
				TypeName: "metadata",
				Block: provider.Block{Attributes: []provider.Attribute{
					attr("notes", computed),
				}},
			},
		},
	}
	got := probeSensitiveEcho("hashicorp/helm", "2.17.0", "helm_release", block)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	if got[0].Detail == "" {
		t.Fatal("expected a non-empty Detail naming the dotted path")
	}
	if !strings.Contains(got[0].Detail, "metadata.notes") {
		t.Fatalf("Detail = %q, want it to name the dotted path metadata.notes", got[0].Detail)
	}
}

func TestProbeSensitiveEcho_KnownFalsePositives_StillMatchButAreCandidatesOnly(t *testing.T) {
	// UBI-37's own real audit finding: these all match the keyword
	// corpus but are NOT real leaks. The hermetic probe can't know
	// that -- it must still flag them as Candidate (never silently
	// skip), and the Detail text must say "unconfirmed."
	block := provider.Block{Attributes: []provider.Attribute{
		attr("private_ip_address", computed),
		attr("login_server", computed),
		attr("public_key_openssh", computed),
	}}
	got := probeSensitiveEcho("hashicorp/azurerm", "5.0.0", "azurerm_thing", block)
	if len(got) != 3 {
		t.Fatalf("got %d findings, want 3 (all real keyword matches, even though all are false positives on inspection)", len(got))
	}
	for _, f := range got {
		if f.Confidence != Candidate {
			t.Fatalf("Confidence = %q, want %q for a hermetic keyword match -- must never claim Confirmed", f.Confidence, Candidate)
		}
	}
}

func TestProbeSensitiveEcho_AppSettingsShapeNotCaught(t *testing.T) {
	// Honest limitation, asserted directly rather than left implicit:
	// "app_settings" (the one REAL gap UBI-37 found) contains none of
	// the corpus keywords, so the hermetic probe does NOT catch it --
	// confirming this check is a triage aid, not a complete detector.
	block := provider.Block{Attributes: []provider.Attribute{
		attr("app_settings", optional),
	}}
	if got := probeSensitiveEcho("hashicorp/azurerm", "5.0.0", "azurerm_linux_web_app", block); got != nil {
		t.Fatalf("got %+v, want nil -- app_settings is Optional (not Computed) and matches no keyword; the hermetic probe can't find this class of gap, by design", got)
	}
}

func TestProbeDrift_AllComputed_Candidate(t *testing.T) {
	block := provider.Block{Attributes: []provider.Attribute{
		attr("id", computed),
		attr("rfc3339", computed),
		attr("unix", computed),
	}}
	got := probeDrift("hashicorp/time", "0.9.2", "time_static", block)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	f := got[0]
	if f.Class != FindingUndriftable || f.Tier != TierHermetic || f.Confidence != Candidate {
		t.Fatalf("unexpected finding shape: %+v", f)
	}
	if f.Verb != "drift" {
		t.Fatalf("Verb = %q, want \"drift\"", f.Verb)
	}
}

func TestProbeDrift_HasTopLevelSettable_NoFinding(t *testing.T) {
	block := provider.Block{Attributes: []provider.Attribute{
		attr("id", computed),
		attr("name", required),
	}}
	if got := probeDrift("acme/widget", "1.0.0", "acme_widget", block); got != nil {
		t.Fatalf("got %+v, want nil (has a Required attribute)", got)
	}
}

func TestProbeDrift_SettableOnlyInsideNestedBlock_NoFinding(t *testing.T) {
	block := provider.Block{
		Attributes: []provider.Attribute{attr("id", computed)},
		NestedBlocks: []provider.NestedBlock{
			{
				TypeName: "config",
				Block:    provider.Block{Attributes: []provider.Attribute{attr("size", optional)}},
			},
		},
	}
	if got := probeDrift("acme/widget", "1.0.0", "acme_widget", block); got != nil {
		t.Fatalf("got %+v, want nil -- a settable attribute nested one level down still counts", got)
	}
}

func TestProbeType_CleanType_NoFindings(t *testing.T) {
	block := provider.Block{Attributes: []provider.Attribute{
		attr("id", computed),
		attr("name", required),
		attr("region", optional),
	}}
	got := ProbeType("acme/widget", "1.0.0", "acme_widget", block)
	if got != nil {
		t.Fatalf("got %+v, want nil for a fully clean type", got)
	}
}

func TestProbeType_CombinesAllThreeProbes(t *testing.T) {
	// A type with no identity candidate AND every attribute computed --
	// should produce findings from both probeIdentityShape and
	// probeDrift (no sensitive-echo candidates here).
	block := provider.Block{Attributes: []provider.Attribute{
		attr("value", computed),
	}}
	got := ProbeType("acme/widget", "1.0.0", "acme_widget", block)
	classes := map[FindingClass]bool{}
	for _, f := range got {
		classes[f.Class] = true
	}
	if !classes[FindingIncompleteRead] || !classes[FindingUndriftable] {
		t.Fatalf("got %+v, want findings from both incomplete-read and undriftable probes", got)
	}
}

func TestProbeSchema_MultipleTypes_DeterministicOrder(t *testing.T) {
	schemas := &provider.Schemas{
		Resources: map[string]*provider.Schema{
			"acme_zzz": {Block: provider.Block{Attributes: []provider.Attribute{attr("value", computed)}}},
			"acme_aaa": {Block: provider.Block{Attributes: []provider.Attribute{attr("value", computed)}}},
		},
	}
	got1 := ProbeSchema("acme/widget", "1.0.0", schemas)
	got2 := ProbeSchema("acme/widget", "1.0.0", schemas)
	if len(got1) == 0 {
		t.Fatal("expected at least one finding")
	}
	if len(got1) != len(got2) {
		t.Fatalf("non-deterministic finding count: %d vs %d", len(got1), len(got2))
	}
	for i := range got1 {
		if got1[i] != got2[i] {
			t.Fatalf("non-deterministic order at index %d: %+v vs %+v", i, got1[i], got2[i])
		}
	}
	// acme_aaa must sort before acme_zzz.
	if got1[0].Type != "acme_aaa" {
		t.Fatalf("first finding's Type = %q, want \"acme_aaa\" (sorted first)", got1[0].Type)
	}
}

func TestProbeSchema_NilResourceEntry_Skipped(t *testing.T) {
	schemas := &provider.Schemas{
		Resources: map[string]*provider.Schema{
			"acme_broken": nil,
		},
	}
	got := ProbeSchema("acme/widget", "1.0.0", schemas)
	if got != nil {
		t.Fatalf("got %+v, want nil -- a nil schema entry must be skipped, never panic", got)
	}
}

func TestProbeSchema_IgnoresDataSources(t *testing.T) {
	schemas := &provider.Schemas{
		Resources: map[string]*provider.Schema{
			"acme_widget": {Block: provider.Block{Attributes: []provider.Attribute{
				attr("id", computed), attr("name", required),
			}}},
		},
		DataSources: map[string]*provider.Schema{
			"acme_widget": {Block: provider.Block{Attributes: []provider.Attribute{
				attr("value", computed), // would trip probeDrift if data sources were scanned
			}}},
		},
	}
	got := ProbeSchema("acme/widget", "1.0.0", schemas)
	if got != nil {
		t.Fatalf("got %+v, want nil -- DataSources must never be probed", got)
	}
}
