package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The scenario that produced this tool, reproduced end to end.
//
// An MCP session concluded a ledger was empty when it held two
// proposals. It was not wrong about what ubx_status told it: status
// reports the current fold, a created-then-destroyed resource is
// tombstoned and skipped (core/fleet.go), and the fold is genuinely
// empty. There was no tool that could answer what had happened, since
// ubx_why needs an address or proposal ID the caller does not have yet.
//
// createThenDestroy builds exactly that ledger: real proposals, empty
// fold. It seeds through seedShippedWidget (cli/knowndependents_test.go)
// because a real shipped create is required, not an adoption:
// fakeprovider only destroys what it created, so an adopted resource's
// destroy fails its own post-destroy read-back and never tombstones,
// and the empty fold this file is about only exists on the
// create-then-destroy path.
func createThenDestroy(t *testing.T) (ledgerDir, addr string) {
	t.Helper()
	ledgerDir = t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	addr = seedShippedWidget(t, ledgerDir, "payments", "ephemeral")

	termOut, err := runUbx(t, env, "terminate", addr, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("terminate: %v\n%s", err, termOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, termOut)
	if _, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir, "--confirm-destroys", "--yes"); err != nil {
		t.Fatalf("ship destroy: %v", err)
	}
	return ledgerDir, addr
}

func TestMCP_History_AnswersWhatStatusCannot(t *testing.T) {
	ledgerDir, addr := createThenDestroy(t)
	session := connectMCPTestClient(t)

	// The premise: status is genuinely empty here, and correct to be.
	statusRes := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": ledgerDir})
	if statusRes.IsError {
		t.Fatalf("ubx_status: %s", toolTextContent(t, statusRes))
	}
	statusPayload := statusRes.StructuredContent.(map[string]any)
	if resources, _ := statusPayload["resources"].([]any); len(resources) != 0 {
		t.Fatalf("precondition failed: the fold should be empty after a shipped destroy, got %v", resources)
	}

	// The fix to the premise: an empty fold now says how much history
	// exists, so the wrong conclusion is not reachable from one call.
	summary := statusPayload["summary"].(map[string]any)
	total, ok := summary["proposals_total"].(float64)
	if !ok || total < 2 {
		t.Fatalf("an empty fold must report how many proposals the ledger holds, got summary %v", summary)
	}

	// The answer itself.
	histRes := callTool(t, session, "ubx_history", map[string]any{"ledger_dir": ledgerDir})
	if histRes.IsError {
		t.Fatalf("ubx_history: %s", toolTextContent(t, histRes))
	}
	histPayload := histRes.StructuredContent.(map[string]any)
	entries, _ := histPayload["entries"].([]any)
	if len(entries) < 2 {
		t.Fatalf("expected the create and the destroy in history, got %v", histPayload)
	}

	text := toolTextContent(t, histRes)
	if !strings.Contains(text, addr) {
		t.Fatalf("history did not name the resource that no longer exists: %s", text)
	}

	// Newest first, and every entry carries an ID ubx_why takes
	// directly. That pairing is half the reason this is a tool: ubx_why's
	// proposal-ID mode was unreachable through MCP without one.
	first := entries[0].(map[string]any)
	id, _ := first["id"].(string)
	if len(id) != 64 {
		t.Fatalf("expected a full proposal ID on the newest entry, got %q", id)
	}
	whyRes := callTool(t, session, "ubx_why", map[string]any{"query": id, "ledger_dir": ledgerDir})
	if whyRes.IsError {
		t.Fatalf("a proposal ID from ubx_history was not accepted by ubx_why: %s", toolTextContent(t, whyRes))
	}
}

// A limit that shortens the list must say so, and must still report how
// much history exists. A silently short list reads as a complete one.
func TestMCP_History_LimitReportsTruncation(t *testing.T) {
	ledgerDir := t.TempDir()
	for i := 0; i < 3; i++ {
		seedShippedWidget(t, ledgerDir, "payments", fmt.Sprintf("w%d", i))
	}
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_history", map[string]any{"ledger_dir": ledgerDir, "limit": 2})
	if res.IsError {
		t.Fatalf("ubx_history: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	entries, _ := payload["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("limit was not applied, got %d entries", len(entries))
	}
	if payload["truncated"] != true {
		t.Fatalf("a shortened list must say it is shortened: %v", payload)
	}
	if total, _ := payload["total"].(float64); int(total) != 3 {
		t.Fatalf("total must count the whole chain, not the returned subset: %v", payload)
	}

	// An unshortened answer must not claim truncation.
	full := callTool(t, session, "ubx_history", map[string]any{"ledger_dir": ledgerDir})
	fullPayload := full.StructuredContent.(map[string]any)
	if fullPayload["truncated"] != false {
		t.Fatalf("a complete list must not report truncation: %v", fullPayload)
	}
	if fullEntries, _ := fullPayload["entries"].([]any); len(fullEntries) != 3 {
		t.Fatalf("the default limit must not shorten a 3-proposal chain, got %d", len(fullEntries))
	}
}

func TestHistoryToolLimit(t *testing.T) {
	for _, tc := range []struct {
		requested, want int
	}{
		{0, 50},       // unset means the default, never unlimited
		{-1, 50},      // as does nonsense
		{10, 10},      // a real request is honoured
		{500, 500},    // exactly the ceiling
		{100000, 500}, // above it is capped rather than obeyed
	} {
		if got := historyToolLimit(tc.requested); got != tc.want {
			t.Errorf("historyToolLimit(%d) = %d, want %d", tc.requested, got, tc.want)
		}
	}
}

// ubx_history is subject to the same ledger_dir rules as every other
// tool that opens a ledger, rather than quietly reintroducing the empty
// result for a wrong path that those rules removed.
func TestMCP_History_RefusesNonRootLedgerDir(t *testing.T) {
	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_history", map[string]any{
		"ledger_dir": filepath.Join(t.TempDir(), "no-such-directory"),
	})
	if !res.IsError {
		t.Fatalf("expected a path that is not a ubx root to be refused, got: %s", toolTextContent(t, res))
	}
	if !strings.Contains(toolTextContent(t, res), "not a ubx root") {
		t.Fatalf("expected the ubx-root refusal, got: %s", toolTextContent(t, res))
	}
}

// The description is the model's UX for this surface
// (docs/architecture.md), and the specific failure here was tool
// selection: the model committed to ubx_status before any field
// description was in play. ubx_status's own description has to say that
// zero resources is not an empty ledger, and name where to look.
func TestMCP_StatusDescription_PointsAtHistoryForAnEmptyFold(t *testing.T) {
	session := connectMCPTestClient(t)
	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var status, history string
	for _, tool := range res.Tools {
		switch tool.Name {
		case "ubx_status":
			status = tool.Description
		case "ubx_history":
			history = tool.Description
		}
	}
	if status == "" || history == "" {
		t.Fatal("expected both ubx_status and ubx_history to be registered")
	}
	for _, want := range []string{"ubx_history", "proposals_total"} {
		if !strings.Contains(status, want) {
			t.Errorf("ubx_status's description must mention %q so an empty fold has a next step: %s", want, status)
		}
	}
	if !strings.Contains(history, "ubx_why") {
		t.Errorf("ubx_history's description should name ubx_why as the drill-in: %s", history)
	}
}
