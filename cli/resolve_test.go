package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeIntentFile is a small helper: marshal an ubx:intent/v1 file to disk
// for `ubx resolve` to read.
func writeIntentFile(t *testing.T, path string, intent map[string]interface{}) {
	t.Helper()
	b, err := json.Marshal(intent)
	if err != nil {
		t.Fatalf("marshal intent file: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write intent file: %v", err)
	}
}

// TestResolveAccept_SimpleCreate exercises `ubx resolve` end to end against
// a real (fake) provider schema and a real, empty ledger: an intent file
// with a single "create" resolves into a draft kind:"change" proposal that
// `ubx accept` then takes exactly like a scan-generated proposal.
func TestResolveAccept_SimpleCreate(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "add widget1"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "widget1",
				"op":   "create",
				"config": map[string]interface{}{
					"name": "widget1",
					"tags": map[string]string{"env": "prod"},
				},
			},
		},
	})

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
	)
	if err != nil {
		t.Fatalf("ubx resolve: %v\noutput: %s", err, resolveOut)
	}
	if !strings.Contains(resolveOut, "1 create(s), 0 change(s)") {
		t.Fatalf("expected a 1-create summary, got: %s", resolveOut)
	}

	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"kind": "change"`) {
		t.Fatalf("resolved proposal missing kind:change: %s", raw)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (resolved): %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	whyOut, err := runUbx(t, env, "why", changeID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why (change): %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "change") {
		t.Fatalf("why output missing change kind: %s", whyOut)
	}
}

// TestResolve_UnknownType confirms `ubx resolve` surfaces
// resolver.ErrUnknownType as a plain error (exit 2 -- resolve has no
// "finding" concept, same audit outcome as propose/init).
func TestResolve_UnknownType(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "add ghost"},
		"resources": []map[string]interface{}{
			{
				"type":   "ghost_type",
				"name":   "x",
				"op":     "create",
				"config": map[string]interface{}{},
			},
		},
	})

	_, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "no such type") {
		t.Fatalf("expected an unknown-type error, got: %v", err)
	}
}

// TestResolve_HallucinatedAttributeNames_Refused is UBI-66's own
// end-to-end proof, run against a REAL fakeprovider process (not an
// in-process fake SchemaInspector, core/resolver's own hermetic tests
// already cover that) -- exercising the real wiring all the way through
// cli/schemainspector.go's schemaInspectorAdapter: `ubx resolve` given an
// intent file using exactly the two hallucinated attribute names a real
// live run (Claude Haiku) produced for a real aws_iam_role
// (role_name/assume_role_policy_document instead of the real
// name/assume_role_policy) must refuse, naming both, each with the
// correct real-key suggestion -- never reaching ApplyResourceChange at
// all, let alone partway through a real ship.
func TestResolve_HallucinatedAttributeNames_Refused(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{
		"FAKEPROVIDER_MODE=conformance-v6",
		"FAKEPROVIDER_RESOURCE_TYPE=aws_iam_role",
		"FAKEPROVIDER_ATTRS=id,name,assume_role_policy,arn",
	}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "platform",
		"intent":         map[string]interface{}{"summary": "CI role"},
		"resources": []map[string]interface{}{
			{
				"type": "aws_iam_role",
				"name": "ci-runner",
				"op":   "create",
				"config": map[string]interface{}{
					"role_name":                   "ci-runner",
					"assume_role_policy_document": "{}",
				},
			},
		},
	})

	_, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 2, "")
	msg := err.Error()
	if !strings.Contains(msg, `"role_name" does not exist on aws_iam_role (did you mean "name"?)`) {
		t.Errorf("missing role_name teaching error: %s", msg)
	}
	if !strings.Contains(msg, `"assume_role_policy_document" does not exist on aws_iam_role (did you mean "assume_role_policy"?)`) {
		t.Errorf("missing assume_role_policy_document teaching error: %s", msg)
	}
}

// TestResolveAccept_CrossStackPin_StaleBlocksAccept is the CLI-level
// version of docs/resolver-adversarial.md row 5: a proposal resolved with
// a $cross reference pins the neighbor ledger's head at resolve time; if
// that neighbor ledger advances before the proposal is accepted, `ubx
// accept` must refuse it (resolver.VerifyPins, wired into both accept
// paths this session) rather than silently accepting a decision made
// against since-superseded neighbor state.
func TestResolveAccept_CrossStackPin_StaleBlocksAccept(t *testing.T) {
	stackDir := t.TempDir()
	neighborDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// Seed the neighbor ("networking") ledger with a real adopted resource
	// via the same scan/accept path every other CLI test uses.
	neighborAdopt := filepath.Join(neighborDir, "adopt.json")
	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "networking",
		"--type", "fake_widget",
		"--name", "vpc",
		"--lookup", `{"name":"vpc","tags":{"env":"prod"}}`,
		"--ledger-dir", neighborDir,
		"--out", neighborAdopt,
	)
	requireExitCode(t, err, 1, scanOut)
	acceptOut, err := runUbx(t, env, "accept", neighborAdopt, "--ledger-dir", neighborDir)
	if err != nil {
		t.Fatalf("ubx accept (neighbor seed): %v\noutput: %s", err, acceptOut)
	}

	// Resolve a proposal in "payments" that cross-references the
	// neighbor's vpc.name -- this records the neighbor's CURRENT head as
	// the pin.
	intentPath := filepath.Join(stackDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "add widget pinned to neighbor vpc"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "widget1",
				"op":   "create",
				"config": map[string]interface{}{
					"name": map[string]interface{}{
						"$cross": map[string]interface{}{
							"ledger_dir": neighborDir,
							"to":         "networking.fake_widget.vpc.name",
						},
					},
				},
			},
		},
	})
	resolvedPath := filepath.Join(stackDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", stackDir,
		"--out", resolvedPath,
	)
	if err != nil {
		t.Fatalf("ubx resolve (cross-stack): %v\noutput: %s", err, resolveOut)
	}

	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cross_stack_pin"`) {
		t.Fatalf("resolved proposal missing a cross_stack_pin resolution input: %s", raw)
	}

	// Advance the neighbor ledger's head before accepting the pinned
	// proposal -- a second, unrelated adoption stands in for "someone
	// else changed the neighbor stack in the meantime."
	neighborAdopt2 := filepath.Join(neighborDir, "adopt2.json")
	scanOut2, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "networking",
		"--type", "fake_widget",
		"--name", "subnet",
		"--lookup", `{"name":"subnet"}`,
		"--ledger-dir", neighborDir,
		"--out", neighborAdopt2,
	)
	requireExitCode(t, err, 1, scanOut2)
	acceptOut2, err := runUbx(t, env, "accept", neighborAdopt2, "--ledger-dir", neighborDir)
	if err != nil {
		t.Fatalf("ubx accept (neighbor advance): %v\noutput: %s", err, acceptOut2)
	}

	// Now accepting the pinned proposal must be refused.
	_, err = runUbx(t, env, "accept", resolvedPath, "--ledger-dir", stackDir)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected a stale cross-stack pin error, got: %v", err)
	}

	// And confirm it truly never reached the stack's own ledger.
	if _, statErr := os.Stat(filepath.Join(stackDir, ".ubx", "ledger.lock")); statErr == nil {
		t.Fatal("ledger.lock should not exist -- the stale-pinned proposal must not have been accepted")
	}
}
