package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestScan_NullVsZeroValue_NotDrift is UBI-63 session 3's own hermetic
// repro of the founder's live "bug 4" finding: a real, live AWS re-test
// (after the previous session's scan-adopt-reship workaround, never a
// real code fix) still showed region/tags/force_detach_policies/
// inline_policy drifting on every fresh scan, purely from SDKv2-vintage
// providers not round-tripping a "no value" attribute byte-for-byte --
// alternating between JSON null and the type's own zero value (or, for a
// Computed attribute like region, resolving null to a real, provider-only
// value) across separate reads of the exact same semantic state.
//
// This adopts a resource whose recorded baseline mirrors the wounded
// stack's own shape (region/labels null, force_detach_policies false,
// inline_policy empty) and then scans it again with every one of those
// four attributes flipped to the OTHER representation the founder's own
// repro named -- proving core.FilterNormalizationNoise (core/scan.go)
// downgrades the verdict to "no drift" rather than reporting it as a
// change, across all four attribute shapes named in the diagnosis:
// Computed materialization (region), null map (labels, standing in for
// tags -- "tags"/"tags_all" are special-cased elsewhere in the fixture's
// own null-fill logic, which would mask the very case this test needs to
// exercise), bool default (force_detach_policies), and empty list
// (inline_policy).
func TestScan_NullVsZeroValue_NotDrift(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{
		"FAKEPROVIDER_MODE=conformance-v6",
		"FAKEPROVIDER_RESOURCE_TYPE=fake_iamlike_resource",
		"FAKEPROVIDER_ATTRS=id,region,labels,force_detach_policies,inline_policy",
		"FAKEPROVIDER_ATTR_TYPES=labels:map,force_detach_policies:bool,inline_policy:list",
		"FAKEPROVIDER_COMPUTED_ATTRS=region",
	}

	adoptViaCLI(t, ledgerDir, "payments", "fake_iamlike_resource", "role-1",
		`{"id":"role-1","region":null,"labels":null,"force_detach_policies":false,"inline_policy":[]}`, env)

	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_iamlike_resource", "--name", "role-1",
		"--lookup", `{"id":"role-1","region":"eu-central-1","labels":{},"force_detach_policies":null,"inline_policy":null}`,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("expected no drift (exit 0), got err=%v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "no drift:") {
		t.Fatalf("expected a 'no drift' verdict, got: %s", out)
	}
}

// TestScan_NullVsZeroValue_RealChangeStillDrifts is
// TestScan_NullVsZeroValue_NotDrift's own adversarial sibling: the
// normalization fix must never swallow a genuine change merely because
// it sits alongside null-noise on other attributes in the same diff --
// one real, non-null-to-different-non-null change (force_detach_policies
// true, not merely null<->false) must still surface as drift.
func TestScan_NullVsZeroValue_RealChangeStillDrifts(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{
		"FAKEPROVIDER_MODE=conformance-v6",
		"FAKEPROVIDER_RESOURCE_TYPE=fake_iamlike_resource",
		"FAKEPROVIDER_ATTRS=id,region,labels,force_detach_policies,inline_policy",
		"FAKEPROVIDER_ATTR_TYPES=labels:map,force_detach_policies:bool,inline_policy:list",
		"FAKEPROVIDER_COMPUTED_ATTRS=region",
	}

	adoptViaCLI(t, ledgerDir, "payments", "fake_iamlike_resource", "role-2",
		`{"id":"role-2","region":null,"labels":null,"force_detach_policies":false,"inline_policy":[]}`, env)

	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_iamlike_resource", "--name", "role-2",
		"--lookup", `{"id":"role-2","region":"eu-central-1","labels":{},"force_detach_policies":true,"inline_policy":null}`,
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "force_detach_policies") {
		t.Fatalf("expected the real force_detach_policies change to surface, got: %s", out)
	}
}

// TestScan_GenerateProposal_FiltersNormalizationNoise_KeepsRealChangeVisible
// is UBI-63 session 4's own hermetic repro of a masking bug found live: a
// resource with attachment_count/managed_policy_arns-style real drift
// (fix 2 not yet reconciled for a pre-fix ship) had its ENTIRE unfiltered
// diff rendered/recorded, including several other attributes that were
// each, individually, already explained by fix 1's own normalization --
// because the original design only ever downgraded RunScan's overall
// verdict, never filtered what a caller went on to display or persist.
// This resource has both a genuine change (force_detach_policies false ->
// true) and pure null<->zero-value/materialization noise (labels null ->
// {}, inline_policy [] -> null, region null -> a real value) in the SAME
// diff -- the generated drift_adopt proposal's own delta.modifies must
// carry only the real one, not the noise alongside it.
func TestScan_GenerateProposal_FiltersNormalizationNoise_KeepsRealChangeVisible(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{
		"FAKEPROVIDER_MODE=conformance-v6",
		"FAKEPROVIDER_RESOURCE_TYPE=fake_iamlike_resource",
		"FAKEPROVIDER_ATTRS=id,region,labels,force_detach_policies,inline_policy",
		"FAKEPROVIDER_ATTR_TYPES=labels:map,force_detach_policies:bool,inline_policy:list",
		"FAKEPROVIDER_COMPUTED_ATTRS=region",
	}

	adoptViaCLI(t, ledgerDir, "payments", "fake_iamlike_resource", "role-3",
		`{"id":"role-3","region":null,"labels":null,"force_detach_policies":false,"inline_policy":[]}`, env)

	proposalPath := filepath.Join(t.TempDir(), "drift.json")
	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_iamlike_resource", "--name", "role-3",
		"--lookup", `{"id":"role-3","region":"eu-central-1","labels":{},"force_detach_policies":true,"inline_policy":null}`,
		"--ledger-dir", ledgerDir, "--no-attribution",
		"--out", proposalPath,
	)
	requireExitCode(t, err, 1, scanOut)

	raw, err := os.ReadFile(proposalPath)
	if err != nil {
		t.Fatal(err)
	}
	var p core.Proposal
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("delta.modifies = %d entries, want exactly 1", len(p.Delta.Modifies))
	}
	mod := p.Delta.Modifies[0]
	if _, ok := mod.After["force_detach_policies"]; !ok {
		t.Fatalf("expected the real force_detach_policies change in delta.modifies, got: %+v", mod)
	}
	for _, noisy := range []string{"labels", "inline_policy", "region"} {
		if _, ok := mod.After[noisy]; ok {
			t.Fatalf("expected %q (normalization noise) filtered out of delta.modifies, got: %+v", noisy, mod)
		}
		if _, ok := mod.Before[noisy]; ok {
			t.Fatalf("expected %q (normalization noise) filtered out of delta.modifies, got: %+v", noisy, mod)
		}
	}
}
