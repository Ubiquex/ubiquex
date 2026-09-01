package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHistory_EmptyLedger is "ubx history"'s own read-only UBI-20 contract
// at its simplest: an empty chain is a successful listing (exit 0), never
// a "finding" the way "ubx status --drift" renders one -- see
// cli/history.go's own doc comment.
func TestHistory_EmptyLedger(t *testing.T) {
	requireHermeticSandbox(t)
	ledgerDir := t.TempDir()

	out, err := runUbx(t, nil, "history", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx history (empty): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "(empty -- no proposals recorded yet)") {
		t.Fatalf("expected the empty-chain message, got:\n%s", out)
	}
}

// TestHistory_ListsNewestFirstHumanAndJSON ships two real changes and
// confirms both "ubx history" render modes agree with each other and with
// the real ledger: newest head first, matching "ubx why"'s own chain-view
// ordering and git log's own convention (cli/history.go's own doc
// comment), never two independently-assembled views of the same chain.
func TestHistory_ListsNewestFirstHumanAndJSON(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create p"},
  "resources": [
    {"type": "fake_widget", "name": "p", "op": "create", "config": {"name": "widget-p"}}
  ],
  "destroys": []
}`, "batch1")
	secondID := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create q"},
  "resources": [
    {"type": "fake_widget", "name": "q", "op": "create", "config": {"name": "widget-q"}}
  ],
  "destroys": []
}`, "batch2")

	humanOut, err := runUbx(t, nil, "history", "--ledger-dir", ledgerDir, "--full-hashes")
	if err != nil {
		t.Fatalf("ubx history: %v\noutput: %s", err, humanOut)
	}
	firstIdx := strings.Index(humanOut, "create q")
	secondIdx := strings.Index(humanOut, "create p")
	if firstIdx == -1 || secondIdx == -1 {
		t.Fatalf("expected both summaries in human output, got:\n%s", humanOut)
	}
	if firstIdx > secondIdx {
		t.Fatalf("expected \"create q\" (newest) to render before \"create p\" (oldest), got:\n%s", humanOut)
	}
	if !strings.Contains(humanOut, secondID) {
		t.Fatalf("expected the newest proposal's own full hash %q in human output, got:\n%s", secondID, humanOut)
	}

	jsonOut, err := runUbx(t, nil, "history", "--ledger-dir", ledgerDir, "--json")
	if err != nil {
		t.Fatalf("ubx history --json: %v\noutput: %s", err, jsonOut)
	}
	var doc struct {
		Format  int `json:"format"`
		Entries []struct {
			ID          string   `json:"id"`
			Kind        string   `json:"kind"`
			Summary     string   `json:"summary"`
			Creates     int64    `json:"creates"`
			Changes     int64    `json:"changes"`
			Terminates  int64    `json:"terminates"`
			Accepted    bool     `json:"accepted"`
			AcceptedBy  []string `json:"accepted_by,omitempty"`
			AcceptedVia string   `json:"accepted_via,omitempty"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatalf("parse history --json: %v\nraw: %s", err, jsonOut)
	}
	if doc.Format != jsonFormatVersion {
		t.Fatalf("expected format %d, got %d", jsonFormatVersion, doc.Format)
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("expected exactly 2 entries, got %d: %+v", len(doc.Entries), doc.Entries)
	}
	newest, oldest := doc.Entries[0], doc.Entries[1]
	if newest.ID != secondID {
		t.Fatalf("expected entries[0].id to be the newest head %q, got %q", secondID, newest.ID)
	}
	if newest.Summary != "create q" || oldest.Summary != "create p" {
		t.Fatalf("expected summaries [create q, create p] newest first, got [%q, %q]", newest.Summary, oldest.Summary)
	}
	if newest.Kind != "change" || oldest.Kind != "change" {
		t.Fatalf("expected both entries kind \"change\", got [%q, %q]", newest.Kind, oldest.Kind)
	}
	if newest.Creates != 1 || oldest.Creates != 1 {
		t.Fatalf("expected both entries to record 1 create, got [%d, %d]", newest.Creates, oldest.Creates)
	}
	if !newest.Accepted || !oldest.Accepted {
		t.Fatalf("expected both entries accepted (both were shipped), got [%v, %v]", newest.Accepted, oldest.Accepted)
	}
}
