package blueprint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// TestExpandCalls_ArgsReachTheResolvedResource is the end-to-end gap the
// unit tests left: everything else about UBI-287 tests splitCallArgs or
// blueprintSource directly, so nothing proved an argument survives a real
// expansion into the resource a proposal is built from.
//
// That is the fixture-altitude failure this project keeps meeting, so it
// is closed here rather than after someone finds it by hand.
func TestExpandCalls_ArgsReachTheResolvedResource(t *testing.T) {
	requireDenoToolchain(t)
	dir := writeCallableBlueprint(t, "ts")

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "ci-platform call", Blueprint: dir, Args: map[string]string{
				"queue_name": "payments-notifications", "max_receive_count": "5",
			}},
		},
	}
	if _, err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}
	sources := intent.Resources[0].Sources
	if len(sources) != 1 {
		t.Fatalf("expected exactly 1 source, got %+v", sources)
	}
	got := sources[0]

	if got.DeclaredArgs["queue_name"] != "payments-notifications" {
		t.Errorf("queue_name did not reach the resource: %+v", got.DeclaredArgs)
	}
	if got.DeclaredArgs["max_receive_count"] != "5" {
		t.Errorf("max_receive_count did not reach the resource: %+v", got.DeclaredArgs)
	}
	if len(got.WithheldArgs) != 0 {
		t.Errorf("nothing in this blueprint is sensitive, so nothing should be withheld: %v", got.WithheldArgs)
	}

	// And the recorded argument is the one the CALLER wrote, not the
	// value the blueprint computed from it. That distinction is the whole
	// point: the config already holds the computed result, and until now
	// the input that produced it was thrown away.
	raw, err := json.Marshal(intent.Resources[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"payments-notifications"`) {
		t.Errorf("the caller's own argument is not in the resolved resource:\n%s", raw)
	}
}

// TestExpandCalls_SensitiveArgIsNeverInTheResolvedResource is the link
// that carries the actual security guarantee, and it is the one the unit
// tests could not reach.
//
// splitCallArgs is tested directly, extraction is tested per language,
// and Describe is tested on both routes. What none of them covers is
// whether invokeCall hands the blueprint's OWN declared params to the
// split. If that wiring is wrong, every piece passes its own test and a
// credential goes into the ledger anyway.
func TestExpandCalls_SensitiveArgIsNeverInTheResolvedResource(t *testing.T) {
	requireDenoToolchain(t)
	dir := writeCallableBlueprint(t, "ts")

	// The same blueprint, with one param re-declared as sensitive. Only
	// the Ubxfile changes: the generated code is identical, which is the
	// point. Sensitivity is a declaration, not a code path.
	sensitiveUbxfile := strings.Replace(callableUbxfileWithUnitConversion,
		"queue_name: string, required",
		"queue_name: string, required, sensitive", 1)
	if sensitiveUbxfile == callableUbxfileWithUnitConversion {
		t.Fatal("fixture drift: the param this test marks sensitive is no longer declared that way")
	}
	if err := os.WriteFile(filepath.Join(dir, UbxfileName), []byte(sensitiveUbxfile), 0o644); err != nil {
		t.Fatal(err)
	}

	const secret = "a-recognisable-queue-name-standing-in-for-a-credential"
	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "ci-platform call", Blueprint: dir, Args: map[string]string{
				"queue_name": secret, "max_receive_count": "5",
			}},
		},
	}
	if _, err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}

	got := intent.Resources[0].Sources[0]
	if v, present := got.DeclaredArgs["queue_name"]; present {
		t.Errorf("a sensitive argument was recorded as %q", v)
	}
	if len(got.WithheldArgs) != 1 || got.WithheldArgs[0] != "queue_name" {
		t.Errorf("withheld = %v, want [queue_name]: the fact that it existed must survive even though the value does not", got.WithheldArgs)
	}
	if got.DeclaredArgs["max_receive_count"] != "5" {
		t.Errorf("an ordinary argument was lost alongside the sensitive one: %+v", got.DeclaredArgs)
	}

	// Checked against the whole serialised source rather than the field
	// it is expected in, because absence from one key only proves absence
	// from the key I thought of.
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("the sensitive value is in the recorded source:\n%s", raw)
	}
}
