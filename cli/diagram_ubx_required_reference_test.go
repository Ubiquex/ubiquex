package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanShip_FromDiagram_UbxRequired_JSONBlockStringValue_RoundTripsVerbatim_UBI93
// isolates UBI-93's own item 3: is ubx_required's "|md ... |" JSON
// block-string encoding (the exact shape platform.d2 used for
// assume_role_policy/policy in both the UBI-92 and UBI-93 live repros,
// playground-14/15) where the create-side false-success mechanism
// actually breaks? This proves NO -- a real diagram-authored
// ubx_required value written as a "|md ... |" block string containing
// JSON text reaches the real fakeprovider subprocess's own
// ApplyResourceChange call byte-for-byte unmodified, read straight back
// out of the ledger's own persisted provider_result. Matches what the
// real playground-14/15 CloudTrail events already showed independently
// (docs/executor.md's UBI-93 amendment): the real hashicorp/aws
// CreateRole request's own assumeRolePolicyDocument carried the exact
// JSON the diagram authored, no corruption, no re-encoding. The actual
// UBI-93 mechanism (a plain, non-JSON scalar literal that never matches
// the referenced resource's real assigned identity) is proven
// separately, and does not need JSON content to reproduce at all --
// see TestPlanShip_FromDiagram_UbxRequired_LiteralValueMismatch_NeverResolves_UBI93,
// same file.
func TestPlanShip_FromDiagram_UbxRequired_JSONBlockStringValue_RoundTripsVerbatim_UBI93(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[providers]
"fake/widget" = "0.1.0"
`)

	// fake_widget's own "name" is a plain string attribute (Required,
	// so eligible for ubx_required per UBI-91's own scope-discipline
	// check) -- it accepts arbitrary string content, including JSON
	// text, the same way a real aws_iam_role's own "assume_role_policy"
	// does. Using name here (rather than inventing a new fakeprovider
	// attribute) is deliberate: it lets this test ship for real through
	// the exact same wire protocol the live incidents used, with zero
	// new fixture surface.
	const jsonPolicyText = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`

	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, `
classes: {
  fake_widget: {}
}
ubx-playground-93a: {
  widget: "block-string-widget-93" {
    class: fake_widget
    ubx_required.name: |md
      `+jsonPolicyText+`
    |
  }
}
`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6", "UBX_PROVIDER_MIRROR=" + mirrorDir}

	planOut, err := runUbx(t, env, "plan", "--from-diagram", diagramPath, "--stack", "ubx-playground-93a", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx plan --from-diagram: %v\noutput: %s", err, planOut)
	}
	if strings.Contains(planOut, "missing required attribute") || strings.Contains(planOut, "not a required attribute") {
		t.Fatalf("expected zero ubx_required refusal, got: %s", planOut)
	}
	hash := mustExtractPlanHash(t, dir, planOut)

	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", dir, "--yes")
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "shipped") {
		t.Fatalf("expected a shipped outcome, got: %s", shipOut)
	}

	// Read the raw ledger apply record directly (the same way this
	// session's own live CloudTrail/ledger cross-check on
	// playground-14/15 worked) rather than through `ubx why`'s own
	// human-text rendering, which isn't obligated to print the full
	// provider_result verbatim.
	applyPath := filepath.Join(dir, "ledger", "applies", hash+".attempt-1.apply.json")
	raw, err := os.ReadFile(applyPath)
	if err != nil {
		t.Fatalf("reading ledger apply record: %v", err)
	}
	var rec struct {
		Resources []struct {
			ProviderResult json.RawMessage `json:"provider_result"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("unmarshal ledger apply record: %v", err)
	}
	if len(rec.Resources) != 1 {
		t.Fatalf("expected exactly 1 resource in the apply record, got %d", len(rec.Resources))
	}
	var result struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Resources[0].ProviderResult, &result); err != nil {
		t.Fatalf("unmarshal provider_result: %v", err)
	}
	if result.Name != jsonPolicyText {
		t.Fatalf("provider_result.name round-tripped as %q, want the exact JSON block-string content %q byte-for-byte -- if this fails, ubx_required's own block-string encoding IS implicated", result.Name, jsonPolicyText)
	}
}

// TestPlanShip_FromDiagram_UbxRequired_LiteralValueMismatch_NeverResolves_UBI93
// is the OTHER half of UBI-93's own isolation: a diagram resource
// referencing a same-batch, already-shipped dependency via a PLAIN
// scalar ubx_required literal (no JSON, no block-string syntax at all)
// that simply doesn't match the dependency's own real, ubx_required-
// supplied identity reproduces the identical retry-then-honest-bailout
// shape the live incidents hit (playground-14/15: an "attach" resource's
// own ubx_required.role/policy_arn literal named the diagram's OWN
// label text, never the real provider-assigned name -- see
// docs/executor.md's UBI-93 amendment for the full mechanism). Proves
// the failure is about ubx_required's own literal-value, zero-reference-
// resolution design (UBI-91: "read verbatim... no reference resolution"),
// not about JSON content or block-string parsing -- this test never
// uses either.
func TestPlanShip_FromDiagram_UbxRequired_LiteralValueMismatch_NeverResolves_UBI93(t *testing.T) {
	if os.Getenv("UBX_TEST_SLOW") == "" {
		t.Skip("set UBX_TEST_SLOW=1 to run this real-wire-protocol test -- it takes about ~19s (core/executor's own noProgressBailoutThreshold, UBI-93, before it bails out), same gating convention as cli/ship_create_dependency_retry_test.go's own budget-exhausted test")
	}

	dir := t.TempDir()
	mirrorDir := t.TempDir()

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[providers]
"fake/widget" = "0.1.0"
`)

	// "role" ships under its own real ubx_required.name. "attach" has a
	// real topology dependency on "role" (the "attach -> role" edge,
	// giving it a genuine depends_on/hasShippedDependency edge, exactly
	// as platform.d2's own "attach -> role"/"attach -> policy" edges
	// did live) but its own ubx_required.name is a plain scalar literal
	// that simply assumes a name -- never role's real one, and never
	// resolved against it (ubx_required performs no $ref/$computed
	// substitution by design). fakeprovider's own fail-create-not-found
	// mode, targeted at attach's own literal name with a budget that
	// never exhausts (FAKEPROVIDER_FAIL_CREATE_COUNT far larger than
	// eventualConsistencyBackoffSchedule's own 10 steps), models "this
	// identity genuinely never becomes visible" -- because it never
	// will, the same as the live incidents.
	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, `
classes: {
  fake_widget: {}
}
ubx-playground-93b: {
  role: "role-93b" {
    class: fake_widget
    ubx_required.name: "role-real-93b"
  }
  attach: "attach-93b" {
    class: fake_widget
    ubx_required.name: "role-assumed-93b"
  }
  attach -> role
}
`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6", "UBX_PROVIDER_MIRROR=" + mirrorDir}

	planOut, err := runUbx(t, env, "plan", "--from-diagram", diagramPath, "--stack", "ubx-playground-93b", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx plan --from-diagram: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, dir, planOut)

	shipEnv := append(append([]string{}, env...),
		"FAKEPROVIDER_APPLY_MODE=fail-create-not-found",
		"FAKEPROVIDER_FAIL_CREATE_TARGET=role-assumed-93b",
		"FAKEPROVIDER_FAIL_CREATE_COUNT=1000",
	)
	shipOut, err := runUbx(t, shipEnv, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", dir, "--yes")
	if exitCode(err) != 1 {
		t.Fatalf("ubx ship against a permanently-mismatched literal reference: want exit 1, got %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "1 shipped") || !strings.Contains(shipOut, "1 failed") {
		t.Fatalf("expected role shipped, attach failed, got: %s", shipOut)
	}

	whyOut, err := runUbx(t, env, "why", hash, "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", hash, err, whyOut)
	}
	if !strings.Contains(whyOut, "reconcile:") {
		t.Fatalf("expected the retry to have fired at all (proves this DID enter retryCreateOnDependencyNotVisible via the diagram+ubx_required path, not fail immediately), got: %s", whyOut)
	}
	if !strings.Contains(whyOut, "does not appear to exist") {
		t.Fatalf("expected UBI-93's own honest early-bailout wording once retrying produced zero progress, got: %s", whyOut)
	}
}
