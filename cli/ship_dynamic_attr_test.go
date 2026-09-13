package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A free-form JSON attribute is declared "dynamic" by a
// CloudFormation-derived schema: the schema states no shape and the
// value carries its own. 707 such attributes across 482 of the 1,724
// AWS resources, every IAM policy document among them, and every one of
// them failed to ship until provider/ctyvalue.go learned the type.
//
// That fix shipped with unit tests that encode and decode through real
// go-cty msgpack, which proves the bytes. This proves the PATH: a real
// resolve, a real accept, a real ship, a real read-back and a real
// update against a real fakeprovider subprocess.
//
// It could not have been written before, because nothing in
// fakeprovider could express a dynamic-typed attribute at all. That is
// the gap this closes, and it is why the defect reached a real stack:
// the conformance harness had no way to model the shape.
//
// What it does and does not catch, checked by reverting each fix
// individually rather than assumed:
//
//	planned-state encode  caught here
//	decode                caught here
//	prior-state encode    NOT caught here, unit tests only
//
// The prior-state encoder is reached by these steps, but nothing in
// this harness routes a dynamic value through it in a way that fails
// visibly: fakeprovider's conformance mode keeps no state between
// invocations, so a read-back reports drift on every attribute either
// way and a destroy refuses as drifted before the encoder is asked for
// anything. provider's own TestEncodePriorState_AcceptsADynamicAttribute
// and TestRealAWSDynamicAttributes cover it at the byte level. Closing
// that last gap needs a fakeprovider that remembers what it applied,
// which is a larger change than this one.
func TestResolveAcceptShip_DynamicAttribute_SurvivesTheRealWire(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{
		"FAKEPROVIDER_MODE=conformance-v6",
		"FAKEPROVIDER_RESOURCE_TYPE=aws_sqs_queue_policy",
		"FAKEPROVIDER_ATTRS=id,queue_url,policy_document",
		"FAKEPROVIDER_ATTR_TYPES=policy_document:dynamic",
	}

	// A real IAM-shaped policy document, which is the class of value
	// this exists for: nested objects, an array of statements, and an
	// Action that is an array where Resource is a bare string. A
	// list-typed or map-typed encoding cannot represent that mixture,
	// which is why the fix infers tuple and object types.
	policy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []interface{}{
			map[string]interface{}{
				"Effect":    "Allow",
				"Principal": map[string]interface{}{"Service": "sns.amazonaws.com"},
				"Action":    []interface{}{"sqs:SendMessage"},
				"Resource":  "arn:aws:sqs:us-east-1:111122223333:orders",
			},
		},
	}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "a dynamic-typed policy document"},
		"resources": []map[string]interface{}{
			{
				"type": "aws_sqs_queue_policy",
				"name": "orders",
				"op":   "create",
				"config": map[string]interface{}{
					"queue_url":       "https://sqs.us-east-1.amazonaws.com/111122223333/orders",
					"policy_document": policy,
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

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	// The create path. This is the call that failed with
	// "encode planned state: encode value: attribute
	// \"policy_document\": unsupported type dynamic".
	shipOut, err := runUbx(t, env, "ship", changeID,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut)
	}

	// A real read-back. ReadResource sends the CURRENT state, which goes
	// through encodeDynamicValue, the second of the three broken paths.
	// This exercises it and would surface an encode failure that
	// reached the output, though see the note above for why it does not
	// catch that path's own regression on its own.
	scanOut, _ := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--stack", "payments",
	)
	// scan exits nonzero when it has something to report, which is not a
	// failure of the path under test. An encode failure is, and it says
	// so in the output rather than in the exit code.
	for _, bad := range []string{"unsupported type", "dynamically-typed value"} {
		if strings.Contains(scanOut, bad) {
			t.Fatalf("reading the resource back failed on its dynamic attribute (%s):\n%s", bad, scanOut)
		}
	}

	// A real update. PlanResourceChange receives the prior state, the
	// same encoder a destroy uses. Same caveat as the read-back above.
	// Same document, op "modify", since the address now exists.
	modifyPath := filepath.Join(ledgerDir, "modify.json")
	writeIntentFile(t, modifyPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "update the dynamic-typed policy document"},
		"resources": []map[string]interface{}{
			{
				"type": "aws_sqs_queue_policy",
				"name": "orders",
				"op":   "modify",
				"config": map[string]interface{}{
					"queue_url":       "https://sqs.us-east-1.amazonaws.com/111122223333/orders",
					"policy_document": policy,
				},
			},
		},
	})

	reresolvedPath := filepath.Join(ledgerDir, "reresolved.json")
	reresolveOut, err := runUbx(t, env, "resolve", modifyPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", reresolvedPath,
	)
	if err != nil {
		t.Fatalf("ubx resolve (second pass, prior-state path): %v\noutput: %s", err, reresolveOut)
	}
	for _, bad := range []string{"unsupported type", "dynamically-typed value"} {
		if strings.Contains(reresolveOut, bad) {
			t.Fatalf("the prior-state encode path failed on its dynamic attribute (%s):\n%s", bad, reresolveOut)
		}
	}

	// What came back out of a real gRPC round trip, read from the ledger
	// rather than from anything ubx claims to have sent.
	//
	// The decode half of this defect did not fail, it returned go-cty's
	// own {"value":..,"type":..} wrapper, so asserting "it shipped" is
	// not enough. The stored value has to be the JSON that went in.
	whyOut, err := runUbx(t, env, "why", changeID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, whyOut)
	}
	if strings.Contains(whyOut, `"type":[`) {
		t.Fatalf("the applied state carries go-cty's type wrapper instead of the policy document:\n%s", whyOut)
	}
	for _, want := range []string{"2012-10-17", "sns.amazonaws.com", "sqs:SendMessage", "arn:aws:sqs:us-east-1:111122223333:orders"} {
		if !strings.Contains(whyOut, want) {
			t.Errorf("the applied policy document lost %q:\n%s", want, whyOut)
		}
	}

	// And the exact structure, from the ledger itself, so a coincidental
	// substring match cannot pass for a preserved document.
	assertLedgerPolicyDocument(t, ledgerDir, policy)
}

// assertLedgerPolicyDocument finds the applied state in the ledger and
// compares its policy_document against what was authored.
func assertLedgerPolicyDocument(t *testing.T, ledgerDir string, want map[string]interface{}) {
	t.Helper()

	var found json.RawMessage
	err := filepath.Walk(ledgerDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != nil || !strings.HasSuffix(path, ".json") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var probe interface{}
		if json.Unmarshal(raw, &probe) != nil {
			return nil
		}
		// The applied record specifically: the point is what came BACK
		// from the provider, not what was authored or resolved.
		if !strings.Contains(string(raw), "provider_result") && !strings.Contains(string(raw), "applied") {
			return nil
		}
		if doc := findKey(probe, "policy_document"); doc != nil {
			found, _ = json.Marshal(doc)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("no applied record carrying policy_document was found in the ledger, so this assertion silently proved nothing")
	}

	wantJSON, _ := json.Marshal(want)
	var gotAny, wantAny interface{}
	if err := json.Unmarshal(found, &gotAny); err != nil {
		t.Fatalf("stored policy_document is not valid JSON: %v\n%s", err, found)
	}
	if err := json.Unmarshal(wantJSON, &wantAny); err != nil {
		t.Fatal(err)
	}
	gotNorm, _ := json.Marshal(gotAny)
	wantNorm, _ := json.Marshal(wantAny)
	if string(gotNorm) != string(wantNorm) {
		t.Errorf("the policy document changed crossing the wire:\n  authored: %s\n  stored:   %s", wantNorm, gotNorm)
	}
}

// findKey returns the first value stored under key anywhere in a decoded
// JSON tree.
func findKey(v interface{}, key string) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		if got, ok := t[key]; ok {
			return got
		}
		for _, sub := range t {
			if got := findKey(sub, key); got != nil {
				return got
			}
		}
	case []interface{}:
		for _, sub := range t {
			if got := findKey(sub, key); got != nil {
				return got
			}
		}
	}
	return nil
}
