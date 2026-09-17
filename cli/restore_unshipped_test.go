package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

const unshippedV1Intent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "v1"},
  "resources": [
    {"type": "fake_widget", "name": "q", "op": "create", "config": {"name": "widget-v1"}}
  ]
}`

const unshippedV2Intent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "v2"},
  "resources": [
    {"type": "fake_widget", "name": "q", "op": "modify", "config": {"name": "widget-v2"}}
  ]
}`

// TestRestore_EmptyDeltaWhenTheChangeNeverShipped pins the distinction
// between the two ways a restore can propose nothing.
//
// A restore that silently does nothing is the worst failure this feature
// can have, so "it proposed nothing" must be traceable to a reason rather
// than trusted. There are two reasons and only one is a bug:
//
//   - the change it would undo never shipped, so the ledger still holds
//     the target head's own shape and there is genuinely nothing to move
//   - the change DID ship and restore failed to notice, which would be
//     the bug
//
// This covers the first. TestRestore_ExactState_CreateModifyDestroy_
// RealFakeProvider covers the second by asserting a real modify when the
// change did ship, so the pair distinguishes them.
//
// The mechanism is FoldState's own shipped gating: an accepted but never
// shipped kind:change modify does not fold into current state, so restore
// compares the target head against a present that never moved.
func TestRestore_EmptyDeltaWhenTheChangeNeverShipped(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// A declared blueprint this head never used, so the reconciliation
	// has something to report. Without it the receipt correctly says
	// nothing, and the note under test would never fire: the note exists
	// for the case where a difference IS reported beside an empty delta,
	// which is what reads as a contradiction.
	writeStackConfigWithBlueprints(t, ledgerDir, "payments", map[string]string{
		"unused-bp": "oci://ghcr.io/ubx-blueprints/unused-bp:v1.0.0",
	})

	targetHead := resolveAcceptShip(t, dir, ledgerDir, env, unshippedV1Intent, "v1")

	// Resolve and ACCEPT the v2 change, but never ship it. The proposal
	// is in the ledger; its effect is not.
	intentPath := filepath.Join(dir, "v2.json")
	if err := os.WriteFile(intentPath, []byte(unshippedV2Intent), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedPath := filepath.Join(dir, "v2-resolved.json")
	if out, err := runUbx(t, env, "resolve", intentPath, "--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir, "--out", resolvedPath, "--timeout", "60s"); err != nil {
		t.Fatalf("resolve v2: %v\n%s", err, out)
	}
	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("accept v2: %v\n%s", err, acceptOut)
	}

	restoreOut, err := runUbx(t, env, "restore", targetHead, "--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx restore: %v\n%s", err, restoreOut)
	}
	restoreID := extractProposalHash(t, restoreOut)
	raw, err := readPlanFileForTest(t, ledgerDir, restoreID)
	if err != nil {
		t.Fatal(err)
	}
	doc := parseRestoreDoc(t, raw)

	if n := len(doc.Delta.Creates) + len(doc.Delta.Modifies) + len(doc.Delta.Destroys); n != 0 {
		t.Fatalf("an unshipped change left nothing to restore, so the delta must be empty, got %d entries:\n%s", n, raw)
	}
	// And the receipt SAYS the delta is empty on purpose, through the real
	// command rather than the helper. Without this, unwiring the note from
	// the receipt leaves every other assertion here green while a user
	// sees an empty delta and no explanation, which is the failure this
	// whole change is about.
	if !strings.Contains(restoreOut, "empty even though a declaration differs") {
		t.Errorf("the receipt does not explain its own empty delta:\n%s", restoreOut)
	}

	// And the same restore against a SHIPPED v2 does propose the change
	// back, which is what makes the empty delta above meaningful rather
	// than a restore that never proposes anything.
	changeID := mustExtractID(t, acceptOut)
	if shipOut, err := runUbx(t, env, "ship", changeID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ship v2: %v\n%s", err, shipOut)
	}
	restoreOut2, err := runUbx(t, env, "restore", targetHead, "--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx restore after ship: %v\n%s", err, restoreOut2)
	}
	raw2, err := readPlanFileForTest(t, ledgerDir, extractProposalHash(t, restoreOut2))
	if err != nil {
		t.Fatal(err)
	}
	doc2 := parseRestoreDoc(t, raw2)
	if len(doc2.Delta.Modifies) != 1 {
		t.Fatalf("once the change shipped, restore must propose moving it back, got:\n%s", raw2)
	}
	if !strings.Contains(string(raw2), "widget-v1") {
		t.Errorf("the restore does not move the resource back to the target head's value:\n%s", raw2)
	}
}

// parseRestoreDoc decodes a saved restore plan. Separate from
// parseRestorePlan, which also re-extracts the hash from receipt text
// this test already has.
func parseRestoreDoc(t *testing.T, raw []byte) restorePlanDoc {
	t.Helper()
	var doc restorePlanDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse restore plan: %v\nraw: %s", err, raw)
	}
	return doc
}

// TestWriteNothingToRestoreNote_ExplainsTheApparentContradiction: a
// reported difference beside an empty delta reads as a contradiction, and
// a reader who concludes the restore is broken is drawing the reasonable
// inference from what they were shown.
func TestWriteNothingToRestoreNote_ExplainsTheApparentContradiction(t *testing.T) {
	rec := blueprintReconcile{UsedAll: 1, Differs: []blueprintDiff{
		{Name: "rev-bp", Declared: "oci://x/rev:v2", Used: "oci://x/rev:v1", FromLock: true},
	}}

	var empty bytes.Buffer
	writeNothingToRestoreNote(&empty, &core.Proposal{}, rec)
	for _, want := range []string{
		"empty even though a declaration differs",
		"both are", // and that both are correct
		"nothing to move back",
		"never shipped", // the usual cause, named without being asserted
	} {
		if !strings.Contains(empty.String(), want) {
			t.Errorf("the note does not say %q:\n%s", want, empty.String())
		}
	}

	// A restore that actually proposes something needs no explanation.
	var withDelta bytes.Buffer
	writeNothingToRestoreNote(&withDelta, &core.Proposal{
		Delta: core.Delta{Modifies: []core.Modification{{Target: core.Address{Stack: "s", Type: "t", Name: "n"}}}},
	}, rec)
	if withDelta.String() != "" {
		t.Errorf("a non-empty restore explained itself anyway:\n%s", withDelta.String())
	}

	// And neither does an empty restore with nothing else to explain.
	var quiet bytes.Buffer
	writeNothingToRestoreNote(&quiet, &core.Proposal{}, blueprintReconcile{})
	if quiet.String() != "" {
		t.Errorf("an ordinary empty restore gained a note:\n%s", quiet.String())
	}
}
