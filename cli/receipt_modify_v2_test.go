package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPlan_ModifyReceipt_HeaderIndentFormat is UBI-88's own end-to-end
// receipt test, against a real fakeprovider subprocess (never hand-seeded
// state): a modify block must render as ONE header line -- "~ <address>
// change", the same "+/~/- <address> <op>" shape create/destroy already
// use -- with its changed attributes indented beneath it at the SAME
// extra indent renderCreates already uses, and an attribute the ledger
// recorded as null/its own zero value that the modify's own drafted
// config simply never mentions must never leak as spurious "-> (absent)"
// noise alongside the real change. Before this session, a modify instead
// rendered as a flat "~ change: <address>: <path>: <before> -> <after>"
// line per attribute, with no header/body structure, and the omitted-
// attribute noise leaked unfiltered (core/resolver's OpModify diff never
// called core.FilterNormalizationNoise at all).
func TestPlan_ModifyReceipt_HeaderIndentFormat(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	seedPath := filepath.Join(ledgerDir, "seed.json")
	writeIntentFile(t, seedPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "platform",
		"intent":         map[string]interface{}{"summary": "seed one widget, UBI-88 modify receipt test"},
		"resources": []map[string]interface{}{
			// "tags" deliberately left unset -- fakeprovider's own schema
			// (provider/internal/fakeprovider) records an unset Optional
			// map attribute back as "{}", not as the config's own absence
			// -- the exact live shape that used to leak as "tags: {} ->
			// (absent)" noise once this test's own later modify omits it
			// too.
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})
	resolvedPath := filepath.Join(ledgerDir, "seed-resolved.json")
	if _, err := runUbx(t, env, "resolve", seedPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath); err != nil {
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

	// The real modify: "name" genuinely changes, "tags" is never
	// mentioned at all (an ordinary, legitimate omission -- config is a
	// partial document, not full state).
	modifyPath := filepath.Join(ledgerDir, "modify.json")
	writeIntentFile(t, modifyPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "platform",
		"intent":         map[string]interface{}{"summary": "rename widget1"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "modify", "config": map[string]interface{}{"name": "widget1-renamed"}},
		},
	})

	out, err := runUbx(t, env, "plan", modifyPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, out)
	}

	const header = "~ platform.fake_widget.widget1 change"
	headerIdx := strings.Index(out, header)
	if headerIdx < 0 {
		t.Fatalf("expected the modify header %q, got:\n%s", header, out)
	}

	const attrLine = "    name: \"widget1\" -> \"widget1-renamed\""
	attrIdx := strings.Index(out, attrLine)
	if attrIdx < 0 {
		t.Fatalf("expected the indented attribute diff %q (4-space extra indent, matching renderCreates' own attrIndent), got:\n%s", attrLine, out)
	}
	if attrIdx < headerIdx {
		t.Fatalf("expected the attribute diff after its own header, got:\n%s", out)
	}

	// The old flat format must be gone entirely.
	if strings.Contains(out, "~ change:") {
		t.Fatalf("expected the old flat \"~ change: <address>: ...\" format to be gone, got:\n%s", out)
	}

	// The null/zero-value<->absent normalization noise must never leak.
	if strings.Contains(out, "-> (absent)") {
		t.Fatalf("expected no null/zero-value<->absent normalization noise in the diff, got:\n%s", out)
	}
	if strings.Contains(out, "tags") {
		t.Fatalf("expected the untouched, noise-only \"tags\" attribute to be filtered out of the diff entirely, got:\n%s", out)
	}

	// Vocabulary: "change(s)"/"terminate(s)", not "modify(ies)"/"destroy(s)".
	if !strings.Contains(out, "delta: +0 create(s), ~1 change(s), -0 terminate(s)") {
		t.Fatalf("expected the change/terminate delta vocabulary, got:\n%s", out)
	}
	if strings.Contains(out, "modify(ies)") || strings.Contains(out, "destroy(s)") {
		t.Fatalf("expected no lingering modify(ies)/destroy(s) wording in the receipt, got:\n%s", out)
	}
}
