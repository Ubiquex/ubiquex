package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestShip_SecondRealModify_LookupRefreshedNotFrozen is UBI-238's own
// end-to-end regression test, against a real fakeprovider subprocess:
// shipping a SECOND real modify against the same resource address, after
// a first modify already shipped successfully, used to fail with a
// spurious stale-observation error every time -- confirmed live, not
// assumed, via two isolated manual repros before this fix, then via
// direct inspection of resolution.inputs[].lookup itself, which stayed
// frozen at create time forever because core/executor's shipModifyNode
// discarded ApplyResourceChange's own fresh lookup return on every
// successful modify (core/executor/ship.go), and core.Ledger.LastLookup
// (core/fleet.go) never consulted a modify's own apply record for a
// refreshed one even when one existed.
//
// fake_widget's own "id" is a documented, deliberate constant
// ("computed-id", identical for every instance -- see
// provider/internal/fakeprovider/main.go's own persistedStatePath doc
// comment), which forces its "name" (mutable, Required) into the
// disambiguating lookup role -- this is what makes the gap observable
// here at all; docs/architecture.md's own "Lookup keys must be derivable
// from immutable attributes" note (added alongside this fix) has the
// full account of why ordinary real infrastructure (an immutable id/ARN
// alone) never hits this.
func TestShip_SecondRealModify_LookupRefreshedNotFrozen(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create widget1"},
  "resources": [
    {"type": "fake_widget", "name": "widget1", "op": "create", "config": {"name": "widget1"}}
  ],
  "destroys": []
}`, "create")

	resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "rename widget1"},
  "resources": [
    {"type": "fake_widget", "name": "widget1", "op": "modify", "config": {"name": "widget1-renamed"}}
  ],
  "destroys": []
}`, "modify1")

	// The real assertion: modify2's OWN resolve must record a FRESH
	// lookup (reflecting modify1's own applied "name"), not the one
	// frozen at create time -- inspected directly, not just inferred
	// from the ship outcome below, since a lucky hash coincidence could
	// otherwise mask a still-stale lookup.
	intentPath := filepath.Join(dir, "modify2.json")
	if err := os.WriteFile(intentPath, []byte(`{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "rename widget1 again"},
  "resources": [
    {"type": "fake_widget", "name": "widget1", "op": "modify", "config": {"name": "widget1-renamed-again"}}
  ],
  "destroys": []
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedPath := filepath.Join(dir, "modify2-resolved.json")
	if _, err := runUbx(t, env, "resolve", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath); err != nil {
		t.Fatalf("modify2 resolve: %v", err)
	}
	var doc struct {
		Resolution struct {
			Inputs []struct {
				Resource string          `json:"resource"`
				Lookup   json.RawMessage `json:"lookup"`
			} `json:"inputs"`
		} `json:"resolution"`
	}
	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse modify2 resolved plan: %v\nraw: %s", err, raw)
	}
	found := false
	for _, in := range doc.Resolution.Inputs {
		if in.Resource != "payments.fake_widget.widget1" {
			continue
		}
		found = true
		var lookup map[string]string
		if err := json.Unmarshal(in.Lookup, &lookup); err != nil {
			t.Fatalf("parse recorded lookup: %v\nraw: %s", err, in.Lookup)
		}
		if lookup["name"] != "widget1-renamed" {
			t.Fatalf("expected modify2's own recorded lookup to reflect modify1's applied name %q, got %q (lookup frozen at create time again): %s", "widget1-renamed", lookup["name"], in.Lookup)
		}
	}
	if !found {
		t.Fatalf("expected a resolution.inputs entry for payments.fake_widget.widget1, got: %+v", doc.Resolution.Inputs)
	}

	// And the real end-to-end proof: it actually ships.
	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("modify2 accept: %v\noutput: %s", err, acceptOut)
	}
	acceptedID := mustExtractID(t, acceptOut)
	shipOut, err := runUbx(t, env, "ship", acceptedID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("modify2 ship: %v\noutput: %s", err, shipOut)
	}
}
