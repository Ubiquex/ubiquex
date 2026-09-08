package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// ubx ship and the [providers] table.
//
// ccd8b8d3 split .ubx/config's single provider table into
// [thirdparty_providers] (Terraform registry sources) and [providers]
// (ubx's own dynamic-provider-backed sources), and its own commit
// message claimed the split made a dynamic source "usable for real infra
// via ubx resolve/ubx ship for the first time". That was true of resolve
// and false of ship: the routing inside providerPool.Get was rewired, but
// ship's own "does this stack use a pool" gate still tested
// [thirdparty_providers] alone, so a stack declaring nothing but
// [providers] never reached the pool at all. d2d235ac finished the
// migration eight hours later across status/drift/scanfleet/scanall/
// scandiscover, scoped to the five callers of
// declaredProvidersForInference. ship builds a pool but does no
// inference, so it sat outside both commits and was swept by neither.
//
// The user-visible result: `ubx init --dynamic-source` writes a
// [providers] table, `ubx plan` reads it and resolves correctly, and
// `ubx ship` then demanded a --provider or --source the stack had no
// reason to own. The documented flow could plan and could not ship.

// TestShip_DynamicProvidersTable_ReachesThePool proves a [providers]-only
// stack now takes ship's pool branch.
//
// It asserts on which failure happens, not on a success: launching a real
// ubx-provider-dynamic is neither hermetic nor something this suite should
// ever do. Before the fix ship refused at the flag check, having never
// looked at the table. After it, the table is what ship routes through,
// and the run gets as far as the dynamic provider itself.
func TestShip_DynamicProvidersTable_ReachesThePool(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// The plan is built against fakeprovider with no config in scope, so
	// the proposal itself is hermetic and ordinary. Only the ship step
	// runs under the [providers] table.
	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	setUpConfig(t, "[providers.aws]\nsource = \"ubiquex/aws\"\n")

	shipOut, shipErr := runUbx(t, env, "ship", hash, "--ledger-dir", ledgerDir, "--yes")
	// SilenceErrors means the refusal lands in err, not in the buffer.
	// Both are searched, so neither this test nor the one below can pass
	// by looking at the wrong half and finding nothing.
	shipAll := shipOut + errText(shipErr)

	// The regression itself: refusing for want of a flag, on a stack whose
	// provider table says exactly which provider to use.
	if strings.Contains(shipAll, "either --provider or --source") {
		t.Fatalf("ship refused for want of a provider flag despite a [providers] table -- the gate never read it: %s", shipOut)
	}
	// And it must not have silently fallen through to the registry either,
	// which is what --source ubiquex/aws did: ubiquex is not a Terraform
	// registry namespace, so that route can only ever 404.
	if strings.Contains(shipAll, "provider release not found") {
		t.Fatalf("ship sent a dynamic source to the Terraform registry: %s", shipOut)
	}
}

// TestShip_NoProviderRoute_RefusesBeforeAccepting is the ordering half.
//
// ship can accept a local plan inline, and that acceptance appends to an
// append-only ledger. Provider resolution used to happen roughly seventy
// lines after that append, so a stack with no reachable provider got its
// proposal durably accepted and only then refused, leaving an
// accepted-but-unshippable record that nothing in the system can retract.
//
// The check needs no network and no provider, so it now runs before the
// signing moment. This test pins the ledger being untouched, not just the
// error text: the error was always correct, it was the write before it
// that was the bug.
func TestShip_NoProviderRoute_RefusesBeforeAccepting(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	// No config at all, and no --provider: ship has no route to a provider
	// by any of its three mechanisms.
	setUpConfig(t, "")

	shipOut, shipErr := runUbx(t, env, "ship", hash, "--ledger-dir", ledgerDir, "--yes")
	if shipErr == nil {
		t.Fatalf("expected ship to refuse with no provider route at all, got success: %s", shipOut)
	}
	shipAll := shipOut + errText(shipErr)
	if !strings.Contains(shipAll, "either --provider or --source") {
		t.Fatalf("expected the missing-provider refusal, got: %s", shipAll)
	}

	// The point of the whole change: nothing was signed on the way to that
	// refusal.
	if strings.Contains(shipAll, "via local plan") {
		t.Fatalf("ship accepted the plan before discovering it had no provider: %s", shipAll)
	}
	historyOut, herr := runUbx(t, env, "history", "--stack", "payments", "--ledger-dir", ledgerDir)
	if herr != nil {
		t.Fatalf("ubx history: %v\noutput: %s", herr, historyOut)
	}
	if strings.Contains(historyOut, hash) {
		t.Fatalf("the refused proposal was appended to the ledger anyway:\n%s", historyOut)
	}
}

// errText renders an error for assertion, or "" for nil. runUbx's root
// command sets SilenceErrors, so a refusal never reaches the output
// buffer -- a test that only searched stdout would pass on any error at
// all, including the one it was written to rule out.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
