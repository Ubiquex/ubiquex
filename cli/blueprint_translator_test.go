package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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

// writeTSCodeBlueprintWithOwnDep writes a TypeScript code blueprint that
// imports something by a bare specifier its OWN deno.json declares.
func writeTSCodeBlueprintWithOwnDep(t *testing.T, parent, name, label string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(f, c string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("deno.json", `{"imports":{"helper":"./helper.ts"}}`)
	write("helper.ts", "export const label = () => "+strconv.Quote(label)+";\n")
	write("blueprint.ts", `import { resource } from "@ubx/sdk";
import { label } from "helper";

export interface Config { name: string }

export function `+name+`(c: Config) {
  resource(
    { wireType: "fake_widget", fields: { name: "name" } },
    c.name,
    { name: label() },
  );
}
`)
	if _, err := blueprint.Package(context.Background(), dir, filepath.Join(t.TempDir(), "bp.tar.gz")); err != nil {
		t.Fatalf("package %s: %v", name, err)
	}
	return dir
}

// TestTranslator_TSBlueprintOwnDependency is UBI-274's TypeScript fix.
//
// A blueprint's own deno.json travels in the content store, verified and
// hashed, and was ignored, so a blueprint importing anything by a bare
// specifier could not run. That covers effectively every real blueprint,
// since generated bindings are how you author one.
//
// The test also pins the property scopes buy, which is the part worth
// protecting: consumer and blueprint declare the SAME specifier pointing
// at different files, and each resolves its own. Python cannot do this,
// because PYTHONPATH is one flat search order and one side loses.
func TestTranslator_TSBlueprintOwnDependency(t *testing.T) {
	requireDeno(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()

	bpDir := writeTSCodeBlueprintWithOwnDep(t, dir, "tsbp", "the BLUEPRINT's helper")

	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(f, c string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(stackDir, f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The consumer declares the same specifier, pointing somewhere else.
	write("deno.json", `{"imports":{"helper":"./dep.ts"}}`)
	write("dep.ts", `export const label = () => "the CONSUMER's helper";`+"\n")
	write("stack.ts", `import { intent, stack, resource } from "@ubx/sdk";
import { tsbp } from "tsbp";
import { label } from "helper";

export default stack("demo", () => {
  intent({ summary: "consumer" });
  resource({ wireType: "fake_widget", fields: { name: "name" } }, "consumer-side", { name: label() });
  tsbp({ name: "blueprint-side" });
});
`)

	writeStackConfigWithBlueprints(t, ledgerDir, "demo", map[string]string{"tsbp": bpDir})

	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "plan", filepath.Join(stackDir, "stack.ts"),
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--timeout", "180s",
	)
	if err != nil {
		t.Fatalf("a blueprint importing its own declared dependency must evaluate: %v\n%s", err, out)
	}

	// Each side resolved ITS OWN "helper". One flat namespace would have
	// given both the same answer.
	if !strings.Contains(out, "the BLUEPRINT's helper") {
		t.Fatalf("the blueprint's own dependency did not resolve:\n%s", out)
	}
	if !strings.Contains(out, "the CONSUMER's helper") {
		t.Fatalf("the consumer's own dependency was displaced by the blueprint's:\n%s", out)
	}
}

// TestTranslator_TSRegistrySpecifierIsRefused pins the JSR limit end to
// end, through a real `ubx plan`.
//
// npm dependencies are fetched now, from a blueprint that pins them. JSR
// is not, and cannot be without narrowing --no-remote: resolving a JSR
// package fetches https://jsr.io/<pkg>/meta.json, which that flag blocks
// even against a warm cache. This is the case most likely to be hit,
// because the published Ubiquex TypeScript SDK lives on JSR, so it is
// worth a refusal that names the real cause rather than failing later
// inside Deno with a message about node_modules that names neither the
// blueprint nor the reason (UBI-274).
func TestTranslator_TSRegistrySpecifierIsRefused(t *testing.T) {
	requireDeno(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()

	bpDir := writeTSCodeBlueprintWithOwnDep(t, dir, "tsbp", "unused")
	if err := os.WriteFile(filepath.Join(bpDir, "deno.json"),
		[]byte(`{"imports":{"helper":"./helper.ts","sdkaws":"jsr:@ubx/sdk-aws@1.2.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := blueprint.Package(context.Background(), bpDir, filepath.Join(t.TempDir(), "bp.tar.gz")); err != nil {
		t.Fatal(err)
	}

	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stackDir, "stack.ts"), []byte(`import { intent, stack } from "@ubx/sdk";
import { tsbp } from "tsbp";

export default stack("demo", () => {
  intent({ summary: "consumer" });
  tsbp({ name: "w1" });
});
`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeStackConfigWithBlueprints(t, ledgerDir, "demo", map[string]string{"tsbp": bpDir})

	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "plan", filepath.Join(stackDir, "stack.ts"),
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--timeout", "180s",
	)
	if err == nil {
		t.Fatalf("a registry specifier must be refused, not fetched:\n%s", out)
	}
	msg := out + err.Error()
	// No ticket id: a Linear identifier means nothing outside this org and
	// does not belong in CLI output.
	if strings.Contains(msg, "UBI-") {
		t.Fatalf("CLI output must not cite a ticket id:\n%s", msg)
	}
	for _, want := range []string{"tsbp", "jsr:@ubx/sdk-aws@1.2.0", "--no-remote"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal does not name %q:\n%s", want, msg)
		}
	}
}

// requireNPMLive gates the one test that genuinely fetches from the npm
// registry.
//
// `go test ./...` stays hermetic, which is why this is gated rather than
// mocked: the thing under test is that a REAL deno subprocess fetches a
// REAL package and verifies it against a REAL lock. A fake registry
// would assert that ubx builds the command it builds, which is the class
// of test this project has already been burned by (UBI-252).
func requireNPMLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_NPM_LIVE") != "1" {
		t.Skip("skipping: set UBX_NPM_LIVE=1 to fetch a real npm package from the real registry")
	}
	requireDeno(t)
}

// TestTranslator_TSNPMDependencyPinnedAndFetched is the whole npm path,
// end to end, through real subprocesses: package generates the lock,
// declaration resolves the blueprint, the fetch pass downloads its
// dependency with integrity enforced by that lock, and the blueprint
// runs.
//
// The blueprint is authored the way an npm user would author one, with a
// package.json and no deno.json and no lock of its own, because that is
// the case the design turns on. The author never runs a Deno command;
// `ubx blueprint package` produces the lock, and it then travels under
// the content hash like every other file.
func TestTranslator_TSNPMDependencyPinnedAndFetched(t *testing.T) {
	requireNPMLive(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()

	bpDir := filepath.Join(dir, "npmbp")
	if err := os.MkdirAll(bpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(f, c string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bpDir, f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// An npm author's own declaration: bare semver ranges, no deno.json,
	// no deno.lock.
	//
	// TWO dependencies, imported two different ways, deliberately.
	// left-pad is imported bare and has no dependencies of its own.
	// date-fns is imported by a SUBPATH, which is how a generated SDK is
	// normally used and is the shape a bare-specifier-only import map
	// silently fails to cover: the first version of this test used
	// left-pad alone and passed while `@ubx/sdk-aws/aws/sqs/queue` failed
	// in the field with the very error this change set exists to remove.
	write("package.json", `{"name":"npmbp","version":"1.0.0","dependencies":{"left-pad":"1.3.0","date-fns":"3.6.0"}}`)
	write("blueprint.ts", `import { resource } from "@ubx/sdk";
import leftPad from "left-pad";
import { addDays } from "date-fns/addDays";

export interface Config { name: string }

export function npmbp(c: Config) {
  resource(
    { wireType: "fake_widget", fields: { name: "name" } },
    c.name,
    { name: leftPad("w", 3, "-") + "/" + addDays(new Date(Date.UTC(2020, 0, 1)), 3).toISOString().slice(0, 10) },
  );
}
`)

	if _, err := blueprint.Package(context.Background(), bpDir, filepath.Join(t.TempDir(), "bp.tar.gz")); err != nil {
		t.Fatalf("package: %v", err)
	}
	// The lock is generated by packaging, not by the author. This is the
	// claim the whole design rests on, so it is asserted directly rather
	// than inferred from the evaluation succeeding.
	lockPath := filepath.Join(bpDir, "deno.lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("ubx blueprint package must generate the blueprint's deno.lock: %v", err)
	}
	// And it must be covered by the content hash, or it pins nothing that
	// survives being published.
	manifest, err := blueprint.Verify(bpDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Files["deno.lock"]; !ok {
		t.Fatalf("the generated lock must be in the manifest, got files: %v", manifest.Files)
	}

	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stackDir, "stack.ts"), []byte(`import { intent, stack } from "@ubx/sdk";
import { npmbp } from "npmbp";

export default stack("demo", () => {
  intent({ summary: "consumer" });
  npmbp({ name: "w1" });
});
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The consumer has a deno.json AND a lock of its own, which is what a
	// stack following docs.ubiquex.io/tutorial/sdk/install has. Both are
	// needed to reproduce: deno adopts a deno.lock only when a deno.json
	// or package.json anchors the workspace, so a stack with a bare lock
	// and no config never had its lock touched in the first place. Found
	// by reverting the fix against a fixture that lacked the config and
	// watching the test pass anyway.
	if err := os.WriteFile(filepath.Join(stackDir, "deno.json"), []byte(`{"imports":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	consumerLock := filepath.Join(stackDir, "deno.lock")
	consumerLockBefore := `{"version":"5"}` + "\n"
	if err := os.WriteFile(consumerLock, []byte(consumerLockBefore), 0o644); err != nil {
		t.Fatal(err)
	}

	writeStackConfigWithBlueprints(t, ledgerDir, "demo", map[string]string{"npmbp": bpDir})

	// A COLD deno cache for the evaluation, which is what makes this test
	// test anything.
	//
	// Packaging ran `deno install` moments ago and warmed the shared cache,
	// so on one machine the fetch pass is a no-op and the test passes with
	// it removed entirely. Verified by removing it. A consumer is a
	// different machine with nothing cached, and a fresh DENO_DIR is that
	// machine.
	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6", "DENO_DIR=" + filepath.Join(t.TempDir(), "cold")}, "plan", filepath.Join(stackDir, "stack.ts"),
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--timeout", "180s",
	)
	if err != nil {
		t.Fatalf("a pinned npm dependency must resolve and run: %v\n%s", err, out)
	}
	// Asserting the VALUES proves both real packages ran, where asserting
	// a successful exit would not. left-pad("w", 3, "-") is "--w", and
	// addDays(2020-01-01, 3) is 2020-01-04. The input is built with
	// Date.UTC rather than a local-time constructor, or the assertion
	// would pass or fail by the machine's timezone.
	if !strings.Contains(out, "--w/2020-01-04") {
		t.Fatalf("the npm dependencies did not both execute (bare and subpath):\n%s", out)
	}

	// And evaluation left the author's own deno.lock alone.
	//
	// This assertion lives HERE, in the one test that resolves a registry
	// dependency, because that is the only situation where deno has
	// anything to write. A hermetic version of it was written first and
	// passed with the fix reverted: the stack imported only "@ubx/sdk",
	// which is a file: path, so no lock entry was ever produced and the
	// test asserted nothing. Verified by reverting.
	after, err := os.ReadFile(consumerLock)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != consumerLockBefore {
		t.Fatalf("evaluation rewrote the author's deno.lock:\n before: %q\n after:  %q", consumerLockBefore, string(after))
	}
}
