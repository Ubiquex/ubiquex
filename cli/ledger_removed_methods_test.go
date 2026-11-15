package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// A ledger written while pr_merge and pr_review existed, before UBI-300
// removed both with the pull-request workflow. Hand-written, because
// nothing can produce either any more, and laid down exactly as the git
// directory store keeps it.
//
// The first entry was accepted by merging a GitHub pull request; the second,
// chained on it, by a native Approve review on a GitLab merge request. Both
// carry every field those methods wrote: platform, merge SHA, PR number,
// proposal file, approvers and, for pr_review, the reviewer's own comment.
const prMergeEntry = `{
  "schema_version": 2, "id": "ID", "stack": "demo", "parent": "PARENT", "kind": "change",
  "intent": {"summary": "queue for the payments pipeline"},
  "delta": {"creates": [{"stack": "demo", "type": "fake_widget", "name": "queue", "config": {"name": "payments-queue"}}]},
  "resolution": {"resolved_at": "2026-08-20T09:00:00Z"},
  "cost_delta": {"monthly_usd": 0},
  "blast_radius": {"creates": 1, "modifies": 0, "destroys": 0},
  "acceptance": {
    "method": "pr_merge", "platform": "github", "merge_sha": "8c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d",
    "pr_number": 42, "proposal_file": "proposals/queue.json",
    "approvers": ["roozbeh", "alice"], "accepted_at": "2026-08-20T09:30:00Z"
  },
  "status": "accepted"
}`

const prReviewEntry = `{
  "schema_version": 2, "id": "ID", "stack": "demo", "parent": "PARENT", "kind": "change",
  "intent": {"summary": "dead-letter queue beside it"},
  "delta": {"creates": [{"stack": "demo", "type": "fake_widget", "name": "dlq", "config": {"name": "payments-dlq"}}]},
  "resolution": {"resolved_at": "2026-08-21T09:00:00Z"},
  "cost_delta": {"monthly_usd": 0},
  "blast_radius": {"creates": 1, "modifies": 0, "destroys": 0},
  "acceptance": {
    "method": "pr_review", "platform": "gitlab", "pr_number": 7, "proposal_file": "proposals/dlq.json",
    "approvers": ["bob"], "review_comment": "LGTM, matches the runbook", "accepted_at": "2026-08-21T09:10:00Z"
  },
  "status": "accepted"
}`

// writeRemovedMethodLedger lays both entries down as ledger/proposals plus
// the head file, filling each ID from its content hash and chaining the
// second on the first. Returns the ledger dir and both IDs.
func writeRemovedMethodLedger(t *testing.T) (dir, mergeID, reviewID string) {
	t.Helper()
	dir = t.TempDir()
	for _, sub := range []string{filepath.Join("ledger", "proposals"), ".ubx"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	parent := ""
	var ids []string
	for _, entry := range []string{prMergeEntry, prReviewEntry} {
		entry = strings.Replace(entry, `"parent": "PARENT"`, `"parent": "`+parent+`"`, 1)
		var p core.Proposal
		if err := json.Unmarshal([]byte(entry), &p); err != nil {
			t.Fatal(err)
		}
		id, err := core.Hash(&p)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "ledger", "proposals", id+".prop.json"), strings.Replace(entry, `"id": "ID"`, `"id": "`+id+`"`, 1))
		ids = append(ids, id)
		parent = id
	}
	writeFile(t, filepath.Join(dir, ".ubx", "ledger.lock"), parent+"\n")
	return dir, ids[0], ids[1]
}

// TestLedger_RemovedAcceptanceMethodsStillRead is UBI-300's own criterion:
// a ledger holding pr_merge and pr_review acceptances still reads, keeps
// every field those methods recorded, verifies, and is reported as from a
// removed method by every command that shows acceptance -- never as
// verified, never failed, never a crash.
func TestLedger_RemovedAcceptanceMethodsStillRead(t *testing.T) {
	dir, mergeID, reviewID := writeRemovedMethodLedger(t)

	l := core.Open(dir)
	merge, err := l.Read(mergeID)
	if err != nil {
		t.Fatalf("Read pr_merge entry: %v", err)
	}
	a := merge.Acceptance
	if a.Method != "pr_merge" || a.Platform != "github" || a.MergeSHA != "8c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d" ||
		a.PRNumber != 42 || a.ProposalFile != "proposals/queue.json" || len(a.Approvers) != 2 {
		t.Fatalf("pr_merge acceptance lost fields on read: %+v", a)
	}
	review, err := l.Read(reviewID)
	if err != nil {
		t.Fatalf("Read pr_review entry: %v", err)
	}
	if r := review.Acceptance; r.Method != "pr_review" || r.ReviewComment != "LGTM, matches the runbook" || r.Approvers[0] != "bob" {
		t.Fatalf("pr_review acceptance lost fields on read: %+v", r)
	}
	if res, err := core.VerifyChain(l); err != nil || !res.ChainIntact {
		t.Fatalf("chain no longer verifies: %v %+v", err, res)
	}

	// ubx verify: intact, exit 0, both reported as from a removed method.
	out, err := runUbx(t, nil, "verify", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx verify must not fail on a removed method's records: %v\n%s", err, out)
	}
	for _, want := range []string{
		"chain: intact",
		"2 from a removed method (not re-verified)",
		mergeID + " (pr_merge): NOT RE-VERIFIED -- pr_merge, approval derived from a merged pull request: a removed acceptance method",
		reviewID + " (pr_review): NOT RE-VERIFIED -- pr_review, approval derived from a pull request review: a removed acceptance method",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("ubx verify output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FAILED") || strings.Contains(out, "unrecognized") {
		t.Fatalf("a removed method must read as history, not as a failure or an unknown:\n%s", out)
	}

	var payload struct {
		Acceptance struct {
			Removed int `json:"removed_method"`
			Checks  []struct {
				Method        string `json:"method"`
				OK            bool   `json:"ok"`
				RemovedMethod bool   `json:"removed_method"`
			} `json:"checks"`
		} `json:"acceptance"`
	}
	jsonOut, err := runUbx(t, nil, "verify", "--ledger-dir", dir, "--json")
	if err != nil {
		t.Fatalf("ubx verify --json: %v\n%s", err, jsonOut)
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, jsonOut)
	}
	if payload.Acceptance.Removed != 2 || len(payload.Acceptance.Checks) != 2 {
		t.Fatalf("verify --json acceptance = %+v, want 2 removed-method checks", payload.Acceptance)
	}
	for _, c := range payload.Acceptance.Checks {
		if c.OK || !c.RemovedMethod {
			t.Fatalf("check %+v: want removed_method and not ok", c)
		}
	}

	// ubx why: the record, as recorded, with the method marked removed.
	out, err = runUbx(t, nil, "why", mergeID, "--ledger-dir", dir, "--full-hashes")
	if err != nil {
		t.Fatalf("ubx why: %v\n%s", err, out)
	}
	if !strings.Contains(out, "via pr_merge (removed method)") || !strings.Contains(out, "roozbeh") {
		t.Fatalf("ubx why does not show the historical acceptance as a removed method:\n%s", out)
	}
	out, err = runUbx(t, nil, "why", "--json", reviewID, "--ledger-dir", dir)
	if err != nil || !strings.Contains(out, `"review_comment": "LGTM, matches the runbook"`) || !strings.Contains(out, `"method": "pr_review"`) {
		t.Fatalf("ubx why --json lost the pr_review record: %v\n%s", err, out)
	}

	// ubx history and ubx stats: shown, and marked.
	out, err = runUbx(t, nil, "history", "--stack", "demo", "--ledger-dir", dir)
	if err != nil || !strings.Contains(out, "via pr_review (removed method)") || !strings.Contains(out, "via pr_merge (removed method)") {
		t.Fatalf("ubx history: %v\n%s", err, out)
	}
	out, err = runUbx(t, nil, "stats", "--ledger-dir", dir)
	if err != nil || !strings.Contains(out, "pr_merge (removed method): 1") || !strings.Contains(out, "pr_review (removed method): 1") {
		t.Fatalf("ubx stats: %v\n%s", err, out)
	}
}
