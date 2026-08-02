package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// shipFiveFakeWidgets is UBI-85's own shared adversarial-row fixture: five
// real fake_widget resources, resolved/accepted/shipped through the
// genuine pipeline (never hand-seeded into the ledger) against a real
// fakeprovider subprocess, matching the founder's own platform.md repro
// shape (five resources, already shipped, then re-planned). Returns the
// ledger dir every row's own `ubx plan --from-doc` runs against.
func shipFiveFakeWidgets(t *testing.T) string {
	t.Helper()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "seed-intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "platform",
		"intent":         map[string]interface{}{"summary": "five widgets, seeding UBI-85's own adversarial rows"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1", "tags": map[string]string{"env": "prod"}}},
			{"type": "fake_widget", "name": "widget2", "op": "create", "config": map[string]interface{}{"name": "widget2", "tags": map[string]string{"env": "prod"}}},
			{"type": "fake_widget", "name": "widget3", "op": "create", "config": map[string]interface{}{"name": "widget3", "tags": map[string]string{"env": "prod"}}},
			{"type": "fake_widget", "name": "widget4", "op": "create", "config": map[string]interface{}{"name": "widget4", "tags": map[string]string{"env": "prod"}}},
			{"type": "fake_widget", "name": "widget5", "op": "create", "config": map[string]interface{}{"name": "widget5", "tags": map[string]string{"env": "prod"}}},
		},
	})

	resolvedPath := filepath.Join(ledgerDir, "seed-resolved.json")
	if _, err := runUbx(t, env, "resolve", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath); err != nil {
		t.Fatalf("seed resolve: %v", err)
	}
	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("seed accept: %v", err)
	}
	id := mustExtractID(t, acceptOut)
	if _, err := runUbx(t, env, "ship", id, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes"); err != nil {
		t.Fatalf("seed ship: %v", err)
	}
	return ledgerDir
}

// planFromDocDeltaLine extracts plan's own "delta: +N create(s), ~N
// change(s), -N terminate(s)" line for exact assertions -- substring
// checks on the whole receipt would also match, e.g., an unrelated "1"
// somewhere else in the output.
func planFromDocDeltaLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "delta:") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no \"delta:\" line found in plan output:\n%s", out)
	return ""
}

// TestPlanFromDoc_UBI85_UnchangedDoc_EmptyDelta is the ticket's own first
// adversarial row: every resource the document describes is byte-
// identical to what's already shipped -- must produce a genuine no-op
// (empty delta), never 5 fresh creates (the original bug) and never 5
// modifies-to-the-same-value. Also confirms the wiring itself: the fake
// adapter's own recorded DraftRequest.KnownResources must actually carry
// all 5 already-shipped addresses -- proof this session's fix reads real
// ledger state, not just that a correctly-behaving response is accepted.
func TestPlanFromDoc_UBI85_UnchangedDoc_EmptyDelta(t *testing.T) {
	ledgerDir := shipFiveFakeWidgets(t)

	fake := &fakeIntentAdapter{draft: `{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": "platform",
	  "intent": {
	    "summary": "No changes: all 5 resources already match their recorded state",
	    "assumptions": [], "defaults": [], "questions": []
	  },
	  "resources": [],
	  "destroys": []
	}`}
	withBuildIntentAdapter(t, fake)

	docPath := filepath.Join(t.TempDir(), "platform.md")
	writeFile(t, docPath, "# Platform\n\nFive widgets, unchanged from what's already shipped.")

	out, err := runUbx(t, nil, "plan", "--from-doc", docPath, "--stack", "platform", "--ledger-dir", ledgerDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx plan --from-doc: %v\noutput: %s", err, out)
	}

	if got := planFromDocDeltaLine(t, out); got != "delta: +0 create(s), ~0 change(s), -0 terminate(s)" {
		t.Fatalf("delta = %q, want a genuine empty no-op", got)
	}

	if len(fake.lastReq.KnownResources) != 5 {
		t.Fatalf("adapter received KnownResources with %d entries, want 5: %+v", len(fake.lastReq.KnownResources), fake.lastReq.KnownResources)
	}
	for _, name := range []string{"widget1", "widget2", "widget3", "widget4", "widget5"} {
		key := "fake_widget." + name
		state, ok := fake.lastReq.KnownResources[key]
		if !ok {
			t.Errorf("KnownResources missing %q, got keys %v", key, knownResourceKeys(fake.lastReq.KnownResources))
			continue
		}
		if !strings.Contains(string(state), `"`+name+`"`) {
			t.Errorf("KnownResources[%q] = %s, expected it to name %q somewhere in its own recorded state", key, state, name)
		}
	}
}

// TestPlanFromDoc_UBI85_OneOfFiveChanged_ExactlyOneModify is the ticket's
// own second adversarial row -- the founder's own literal repro shape:
// 4 resources unchanged, 1 resource's own attribute genuinely differs.
// Must produce +0~1-0 exactly, with a CLEAN before/after diff (only the
// attribute that actually changed), never a full resource redump.
func TestPlanFromDoc_UBI85_OneOfFiveChanged_ExactlyOneModify(t *testing.T) {
	ledgerDir := shipFiveFakeWidgets(t)

	fake := &fakeIntentAdapter{draft: `{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": "platform",
	  "intent": {
	    "summary": "widget3's own tags.env changed from prod to staging; 4 others unchanged",
	    "assumptions": [], "defaults": [], "questions": []
	  },
	  "resources": [
	    {"type": "fake_widget", "name": "widget3", "op": "modify", "config": "{\"id\":\"computed-id\",\"name\":\"widget3\",\"tags\":{\"env\":\"staging\"}}"}
	  ],
	  "destroys": []
	}`}
	withBuildIntentAdapter(t, fake)

	docPath := filepath.Join(t.TempDir(), "platform.md")
	writeFile(t, docPath, "# Platform\n\nwidget3 now runs in staging.")

	out, err := runUbx(t, nil, "plan", "--from-doc", docPath, "--stack", "platform", "--ledger-dir", ledgerDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx plan --from-doc: %v\noutput: %s", err, out)
	}

	if got := planFromDocDeltaLine(t, out); got != "delta: +0 create(s), ~1 change(s), -0 terminate(s)" {
		t.Fatalf("delta = %q, want exactly +0~1-0", got)
	}
	if !strings.Contains(out, `tags.env: "prod" -> "staging"`) {
		t.Errorf("expected a clean tags.env before/after diff line, got:\n%s", out)
	}
	// The clean-diff requirement, proven negatively: "name"/"id" must
	// NEVER appear as their own changed-attribute line -- DiffAttributes
	// (core/state.go) already drops any attribute whose before/after are
	// equal, so reproducing them unchanged in the modify's own full
	// config (as this fake's draft, matching the system prompt's own
	// instruction, does) must net out to nothing extra in the rendered
	// diff, not a full resource redump.
	if strings.Contains(out, "name:") || strings.Contains(out, "id:") {
		t.Errorf("expected only tags.env in the diff (a clean diff, not a full redump), got:\n%s", out)
	}
}

// TestPlanFromDoc_UBI85_GenuineNewSixthResource_OnlyOneCreate is the
// ticket's own third adversarial row: the document adds a real, brand-new
// 6th resource alongside 5 unchanged ones -- must produce +1 create only,
// never 6 creates (the original bug re-triggering for the 5 that didn't
// change) and never a modify for any of the unchanged 5.
func TestPlanFromDoc_UBI85_GenuineNewSixthResource_OnlyOneCreate(t *testing.T) {
	ledgerDir := shipFiveFakeWidgets(t)

	fake := &fakeIntentAdapter{draft: `{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": "platform",
	  "intent": {
	    "summary": "a genuine new 6th widget added; the existing 5 are unchanged",
	    "assumptions": [], "defaults": [], "questions": []
	  },
	  "resources": [
	    {"type": "fake_widget", "name": "widget6", "op": "create", "config": "{\"name\":\"widget6\",\"tags\":{\"env\":\"prod\"}}"}
	  ],
	  "destroys": []
	}`}
	withBuildIntentAdapter(t, fake)

	docPath := filepath.Join(t.TempDir(), "platform.md")
	writeFile(t, docPath, "# Platform\n\nAdd a new sixth widget.")

	out, err := runUbx(t, nil, "plan", "--from-doc", docPath, "--stack", "platform", "--ledger-dir", ledgerDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx plan --from-doc: %v\noutput: %s", err, out)
	}

	if got := planFromDocDeltaLine(t, out); got != "delta: +1 create(s), ~0 change(s), -0 terminate(s)" {
		t.Fatalf("delta = %q, want exactly +1~0-0", got)
	}
	if !strings.Contains(out, "widget6") {
		t.Errorf("expected the new resource's own address in the receipt, got:\n%s", out)
	}
}

// TestPlanFromDoc_UBI85_DocDropsAResource_BlockingQuestionNeverDestroy is
// the ticket's own fourth adversarial row: the document simply stops
// describing a resource the ledger still tracks. Must NEVER auto-destroy
// (destroys stay explicit, per the founding design -- also restated
// directly in the schema's own "destroys" field description and this
// session's new system-prompt section) -- must instead surface a visible,
// blocking Question naming the address.
func TestPlanFromDoc_UBI85_DocDropsAResource_BlockingQuestionNeverDestroy(t *testing.T) {
	ledgerDir := shipFiveFakeWidgets(t)

	fake := &fakeIntentAdapter{draft: `{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": "platform",
	  "intent": {
	    "summary": "widget5 is no longer described in this document",
	    "assumptions": [], "defaults": [],
	    "questions": [
	      {"text": "the ledger still tracks platform.fake_widget.widget5, but this document no longer describes it -- run ubx terminate explicitly to remove it if that is truly intended", "affects": ["fake_widget.widget5"], "blocking": true}
	    ]
	  },
	  "resources": [],
	  "destroys": []
	}`}
	withBuildIntentAdapter(t, fake)

	docPath := filepath.Join(t.TempDir(), "platform.md")
	writeFile(t, docPath, "# Platform\n\nFour widgets now (widget5's own paragraph was deleted).")

	out, err := runUbx(t, nil, "plan", "--from-doc", docPath, "--stack", "platform", "--ledger-dir", ledgerDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx plan --from-doc: %v\noutput: %s", err, out)
	}

	if got := planFromDocDeltaLine(t, out); got != "delta: +0 create(s), ~0 change(s), -0 terminate(s)" {
		t.Fatalf("delta = %q, want zero destroys -- a dropped description must never auto-destroy", got)
	}
	if !strings.Contains(out, "Questions") || !strings.Contains(out, "widget5") {
		t.Errorf("expected a rendered blocking Question naming widget5, got:\n%s", out)
	}
	if !strings.Contains(out, "[blocking") {
		t.Errorf("expected the question to render as blocking, got:\n%s", out)
	}
}

func knownResourceKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
