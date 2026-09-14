package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
)

// TestTranslator_GoBareImport is UBI-272's Go half.
//
// A stack declares a blueprint in .ubx/config and imports it by its
// module path. No `ubx blueprint pull` by hand, no relative import into
// a pulled directory, and critically no require or replace written into
// the author's own go.mod: the blueprint reaches the build as a `use`
// entry in the workspace goeval already synthesizes.
//
// The test asserts the go.mod is byte-identical afterwards, because
// "ubx did not write into a file the toolchain owns" is the property
// this design was chosen for and it would be invisible otherwise.
func TestTranslator_GoBareImport(t *testing.T) {
	dir := t.TempDir()
	ledgerDir := t.TempDir()

	bpGoDir := writeBlueprintPackage(t, dir, "widgetplatform")
	bpRoot := filepath.Dir(bpGoDir)
	if _, err := blueprint.Package(context.Background(), bpRoot, filepath.Join(t.TempDir(), "bp.tar.gz")); err != nil {
		t.Fatalf("package the blueprint: %v", err)
	}

	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The stack's own go.mod does NOT mention the blueprint.
	goMod := "module example.com/stack\n\ngo 1.23\n\nrequire github.com/ubiquex/ubx-sdk-go v0.0.0\n\nreplace github.com/ubiquex/ubx-sdk-go => " + blueprintCallSdkGoRoot(t) + "\n"
	if err := os.WriteFile(filepath.Join(stackDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package main

import (
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
	bp "widgetplatform"
)

func main() {
	sdk.Main(sdk.Stack("platform", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "declared blueprint"})
		bp.Widgetplatform("primary")
	}))
}
`
	entry := filepath.Join(stackDir, "main.go")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	writeStackConfigWithBlueprints(t, ledgerDir, "platform", map[string]string{"widgetplatform": bpRoot})

	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "plan", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--timeout", "180s",
	)
	if err != nil {
		t.Fatalf("plan with a declared Go blueprint: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pulled widgetplatform @") {
		t.Fatalf("expected a pull+verify receipt for the declared blueprint:\n%s", out)
	}

	// The author's go.mod is untouched. This is the property the design
	// exists for.
	after, err := os.ReadFile(filepath.Join(stackDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != goMod {
		t.Fatalf("ubx wrote into the author's go.mod:\n--- before ---\n%s\n--- after ---\n%s", goMod, after)
	}
	if _, err := os.Stat(filepath.Join(stackDir, "go.work")); !os.IsNotExist(err) {
		t.Fatal("ubx left a go.work in the author's tree")
	}

	// And the resource carries the blueprint's provenance, completed
	// from the declaration rather than from a discovered directory.
	assertBlueprintProvenance(t, ledgerDir, "widgetplatform")
}

// assertBlueprintProvenance reads the plan ubx just wrote and checks a
// create carries a complete "<name>:sha256:..." blueprint source.
func assertBlueprintProvenance(t *testing.T, ledgerDir, name string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(ledgerDir, ".ubx", "plans"))
	if err != nil {
		t.Fatalf("read plan store: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no plan was saved")
	}
	data, err := os.ReadFile(filepath.Join(ledgerDir, ".ubx", "plans", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Delta struct {
			Creates []struct {
				Sources []struct {
					Kind string `json:"kind"`
					Ref  string `json:"ref"`
				} `json:"sources"`
			} `json:"creates"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	want := name + ":sha256:"
	for _, c := range doc.Delta.Creates {
		for _, s := range c.Sources {
			if s.Kind == "blueprint" && strings.HasPrefix(s.Ref, want) {
				return
			}
		}
	}
	t.Fatalf("no create carries a complete blueprint ref starting %q: %s", want, data)
}

// TestTranslator_TSBareImport is UBI-272's TypeScript half.
//
// A stack declares a blueprint in .ubx/config and imports it by a bare
// specifier. No pull by hand, no relative path into a pulled directory,
// and nothing written into node_modules or the project's deno.json: the
// blueprint reaches Deno as an entry in the import map tseval already
// generates, merges and cleans up.
func TestTranslator_TSBareImport(t *testing.T) {
	requireDeno(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()

	tsDir := writeBlueprintPackageTS(t, dir, "widgetplatform")
	bpRoot := filepath.Dir(tsDir)
	if _, err := blueprint.Package(context.Background(), bpRoot, filepath.Join(t.TempDir(), "bp.tar.gz")); err != nil {
		t.Fatalf("package the blueprint: %v", err)
	}

	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A bare specifier, resolved by the import map rather than a path.
	src := `import { intent, stack } from "@ubx/sdk";
import { widgetplatform } from "widgetplatform";

export default stack("platform", () => {
  intent({ summary: "declared blueprint" });
  widgetplatform("primary");
});
`
	entry := filepath.Join(stackDir, "stack.ts")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	writeStackConfigWithBlueprints(t, ledgerDir, "platform", map[string]string{"widgetplatform": bpRoot})

	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "plan", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--timeout", "180s",
	)
	if err != nil {
		t.Fatalf("plan with a declared TS blueprint: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pulled widgetplatform @") {
		t.Fatalf("expected a pull+verify receipt for the declared blueprint:\n%s", out)
	}

	// Nothing was written into anything the project or npm owns.
	for _, unwanted := range []string{"node_modules", "deno.json", "deno.jsonc", "package.json"} {
		if _, err := os.Stat(filepath.Join(stackDir, unwanted)); !os.IsNotExist(err) {
			t.Fatalf("ubx created %s in the author's tree", unwanted)
		}
	}

	assertBlueprintProvenance(t, ledgerDir, "widgetplatform")
}
