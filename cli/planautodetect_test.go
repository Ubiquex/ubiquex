package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAutodetectMedium_SingleDoc_Detected is docs/cli-output-spec.md
// §v2's own bare "ubx plan" auto-detection (UBI-63): exactly one .md
// authoring doc in the directory is detected as a --from-doc candidate.
func TestAutodetectMedium_SingleDoc_Detected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "platform.md"), "# Platform\n\nA queue.")

	found, err := autodetectMedium(dir)
	if err != nil {
		t.Fatalf("autodetectMedium: %v", err)
	}
	if len(found) != 1 || found[0].flag != "--from-doc" {
		t.Fatalf("expected exactly one --from-doc candidate, got %+v", found)
	}
}

// TestAutodetectMedium_ReadmeNeverFalsePositive is the founder's own
// explicit rule from docs/cli-output-spec.md §v2: "README.md-class
// files must not false-positive." A directory with only a README (and
// a few other well-known project-meta markdown files) has NO detected
// medium candidates.
func TestAutodetectMedium_ReadmeNeverFalsePositive(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"README.md", "CHANGELOG.md", "LICENSE", "CONTRIBUTING.md"} {
		writeFile(t, filepath.Join(dir, name), "not an authoring document")
	}

	found, err := autodetectMedium(dir)
	if err != nil {
		t.Fatalf("autodetectMedium: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected README/CHANGELOG/LICENSE/CONTRIBUTING to never be detected as a medium, got %+v", found)
	}
}

// TestAutodetectMedium_DiagramAndDoc_BothCandidates proves a .d2
// diagram and a real .md doc both surface as separate candidates when
// both are present -- the "multiple mediums found, pick one" case.
func TestAutodetectMedium_DiagramAndDoc_BothCandidates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "platform.md"), "# Platform\n\nA queue.")
	writeFile(t, filepath.Join(dir, "topology.d2"), "queue: SQS queue")

	found, err := autodetectMedium(dir)
	if err != nil {
		t.Fatalf("autodetectMedium: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 candidates (doc + diagram), got %+v", found)
	}
}

// TestAutodetectMedium_SDKProgram_ContentSniffed proves a .go file is
// only ever detected when it carries the real SDK import (docs/
// cli-output-spec.md §v2's own "extension + intent-marker sniffing"
// rule) -- an arbitrary .go file sitting in the same directory (a
// common case, this being a Go module) never false-positives just
// because of its extension.
func TestAutodetectMedium_SDKProgram_ContentSniffed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "helper.go"), "package main\n\nfunc main() {}\n")

	found, err := autodetectMedium(dir)
	if err != nil {
		t.Fatalf("autodetectMedium: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected an ordinary .go file with no SDK import to never be detected, got %+v", found)
	}

	writeFile(t, filepath.Join(dir, "program.go"), `package main

import (
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

func main() { _ = sdk.Main }
`)
	found, err = autodetectMedium(dir)
	if err != nil {
		t.Fatalf("autodetectMedium: %v", err)
	}
	if len(found) != 1 || found[0].flag != "--from-code" {
		t.Fatalf("expected exactly one --from-code candidate (the real SDK program), got %+v", found)
	}
}

// TestPlanAutodetect_SingleDoc_PlansAutomatically is the end-to-end
// proof: bare `ubx plan` (no --from-doc, no positional argument) with
// exactly one authoring doc in --ledger-dir plans it automatically.
func TestPlanAutodetect_SingleDoc_PlansAutomatically(t *testing.T) {
	fake := &fakeIntentAdapter{draft: `{
		"schema_version": 1,
		"kind": "ubx:intent/v1",
		"stack": "playground",
		"intent": {"summary": "queue via auto-detected ubx plan"},
		"resources": [
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": "{\"name\":\"widget1\"}"}
		],
		"destroys": []
	}`}
	withBuildIntentAdapter(t, fake)

	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)
	writeConfig(t, ledgerDir, `stack = "playground"`)
	writeFile(t, filepath.Join(ledgerDir, "platform.md"), "# Platform\n\nA queue.")

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	out, err := runUbx(t, env, "plan", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("bare ubx plan with one auto-detected doc: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "blast radius: +1 ~0 -0") {
		t.Fatalf("expected the auto-detected doc's own resolved delta, got: %s", out)
	}
	if !strings.Contains(out, "platform.md") {
		t.Fatalf("expected the auto-detected file's own name in the receipt header, got: %s", out)
	}
}

// TestPlanAutodetect_MultipleCandidates_ListsAndAsks proves multiple
// medium candidates are never guessed between -- a teaching error lists
// every candidate with its own correct --from-* flag.
func TestPlanAutodetect_MultipleCandidates_ListsAndAsks(t *testing.T) {
	ledgerDir := t.TempDir()
	writeFile(t, filepath.Join(ledgerDir, "platform.md"), "# Platform\n\nA queue.")
	writeFile(t, filepath.Join(ledgerDir, "topology.d2"), "queue: SQS queue")

	out, err := runUbx(t, nil, "plan", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "multiple mediums found") {
		t.Fatalf("expected a multiple-mediums-found error, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(err.Error(), "--from-doc") || !strings.Contains(err.Error(), "--from-diagram") {
		t.Fatalf("expected both candidates' own correct flags named, got: %v", err)
	}
}

// TestPlanAutodetect_ReadmeOnly_StillRequiresInput proves a README
// alone never auto-plans -- bare `ubx plan` still refuses with the
// ordinary "requires exactly one of" error, exactly as if no files were
// present at all.
func TestPlanAutodetect_ReadmeOnly_StillRequiresInput(t *testing.T) {
	ledgerDir := t.TempDir()
	writeFile(t, filepath.Join(ledgerDir, "README.md"), "# This project\n\nNot an authoring doc.")

	_, err := runUbx(t, nil, "plan", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "requires exactly one of") {
		t.Fatalf("expected the ordinary requires-one-input error, got: %v", err)
	}
}
