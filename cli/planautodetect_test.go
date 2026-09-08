package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAutodetectMedium_ReadmeNeverFalsePositive proves an ordinary
// markdown file sitting in the directory (README-shaped or otherwise)
// is never mistaken for an SDK program -- UBI-224 removed markdown as an
// authoring medium entirely, so autodetectMedium no longer has any .md
// handling at all to false-positive on in the first place.
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
	if len(found) != 1 {
		t.Fatalf("expected exactly one --from-code candidate (the real SDK program), got %+v", found)
	}
}

// autodetectSDKProgram is a single, self-contained TS SDK program (its
// own inline binding, never a second "./bindings.ts" file) that creates
// one fake_widget -- used to prove bare `ubx plan`'s auto-detection
// actually resolves, not just finds, a lone candidate. Self-contained
// so writing it alone into a directory is exactly one autodetectMedium
// candidate.
const autodetectSDKProgram = `import { intent, resource, stack } from "@ubx/sdk";
import type { FieldMap, ResourceBinding } from "@ubx/sdk";

interface FakeWidgetConfig {
  name: string;
  tags?: Record<string, string>;
}
interface FakeWidgetAttrs {
  id: string;
  name: string;
  tags: Record<string, string>;
}
const fields: FieldMap = { name: "name", tags: "tags" };
const FakeWidget: ResourceBinding<FakeWidgetConfig, FakeWidgetAttrs> = {
  wireType: "fake_widget",
  fields,
};

export default stack("playground", () => {
  intent({ summary: "queue via auto-detected ubx plan" });
  resource(FakeWidget, "widget1", { name: "widget1" });
});
`

// TestPlanAutodetect_SingleDoc_PlansAutomatically is the end-to-end
// proof: bare `ubx plan` (no --from-code, no positional argument) with
// exactly one SDK program in --ledger-dir plans it automatically.
func TestPlanAutodetect_SingleDoc_PlansAutomatically(t *testing.T) {
	requireDeno(t)

	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)
	writeConfig(t, ledgerDir, `stack = "playground"`)
	writeFile(t, filepath.Join(ledgerDir, "platform.ts"), autodetectSDKProgram)

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	out, err := runUbx(t, env, "plan", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("bare ubx plan with one auto-detected SDK program: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "blast radius: +1 ~0 -0") {
		t.Fatalf("expected the auto-detected program's own resolved delta, got: %s", out)
	}
	if !strings.Contains(out, "platform.ts") {
		t.Fatalf("expected the auto-detected file's own name in the receipt header, got: %s", out)
	}
}

// TestPlanAutodetect_MultipleCandidates_ListsAndAsks proves multiple
// SDK program candidates are never guessed between -- a teaching error
// lists every candidate with its own correct --from-code invocation.
func TestPlanAutodetect_MultipleCandidates_ListsAndAsks(t *testing.T) {
	ledgerDir := t.TempDir()
	writeFile(t, filepath.Join(ledgerDir, "platform.ts"), autodetectSDKProgram)
	writeFile(t, filepath.Join(ledgerDir, "platform2.ts"), autodetectSDKProgram)

	out, err := runUbx(t, nil, "plan", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "multiple SDK programs found") {
		t.Fatalf("expected a multiple-SDK-programs-found error, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(err.Error(), "platform.ts") || !strings.Contains(err.Error(), "platform2.ts") || !strings.Contains(err.Error(), "ubx plan ") {
		t.Fatalf("expected both candidates' own correct invocation named, got: %v", err)
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
	if !strings.Contains(err.Error(), "no SDK program found here") {
		t.Fatalf("expected the ordinary no-input refusal, got: %v", err)
	}
	// The refusal now names the conventional entry, so a reader learns
	// what to write rather than only that something is missing.
	if !strings.Contains(err.Error(), "stack.ts") {
		t.Fatalf("the refusal does not name the conventional entry file, got: %v", err)
	}
}

// TestPlanAutodetect_ConventionalEntryWinsOverOtherPrograms is the
// conventional-entry rule: bare `ubx plan` in a directory holding more
// than one SDK program picks stack.<ext> rather than refusing.
//
// Deliberately not directory merging the way Terraform concatenates
// every .tf file. These languages already have imports, so a stack
// spanning several files says so in its own language; merging has no
// coherent cross-language meaning; and intent.sources stamps ONE entry
// file's content hash, which is what makes an SDK-authored proposal
// auditable at all.
func TestPlanAutodetect_ConventionalEntryWinsOverOtherPrograms(t *testing.T) {
	requireDeno(t)

	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `stack = "playground"`)
	writeFile(t, filepath.Join(dir, "billing.ts"), autodetectSDKProgram)
	writeFile(t, filepath.Join(dir, "stack.ts"), autodetectSDKProgram)

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	out, err := runUbx(t, env, "plan", "--provider", fakeProviderBinary, "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("bare ubx plan with a conventional entry present: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "stack.ts") {
		t.Fatalf("expected the conventional entry to win, got: %s", out)
	}
	if strings.Contains(out, "billing.ts") {
		t.Errorf("the non-conventional program should not have been planned: %s", out)
	}
}

// Two non-conventional programs is still a genuine ambiguity, refused
// with a teaching error rather than guessed. The error now also names
// the convention, so the reader learns the way out rather than only the
// escape hatch.
func TestPlanAutodetect_NoConventionalEntry_StillRefusesAndTeaches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "billing.ts"), autodetectSDKProgram)
	writeFile(t, filepath.Join(dir, "other.ts"), autodetectSDKProgram)

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	_, err := runUbx(t, env, "plan", "--provider", fakeProviderBinary, "--ledger-dir", dir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "multiple SDK programs found") {
		t.Fatalf("expected the ambiguity refusal, got: %v", err)
	}
	if !strings.Contains(err.Error(), "stack.ts") {
		t.Errorf("the ambiguity error does not name the conventional way out: %v", err)
	}
}

// TestPlanResolve_PositionalSDKProgram_NeedsNoFlag is UBI-224's own
// delayed consequence: --from-code existed to tell an SDK program apart
// from the markdown, diagram and chat mediums, all three of which that
// ticket removed. From then on it distinguished nothing, because every
// input that is not an intent file is an SDK program.
//
// Both commands, deliberately. Leaving one on a flag and the other not
// is worse than either state on its own.
func TestPlanResolve_PositionalSDKProgram_NeedsNoFlag(t *testing.T) {
	requireDeno(t)

	for _, verb := range []string{"plan", "resolve"} {
		t.Run(verb, func(t *testing.T) {
			dir := t.TempDir()
			withConfigSearchDir(t, dir)
			writeConfig(t, dir, `stack = "playground"`)
			entry := filepath.Join(dir, "app.ts")
			writeFile(t, entry, autodetectSDKProgram)

			env := []string{"FAKEPROVIDER_MODE=ok-v6"}
			out, err := runUbx(t, env, verb, entry, "--provider", fakeProviderBinary, "--ledger-dir", dir)
			if err != nil {
				t.Fatalf("ubx %s <program.ts> with no flag: %v\noutput: %s", verb, err, out)
			}
			if !strings.Contains(out, "1 create") {
				t.Fatalf("expected the program to have been evaluated, got: %s", out)
			}
		})
	}
}

// The flag stays as a hidden alias. It is spelled out across the
// tutorials, in `ubx promote`'s own teaching errors, and in this
// command's multiple-candidate hint, so removing it outright would break
// working invocations for no gain.
func TestPlanResolve_FromCodeFlag_StillWorksButIsHidden(t *testing.T) {
	requireDeno(t)

	for _, verb := range []string{"plan", "resolve"} {
		t.Run(verb, func(t *testing.T) {
			dir := t.TempDir()
			withConfigSearchDir(t, dir)
			writeConfig(t, dir, `stack = "playground"`)
			entry := filepath.Join(dir, "app.ts")
			writeFile(t, entry, autodetectSDKProgram)

			env := []string{"FAKEPROVIDER_MODE=ok-v6"}
			out, err := runUbx(t, env, verb, "--from-code", entry, "--provider", fakeProviderBinary, "--ledger-dir", dir)
			if err != nil {
				t.Fatalf("ubx %s --from-code still has to work: %v\noutput: %s", verb, err, out)
			}

			help, err := runUbx(t, nil, verb, "--help")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(help, "from-code") {
				t.Errorf("ubx %s --help still advertises the alias, which teaches a flag nobody needs to type:\n%s", verb, help)
			}
		})
	}
}

// A positional intent file must still be read as an intent file: the
// extension dispatch must not swallow the original argument.
func TestPlan_PositionalIntentFile_StillReadsAsIntent(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `stack = "payments"`)
	intentPath := filepath.Join(dir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "add widget1"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create",
				"config": map[string]interface{}{"name": "widget1"}},
		},
	})

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	out, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx plan <intent.json>: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "1 create") {
		t.Fatalf("expected the intent file to resolve, got: %s", out)
	}
}

// TestPlanAutodetect_ConventionalEntry_CoversTheHCLMedium closes the gap
// left when the conventional entry landed for three media and not the
// fourth.
//
// Two things had to change for stack.ubx.hcl to work, and only one of
// them is obvious. The first is that `ubx plan` refused .ubx.hcl
// outright, so the front door could not plan a whole authoring medium
// that `ubx resolve` had always accepted. The second is quieter: the
// original conventional-name check derived a base with
// TrimSuffix(name, filepath.Ext(name)), which yields "stack.ubx" for a
// double extension, so it would silently have failed to match even once
// detection found the file.
func TestPlanAutodetect_ConventionalEntry_CoversTheHCLMedium(t *testing.T) {
	if !isConventionalEntry("stack.ubx.hcl") {
		t.Error("stack.ubx.hcl is not recognised as a conventional entry")
	}
	for _, name := range []string{"stack.ts", "stack.go", "stack.py"} {
		if !isConventionalEntry(name) {
			t.Errorf("%s is not recognised as a conventional entry", name)
		}
	}
	for _, name := range []string{"stack.ubx", "billing.ubx.hcl", "stack.hcl", "stack.tsx"} {
		if isConventionalEntry(name) {
			t.Errorf("%s should not be a conventional entry", name)
		}
	}
	if !strings.Contains(conventionalEntryNames(), "stack.ubx.hcl") {
		t.Error("the teaching error does not name the HCL medium")
	}
}

// Two conventional entries in different media is refused, never resolved
// by precedence. They are not two spellings of one stack: one evaluates
// code and the other only parses, so any fixed precedence would mean
// adding a file silently changes which one ships.
func TestPlanAutodetect_TwoMedia_RefusesAndSaysWhy(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `stack = "demo"`)
	writeFile(t, filepath.Join(dir, "stack.ts"), autodetectSDKProgram)
	writeFile(t, filepath.Join(dir, "stack.ubx.hcl"), "stack = \"demo\"\n")

	_, err := runUbx(t, nil, "plan", "--ledger-dir", dir)
	requireExitCode(t, err, 2, "")
	msg := err.Error()
	if !strings.Contains(msg, "stack.ts") || !strings.Contains(msg, "stack.ubx.hcl") {
		t.Fatalf("expected both entries named, got: %v", err)
	}
	// The message must not read like the two-programs case, which is a
	// tidying problem. This is a decision about which medium the stack is
	// authored in.
	if !strings.Contains(msg, "authoring media") {
		t.Errorf("expected the error to name the medium conflict, got: %v", err)
	}
	if strings.Contains(msg, "multiple SDK programs found") {
		t.Errorf("expected the medium-conflict error, not the two-programs one, got: %v", err)
	}
}

// HCL reaching the intent-file reader used to report "invalid character
// 's' looking for beginning of value", a JSON parse error about a file
// that was never JSON. It named the wrong problem entirely.
func TestPlan_HCLContentNotNamedUbxHCL_SaysSo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.json")
	writeFile(t, path, "stack = \"demo\"\n\nblueprint \"a\" \"b\" {\n  source = \"/tmp/x\"\n}\n")

	_, err := runUbx(t, nil, "plan", path, "--ledger-dir", dir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "looks like HCL") {
		t.Fatalf("expected the misdirecting JSON parse error to be replaced, got: %v", err)
	}
	if strings.Contains(err.Error(), "invalid character") {
		t.Errorf("still reporting a JSON parse error for an HCL file: %v", err)
	}
}
