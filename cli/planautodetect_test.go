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
	if !strings.Contains(err.Error(), "platform.ts") || !strings.Contains(err.Error(), "platform2.ts") || !strings.Contains(err.Error(), "ubx plan --from-code") {
		t.Fatalf("expected both candidates' own correct --from-code invocation named, got: %v", err)
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
