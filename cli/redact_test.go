package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sensitiveEnv drives the fakeprovider fixture's "conformance" mode with
// one Sensitive attribute (api_key) alongside id/name -- see
// provider/internal/fakeprovider's package doc comment for the env var
// contract (UBI-23 added FAKEPROVIDER_SENSITIVE_ATTRS).
var sensitiveEnv = []string{
	"FAKEPROVIDER_MODE=conformance-v6",
	"FAKEPROVIDER_RESOURCE_TYPE=fake_secret_resource",
	"FAKEPROVIDER_ATTRS=id,name,api_key",
	"FAKEPROVIDER_SENSITIVE_ATTRS=api_key",
}

// TestScan_RedactsSensitiveAttribute_AdoptionAndDriftBothDirections is
// UBI-23's core end-to-end adversarial case, run against the actual `ubx`
// CLI commands rather than core/provider internals directly: a resource
// with a Sensitive-flagged attribute must adopt cleanly with the secret
// redacted, must NOT report drift when the real secret is unchanged
// across scans (proving redaction doesn't defeat drift detection by
// making everything look different every time), MUST report drift when
// the real secret genuinely changes, and the generated proposal file must
// never contain the real secret material on disk at any point -- the
// "confirm the proposal file on disk contains NO material, grep it"
// requirement, hermetically (the real-AWS live equivalent is run
// separately, gated behind UBX_CONFORMANCE_LIVE).
//
// --lookup deliberately carries only "id" throughout -- never the secret
// itself. This matches every real provider schema this project has
// empirically checked (conformance/registry.go's LookupHint/IdentityFields
// across both AWS and GCP): an identity/lookup-worthy attribute is never
// also Sensitive-flagged in practice, so ubx's lookup key never needs to
// carry secret material. The fake_secret_resource's api_key value instead
// comes from FAKEPROVIDER_MUTATE_ATTR/VALUE, standing in for a real
// provider-computed/stored secret a live ReadResource call would return
// regardless of what identifying key was supplied.
func TestScan_RedactsSensitiveAttribute_AdoptionAndDriftBothDirections(t *testing.T) {
	ledgerDir := t.TempDir()
	const secretV1 = "initial-secret-value"
	const secretV2 = "rotated-secret-value"

	adoptEnv := append(append([]string{}, sensitiveEnv...), "FAKEPROVIDER_MUTATE_ATTR=api_key", "FAKEPROVIDER_MUTATE_VALUE="+secretV1)
	adoptPath := filepath.Join(ledgerDir, "adopt.json")
	scanOut, err := runUbx(t, adoptEnv, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_secret_resource",
		"--name", "widget1",
		"--lookup", `{"id":"widget1"}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
	)
	requireExitCode(t, err, 1, scanOut)
	if !strings.Contains(scanOut, "new:") {
		t.Fatalf("expected a 'new' classification, got: %s", scanOut)
	}
	requireNoSecretMaterial(t, adoptPath, secretV1, secretV2)

	acceptOut, err := runUbx(t, adoptEnv, "accept", adoptPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (adopt): %v\noutput: %s", err, acceptOut)
	}
	adoptID := mustExtractID(t, acceptOut)

	// Re-scanning against the exact same (unchanged) secret must report no
	// drift -- redaction must not make every scan look like a change.
	scanOut2, err := runUbx(t, adoptEnv, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_secret_resource",
		"--name", "widget1",
		"--lookup", `{"id":"widget1"}`,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx scan (unchanged secret): %v\noutput: %s", err, scanOut2)
	}
	if !strings.Contains(scanOut2, "no drift") {
		t.Fatalf("expected 'no drift' for an unchanged secret, got: %s", scanOut2)
	}

	// The real secret rotates -- drift must fire.
	driftEnv := append(append([]string{}, sensitiveEnv...), "FAKEPROVIDER_MUTATE_ATTR=api_key", "FAKEPROVIDER_MUTATE_VALUE="+secretV2)
	driftPath := filepath.Join(ledgerDir, "drift.json")
	scanOut3, err := runUbx(t, driftEnv, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_secret_resource",
		"--name", "widget1",
		"--lookup", `{"id":"widget1"}`,
		"--ledger-dir", ledgerDir,
		"--out", driftPath,
		"--no-attribution",
	)
	requireExitCode(t, err, 1, scanOut3)
	if !strings.Contains(scanOut3, "drifted:") {
		t.Fatalf("expected a 'drifted' classification when the secret rotates, got: %s", scanOut3)
	}
	requireNoSecretMaterial(t, driftPath, secretV1, secretV2)

	// The drift proposal's delta must show a redacted before/after, not a
	// missing/empty one, and its persisted lookup key (needed verbatim for
	// future re-verification, docs/schema.md's own lookup-key amendment)
	// must never itself carry the secret -- confirming the realistic case
	// (identity fields are never Sensitive-flagged, empirically) holds.
	raw, err := os.ReadFile(driftPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"api_key"`) {
		t.Fatalf("expected the drift proposal to mention the changed api_key attribute, got: %s", raw)
	}
	var driftProposal map[string]interface{}
	if err := json.Unmarshal(raw, &driftProposal); err != nil {
		t.Fatalf("decode drift proposal: %v", err)
	}
	inputs := driftProposal["resolution"].(map[string]interface{})["inputs"].([]interface{})
	lookup, err := json.Marshal(inputs[0].(map[string]interface{})["lookup"])
	if err != nil {
		t.Fatal(err)
	}
	if string(lookup) != `{"id":"widget1"}` {
		t.Fatalf("expected the persisted lookup key to be exactly {\"id\":\"widget1\"}, got: %s", lookup)
	}

	acceptOut2, err := runUbx(t, driftEnv, "accept", driftPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (drift): %v\noutput: %s", err, acceptOut2)
	}
	driftID := mustExtractID(t, acceptOut2)

	// why must render "(redacted)" for the changed attribute, never the
	// hash or the real value.
	whyOut, err := runUbx(t, nil, "why", driftID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why (drift): %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "(redacted)") {
		t.Fatalf("expected why to render (redacted) for the changed sensitive attribute, got: %s", whyOut)
	}
	if strings.Contains(whyOut, secretV1) || strings.Contains(whyOut, secretV2) || strings.Contains(whyOut, "sha256") {
		t.Fatalf("why output must never show the real secret or its raw hash inline, got: %s", whyOut)
	}
	_ = adoptID

	// --json output must carry the same $redacted marker, never the real
	// material -- no separate code path to diverge from the human view's
	// safety guarantee.
	jsonOut, err := runUbx(t, nil, "why", driftID, "--ledger-dir", ledgerDir, "--json")
	if err != nil {
		t.Fatalf("ubx why --json: %v\noutput: %s", err, jsonOut)
	}
	if !strings.Contains(jsonOut, "$redacted") {
		t.Fatalf("expected --json output to carry the $redacted marker, got: %s", jsonOut)
	}
	if strings.Contains(jsonOut, secretV1) || strings.Contains(jsonOut, secretV2) {
		t.Fatalf("--json output must never contain the real secret material, got: %s", jsonOut)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(jsonOut), &decoded); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
}

// requireNoSecretMaterial reads path and fails the test if it contains
// either the old or new secret value in plaintext -- the direct
// implementation of "confirm the proposal file on disk contains NO
// material, grep it."
func requireNoSecretMaterial(t *testing.T, path string, secrets ...string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, s := range secrets {
		if strings.Contains(string(raw), s) {
			t.Fatalf("proposal file %s leaked real secret material %q:\n%s", path, s, raw)
		}
	}
	if !strings.Contains(string(raw), "$redacted") || !strings.Contains(string(raw), "sha256") {
		t.Fatalf("expected proposal file %s to contain a $redacted/sha256 marker, got:\n%s", path, raw)
	}
}

// TestWriteback_DeclinesRedactedAttribute is the CLI-level confirmation
// that `ubx writeback` never writes a redacted marker into a real .tf
// file, end to end through the actual command (writeback/writeback_test.go
// already covers the underlying mechanism in isolation).
func TestWriteback_DeclinesRedactedAttribute(t *testing.T) {
	ledgerDir := t.TempDir()
	const secretV1 = "initial-secret-value"
	const secretV2 = "rotated-secret-value"

	adoptEnv := append(append([]string{}, sensitiveEnv...), "FAKEPROVIDER_MUTATE_ATTR=api_key", "FAKEPROVIDER_MUTATE_VALUE="+secretV1)
	adoptPath := filepath.Join(ledgerDir, "adopt.json")
	_, err := runUbx(t, adoptEnv, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_secret_resource",
		"--name", "web",
		"--lookup", `{"id":"web"}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
	)
	requireExitCode(t, err, 1, "")
	if _, err := runUbx(t, adoptEnv, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("accept adopt: %v", err)
	}

	driftEnv := append(append([]string{}, sensitiveEnv...), "FAKEPROVIDER_MUTATE_ATTR=api_key", "FAKEPROVIDER_MUTATE_VALUE="+secretV2)
	driftPath := filepath.Join(ledgerDir, "drift.json")
	_, err = runUbx(t, driftEnv, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_secret_resource",
		"--name", "web",
		"--lookup", `{"id":"web"}`,
		"--ledger-dir", ledgerDir,
		"--out", driftPath,
		"--no-attribution",
	)
	requireExitCode(t, err, 1, "")
	acceptOut, err := runUbx(t, driftEnv, "accept", driftPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("accept drift: %v\n%s", err, acceptOut)
	}
	driftID := mustExtractID(t, acceptOut)

	tfDir := t.TempDir()
	tfFile := filepath.Join(tfDir, "main.tf")
	tfContent := "resource \"fake_secret_resource\" \"web\" {\n  api_key = \"" + secretV1 + "\"\n}\n"
	if err := os.WriteFile(tfFile, []byte(tfContent), 0o644); err != nil {
		t.Fatal(err)
	}

	writebackOut, err := runUbx(t, nil, "writeback", driftID, "--ledger-dir", ledgerDir, "--tf-dir", tfDir, "--write")
	requireExitCode(t, err, 1, writebackOut)
	if !strings.Contains(writebackOut, "redacted") {
		t.Fatalf("expected writeback to name redaction as the decline reason, got: %s", writebackOut)
	}

	after, err := os.ReadFile(tfFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != tfContent {
		t.Fatalf("expected the .tf file to be left untouched by a declined redacted attribute, got:\n%s", after)
	}
	if strings.Contains(string(after), secretV2) || strings.Contains(string(after), "$redacted") || strings.Contains(string(after), "sha256") {
		t.Fatalf("the .tf file must never gain the new secret, a $redacted marker, or a hash, got:\n%s", after)
	}
}

// TestScanAll_SummaryReportsRedactionCount is bulk onboarding's own
// UBI-23 requirement: a resource with sensitive fields adopts cleanly with
// redaction applied, and the batch summary notes how many attributes were
// redacted -- visibility that adoption handled it, not a silent side
// effect.
func TestScanAll_SummaryReportsRedactionCount(t *testing.T) {
	statePath := writeState(t, `
		{"mode": "managed", "type": "fake_secret_resource", "name": "widget", "instances": [{"attributes": {"id": "widget-1"}}]}
	`)
	ledgerDir := t.TempDir()
	env := append(append([]string{}, sensitiveEnv...), "FAKEPROVIDER_MUTATE_ATTR=api_key", "FAKEPROVIDER_MUTATE_VALUE=onboarded-secret")

	out, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx scan --all: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "1 adopted, 0 skipped, 1 attribute(s) redacted") {
		t.Fatalf("expected the summary to report 1 redacted attribute, got: %s", out)
	}
	if strings.Contains(out, "onboarded-secret") {
		t.Fatalf("scan --all output must never contain the real secret material, got: %s", out)
	}
}
