package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// TestTerminate_ScanCleanButShipRefused_NormalizationNoise is UBI-63
// session 5's own end-to-end reproduction, through the real `ubx`
// commands (not just Go-level executor calls), of a live divergence that
// blocked the founder's actual cleanup: `ubx scan` on a resource reported
// clean seconds before `ubx terminate` + `ubx ship` on the EXACT SAME,
// untouched resource refused with "destroy target drifted since it was
// signed away" -- same resource, same real state, back to back, nothing
// touched in between.
//
// The ledger's own recorded baseline is hand-seeded (bypassing `ubx
// scan`'s own adopt path, which -- through ok-v6's echoWidgetState --
// would immediately normalize tags:null to tags:{} on read, making the
// exact "recorded null, later {}" shape this bug needs impossible to
// construct through the normal adopt flow) with tags recorded as null,
// the same real provider-read-nondeterminism shape as bug 4's own
// original repro. `ubx scan`, `ubx terminate`, and `ubx ship` are then
// driven as the real CLI commands a founder actually types.
func TestTerminate_ScanCleanButShipRefused_NormalizationNoise(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "role-1"}

	ledger := core.Open(ledgerDir)
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack,
		"type":  addr.Type,
		"name":  addr.Name,
		"state": json.RawMessage(`{"id":"role-1","name":"role-1","tags":null}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	seedHash, err := core.ObservedHash(json.RawMessage(`{"id":"role-1","name":"role-1","tags":null}`))
	if err != nil {
		t.Fatal(err)
	}
	adopt := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         addr.Stack,
		Kind:          core.KindAdoption,
		Intent:        core.Intent{Summary: "hand-seeded adopt"},
		Delta:         core.Delta{Creates: []json.RawMessage{node}},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: seedHash, Lookup: json.RawMessage(`{"id":"role-1","name":"role-1"}`)},
			},
		},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		Status:    core.StatusDraft,
	}
	if _, err := core.Accept(ledger, adopt); err != nil {
		t.Fatalf("hand-seed adopt: %v", err)
	}

	// 1. `ubx scan`: a fresh read (ok-v6 auto-fills tags:null -> {}) must
	// report clean -- fix 1's own normalization.
	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "role-1",
		"--lookup", `{"id":"role-1","name":"role-1"}`,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx scan: expected clean (exit 0), got err=%v\noutput: %s", err, scanOut)
	}
	if !strings.Contains(scanOut, "no drift:") {
		t.Fatalf("expected a 'no drift' verdict, got: %s", scanOut)
	}

	// 2. `ubx terminate`, on the exact same, untouched resource,
	// immediately after.
	termOut, err := runUbx(t, env, "terminate", addr.String(), "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx terminate: %v\noutput: %s", err, termOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, termOut)

	// 3. `ubx ship --confirm-destroys --yes`: this is where the bug bit
	// -- must succeed, not refuse the exact resource scan just confirmed
	// was clean.
	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--confirm-destroys", "--yes")
	if err != nil {
		t.Fatalf("ubx ship: expected success, got err=%v\noutput: %s\n\n"+
			"this is the exact divergence: scan said clean, ship refused the same untouched resource", err, shipOut)
	}
	if strings.Contains(shipOut, "drifted since it was signed away") {
		t.Fatalf("ship output still shows the false-positive refusal: %s", shipOut)
	}
}
