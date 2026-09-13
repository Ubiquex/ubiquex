package blueprint

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/goeval"
	"github.com/ubiquex/ubiquex/tseval"
)

// Both discovery paths, Go's and TypeScript's, looked for a blueprint
// root one level ABOVE the imported source. That is the built model's
// shape (root/go/, root/ts/) and only that one. A blueprint written as
// code puts its source at the root itself, so it was never discovered,
// and a code blueprint that pushed its own name got a hard refusal
// rather than silence.

func TestBlueprintRootContaining_BothModels(t *testing.T) {
	parent := t.TempDir()

	// Built model: source one level below the blueprint root.
	built := filepath.Join(parent, "built-bp")
	if err := os.MkdirAll(filepath.Join(built, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(built, UbxfileName), []byte("lang: go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Code model: source at the blueprint root.
	code := filepath.Join(parent, "code-bp")
	if err := os.MkdirAll(code, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(code, SchemaFileName), []byte(`{"schema_version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Neither: an ordinary dependency.
	ordinary := filepath.Join(parent, "some-lib", "pkg")
	if err := os.MkdirAll(ordinary, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		dir  string
		want string
	}{
		{"built blueprint, source one level below", filepath.Join(built, "go"), built},
		{"code blueprint, source at the root", code, code},
	} {
		got, ok := blueprintRootContaining(c.dir)
		if !ok || got != c.want {
			t.Errorf("%s: got (%q, %v), want (%q, true)", c.name, got, ok, c.want)
		}
	}

	if got, ok := blueprintRootContaining(ordinary); ok {
		t.Errorf("an ordinary dependency was claimed as a blueprint: %q", got)
	}
}

// writeGoCodeBlueprintThatPushes writes a Go blueprint under the code
// model whose entrypoint marks its own resources, which is what the
// built model's generated wrapper does for itself.
func writeGoCodeBlueprintThatPushes(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module github.com/ubx-blueprints/" + name + "\n\ngo 1.23\n\n" +
		"require github.com/ubiquex/ubx-sdk-go v0.0.0\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRootForSchemaTests(t) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package widgets

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

type Config struct{ Name string }
type Outputs struct{ WidgetID *sdk.Computed }

func BuildWidget(cfg Config) Outputs {
	sdk.PushBlueprintSource("` + name + `")
	defer sdk.PopBlueprintSource()
	w := sdk.Resource(
		sdk.ResourceBinding{WireType: "fake_widget", Fields: sdk.FieldMap{"Name": {WireName: "name"}}},
		"primary",
		struct{ Name string }{cfg.Name},
	)
	return Outputs{WidgetID: w.Field("id")}
}
`
	if err := os.WriteFile(filepath.Join(dir, "widget.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(context.Background(), dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}
	return dir
}

// requireSandboxForBlueprintTests skips when goeval has no verified
// hermetic mechanism on this platform, matching goeval's own guard.
func requireSandboxForBlueprintTests(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec not found in PATH")
		}
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bubblewrap (bwrap) not found in PATH")
		}
	default:
		t.Skip("no verified hermetic sandbox mechanism on this platform")
	}
}

func TestStampDirectCallProvenance_GoCodeBlueprint_IsDiscovered(t *testing.T) {
	requireSandboxForBlueprintTests(t)

	parent := t.TempDir()
	bp := writeGoCodeBlueprintThatPushes(t, parent, "widget-bp")

	stack := filepath.Join(parent, "stack")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/stack\n\ngo 1.23\n\nrequire (\n" +
		"\tgithub.com/ubiquex/ubx-sdk-go v0.0.0\n" +
		"\tgithub.com/ubx-blueprints/widget-bp v0.0.0\n)\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRootForSchemaTests(t) + "\n" +
		"replace github.com/ubx-blueprints/widget-bp => " + bp + "\n"
	if err := os.WriteFile(filepath.Join(stack, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	main := `package main

import (
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
	widgets "github.com/ubx-blueprints/widget-bp"
)

func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "direct import of a code blueprint"})
		widgets.BuildWidget(widgets.Config{Name: "orders"})
	}))
}
`
	entry := filepath.Join(stack, "main.go")
	if err := os.WriteFile(entry, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	canon, err := goeval.Evaluate(ctx, entry)
	if err != nil {
		t.Fatalf("goeval.Evaluate: %v", err)
	}
	var intent resolver.IntentFile
	if err := json.Unmarshal(canon, &intent); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, canon)
	}

	// Before the fix this refused outright: "no imported Go module ...
	// sits inside a blueprint this can hash".
	if err := StampDirectCallProvenance(ctx, entry, &intent); err != nil {
		t.Fatalf("StampDirectCallProvenance: %v", err)
	}

	assertStampedRef(t, &intent, "widget-bp")
}

func TestStampDirectCallProvenanceTS_CodeBlueprint_IsDiscovered(t *testing.T) {
	requireDeno(t)

	parent := t.TempDir()
	bp := filepath.Join(parent, "widget-bp")
	if err := os.MkdirAll(bp, 0o755); err != nil {
		t.Fatal(err)
	}
	// A code blueprint's entry sits at the blueprint root, so the old
	// "the directory must be named ts" check skipped it immediately.
	src := `import * as sdk from "@ubx/sdk";
import { Computed } from "@ubx/sdk";

export interface Config { name: string }
export interface Outputs { widgetId: Computed }

export function buildWidget(cfg: Config): Outputs {
  sdk.pushBlueprintSource("widget-bp");
  try {
    const w = sdk.resource(
      { wireType: "fake_widget", fields: { name: { wireName: "name" } } },
      "primary",
      { name: cfg.name },
    );
    return { widgetId: (w as unknown as Record<string, Computed>).id };
  } finally {
    sdk.popBlueprintSource();
  }
}
`
	if err := os.WriteFile(filepath.Join(bp, "blueprint.ts"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(context.Background(), bp, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}

	stack := filepath.Join(parent, "stack")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(stack, "main.ts")
	main := `import * as sdk from "@ubx/sdk";
import { buildWidget } from "../widget-bp/blueprint.ts";

export default sdk.stack("payments", () => {
  sdk.intent({ summary: "direct import of a code blueprint" });
  buildWidget({ name: "orders" });
});
`
	if err := os.WriteFile(entry, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	canon, err := tseval.Evaluate(ctx, entry)
	if err != nil {
		t.Fatalf("tseval.Evaluate: %v", err)
	}
	var intent resolver.IntentFile
	if err := json.Unmarshal(canon, &intent); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, canon)
	}

	if err := StampDirectCallProvenanceTS(ctx, entry, &intent); err != nil {
		t.Fatalf("StampDirectCallProvenanceTS: %v", err)
	}

	assertStampedRef(t, &intent, "widget-bp")
}

// assertStampedRef checks that every blueprint source on every resource
// was completed from a bare name into a real "<name>:sha256:..." ref.
func assertStampedRef(t *testing.T, intent *resolver.IntentFile, name string) {
	t.Helper()
	seen := 0
	for _, r := range intent.Resources {
		for _, s := range r.Sources {
			if s.Kind != "blueprint" {
				continue
			}
			seen++
			if !strings.HasPrefix(s.Ref, name+":sha256:") {
				t.Errorf("%s.%s source ref = %q, want %s:sha256:...", r.Type, r.Name, s.Ref, name)
			}
		}
	}
	if seen == 0 {
		t.Fatalf("no resource carries a blueprint source at all: %+v", intent.Resources)
	}
}

// ---------------------------------------------------------------------
// Call-site attribution (UBI-266)
// ---------------------------------------------------------------------

// writeGoCodeBlueprint writes a blueprint under the code model exactly
// as the as-code tutorial shows one: a plain exported function, with NO
// PushBlueprintSource anywhere. Before call-site attribution every
// resource it produced reached the ledger with no source at all.
func writeGoCodeBlueprint(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module github.com/ubx-blueprints/" + name + "\n\ngo 1.23\n\n" +
		"require github.com/ubiquex/ubx-sdk-go v0.0.0\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRootForSchemaTests(t) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package widgets

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

type Config struct{ Name string }
type Outputs struct{ WidgetID *sdk.Computed }

func BuildWidget(cfg Config) Outputs {
	w := sdk.Resource(
		sdk.ResourceBinding{WireType: "fake_widget", Fields: sdk.FieldMap{"Name": {WireName: "name"}}},
		"primary",
		struct{ Name string }{cfg.Name},
	)
	return Outputs{WidgetID: w.Field("id")}
}
`
	if err := os.WriteFile(filepath.Join(dir, "widget.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(context.Background(), dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}
	return dir
}

func writeGoStackImporting(t *testing.T, parent, bpDir string) string {
	t.Helper()
	stack := filepath.Join(parent, "stack")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/stack\n\ngo 1.23\n\nrequire (\n" +
		"\tgithub.com/ubiquex/ubx-sdk-go v0.0.0\n" +
		"\tgithub.com/ubx-blueprints/" + filepath.Base(bpDir) + " v0.0.0\n)\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRootForSchemaTests(t) + "\n" +
		"replace github.com/ubx-blueprints/" + filepath.Base(bpDir) + " => " + bpDir + "\n"
	if err := os.WriteFile(filepath.Join(stack, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	main := `package main

import (
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
	widgets "github.com/ubx-blueprints/` + filepath.Base(bpDir) + `"
)

func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "a stack calling a code blueprint"})
		widgets.BuildWidget(widgets.Config{Name: "orders"})
		// A resource the stack itself creates, which must stay
		// unattributed: attribution is per call site, not per program.
		sdk.Resource(
			sdk.ResourceBinding{WireType: "fake_widget", Fields: sdk.FieldMap{"Name": {WireName: "name"}}},
			"own",
			struct{ Name string }{"written-here"},
		)
	}))
}
`
	entry := filepath.Join(stack, "main.go")
	if err := os.WriteFile(entry, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestEvaluateGoWithBlueprints_AttributesByCallSite(t *testing.T) {
	requireSandboxForBlueprintTests(t)

	parent := t.TempDir()
	bp := writeGoCodeBlueprint(t, parent, "widget-bp")
	entry := writeGoStackImporting(t, parent, bp)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	canon, refs, err := EvaluateGoWithBlueprints(ctx, entry)
	if err != nil {
		t.Fatalf("EvaluateGoWithBlueprints: %v", err)
	}
	var intent resolver.IntentFile
	if err := json.Unmarshal(canon, &intent); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, canon)
	}
	if err := StampDirectCallProvenancePy(&intent, refs); err != nil {
		t.Fatalf("complete refs: %v", err)
	}

	byName := map[string]resolver.ResourceIntent{}
	for _, r := range intent.Resources {
		byName[r.Name] = r
	}

	// The blueprint's own resource is attributed, with a real hash,
	// though nothing in the blueprint said so.
	primary, ok := byName["primary"]
	if !ok {
		t.Fatalf("no resource named primary: %+v", intent.Resources)
	}
	if len(primary.Sources) != 1 || primary.Sources[0].Kind != "blueprint" {
		t.Fatalf("primary has no blueprint source: %+v", primary.Sources)
	}
	if !strings.HasPrefix(primary.Sources[0].Ref, "widget-bp:sha256:") {
		t.Errorf("primary ref = %q, want widget-bp:sha256:...", primary.Sources[0].Ref)
	}

	// The stack's own resource is not, which is the half a program-wide
	// flag would get wrong.
	own, ok := byName["own"]
	if !ok {
		t.Fatalf("no resource named own: %+v", intent.Resources)
	}
	if len(own.Sources) != 0 {
		t.Errorf("a resource the stack wrote itself was attributed to a blueprint: %+v", own.Sources)
	}
}

// An ordinary stack importing no blueprint gets no manifest, so nothing
// about it changes. The guard against a mechanism that only ever adds
// provenance starting to add it where there is none.
func TestEvaluateGoWithBlueprints_OrdinaryStackIsUnchanged(t *testing.T) {
	requireSandboxForBlueprintTests(t)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	canon, refs, err := EvaluateGoWithBlueprints(ctx, "../goeval/testdata/happy/main.go")
	if err != nil {
		t.Fatalf("EvaluateGoWithBlueprints: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("refs = %v, want none for a stack importing no blueprint", refs)
	}
	if strings.Contains(string(canon), `"blueprint"`) {
		t.Errorf("an ordinary stack gained a blueprint source:\n%s", canon)
	}
}

func TestEvaluateTSWithBlueprints_AttributesByCallSite(t *testing.T) {
	requireDeno(t)

	parent := t.TempDir()
	bp := filepath.Join(parent, "widget-bp")
	if err := os.MkdirAll(bp, 0o755); err != nil {
		t.Fatal(err)
	}
	// No pushBlueprintSource anywhere: the as-code model's own shape.
	src := `import * as sdk from "@ubx/sdk";
import { Computed } from "@ubx/sdk";

export interface Config { name: string }
export interface Outputs { widgetId: Computed }

export function buildWidget(cfg: Config): Outputs {
  const w = sdk.resource(
    { wireType: "fake_widget", fields: { name: { wireName: "name" } } },
    "primary",
    { name: cfg.name },
  );
  return { widgetId: (w as unknown as Record<string, Computed>).id };
}
`
	if err := os.WriteFile(filepath.Join(bp, "blueprint.ts"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(context.Background(), bp, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}

	stack := filepath.Join(parent, "stack")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(stack, "main.ts")
	main := `import * as sdk from "@ubx/sdk";
import { buildWidget } from "../widget-bp/blueprint.ts";

export default sdk.stack("payments", () => {
  sdk.intent({ summary: "a stack calling a code blueprint" });
  buildWidget({ name: "orders" });
  sdk.resource(
    { wireType: "fake_widget", fields: { name: { wireName: "name" } } },
    "own",
    { name: "written-here" },
  );
});
`
	if err := os.WriteFile(entry, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	canon, refs, err := EvaluateTSWithBlueprints(ctx, entry)
	if err != nil {
		t.Fatalf("EvaluateTSWithBlueprints: %v", err)
	}
	var intent resolver.IntentFile
	if err := json.Unmarshal(canon, &intent); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, canon)
	}
	if err := StampDirectCallProvenancePy(&intent, refs); err != nil {
		t.Fatalf("complete refs: %v", err)
	}
	assertAttribution(t, &intent, "widget-bp")
}

// assertAttribution is the shared check for every language's own
// call-site test: the blueprint's resource carries a real hashed ref,
// and the stack's own resource carries nothing.
func assertAttribution(t *testing.T, intent *resolver.IntentFile, name string) {
	t.Helper()
	byName := map[string]resolver.ResourceIntent{}
	for _, r := range intent.Resources {
		byName[r.Name] = r
	}

	primary, ok := byName["primary"]
	if !ok {
		t.Fatalf("no resource named primary: %+v", intent.Resources)
	}
	if len(primary.Sources) != 1 || primary.Sources[0].Kind != "blueprint" {
		t.Fatalf("primary has no blueprint source: %+v", primary.Sources)
	}
	if !strings.HasPrefix(primary.Sources[0].Ref, name+":sha256:") {
		t.Errorf("primary ref = %q, want %s:sha256:...", primary.Sources[0].Ref, name)
	}

	own, ok := byName["own"]
	if !ok {
		t.Fatalf("no resource named own: %+v", intent.Resources)
	}
	if len(own.Sources) != 0 {
		t.Errorf("a resource the stack wrote itself was attributed to a blueprint: %+v", own.Sources)
	}
}

// pyCodeBlueprintSource is a blueprint under the code model with NO
// push_blueprint_source anywhere, which is what the as-code model
// actually produces.
const pyCodeBlueprintSource = `from dataclasses import dataclass

import ubx_sdk as ubx
from ubx_sdk import Computed, FieldSpec, ResourceBinding


@dataclass
class Config:
    name: str


@dataclass
class Outputs:
    widget_id: Computed


WIDGET = ResourceBinding(
    wire_type="fake_widget",
    fields={"name": FieldSpec(wire_name="name")},
)


@dataclass
class WidgetConfig:
    name: str


def build_widget(cfg: Config) -> Outputs:
    w = ubx.resource(WIDGET, "primary", WidgetConfig(name=cfg.name))
    return Outputs(w.id)
`

const pyStackSource = `import sys, os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "widget-bp"))

import ubx_sdk as ubx
from ubx_sdk import FieldSpec, ResourceBinding
from dataclasses import dataclass
from blueprint import Config, build_widget

OWN = ResourceBinding(wire_type="fake_widget", fields={"name": FieldSpec(wire_name="name")})


@dataclass
class OwnConfig:
    name: str


def main():
    ubx.intent("a stack calling a colocated code blueprint")
    build_widget(Config(name="orders"))
    ubx.resource(OWN, "own", OwnConfig(name="written-here"))


if __name__ == "__main__":
    ubx.run("payments", main)
`

// The colocated shape: a blueprint copied into the stack's own
// directory and reached with sys.path.insert. It has no declaration
// anywhere, which is why it recorded nothing at all before this, and
// why Python needs a walk where Go and TypeScript have a module graph.
func TestEvaluatePythonWithDeps_ColocatedBlueprint_AttributesByCallSite(t *testing.T) {
	requireWasmtimeForBlueprintTests(t)

	stack := t.TempDir()
	bp := filepath.Join(stack, "widget-bp")
	if err := os.MkdirAll(bp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bp, "blueprint.py"), []byte(pyCodeBlueprintSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(context.Background(), bp, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}

	entry := filepath.Join(stack, "main.py")
	if err := os.WriteFile(entry, []byte(pyStackSource), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	canon, _, refs, err := EvaluatePythonWithDeps(ctx, entry)
	if err != nil {
		t.Fatalf("EvaluatePythonWithDeps: %v", err)
	}
	var intent resolver.IntentFile
	if err := json.Unmarshal(canon, &intent); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, canon)
	}
	if err := StampDirectCallProvenancePy(&intent, refs); err != nil {
		t.Fatalf("complete refs: %v", err)
	}
	assertAttribution(t, &intent, "widget-bp")
}

// The declared shape, for the same blueprint: pulled and mounted at its
// own guest path rather than arriving under the program's directory.
// Both have to attribute, and the guest paths involved are different,
// which is the part only pyeval can know.
func TestEvaluatePythonWithDeps_DeclaredBlueprint_AttributesByCallSite(t *testing.T) {
	requireWasmtimeForBlueprintTests(t)

	parent := t.TempDir()
	bp := filepath.Join(parent, "widget-bp")
	if err := os.MkdirAll(bp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bp, "blueprint.py"), []byte(pyCodeBlueprintSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(context.Background(), bp, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}

	stack := filepath.Join(parent, "stack")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRequirementsTxt(t, stack, "widget-bp @ "+bp+"\n")
	entry := filepath.Join(stack, "main.py")
	declared := strings.Replace(pyStackSource,
		"import sys, os\nsys.path.insert(0, os.path.join(os.path.dirname(__file__), \"widget-bp\"))\n\n", "", 1)
	if err := os.WriteFile(entry, []byte(declared), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	canon, receipts, refs, err := EvaluatePythonWithDeps(ctx, entry)
	if err != nil {
		t.Fatalf("EvaluatePythonWithDeps: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("receipts = %v, want one for the declared dependency", receipts)
	}
	var intent resolver.IntentFile
	if err := json.Unmarshal(canon, &intent); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, canon)
	}
	if err := StampDirectCallProvenancePy(&intent, refs); err != nil {
		t.Fatalf("complete refs: %v", err)
	}
	assertAttribution(t, &intent, "widget-bp")
}

// Discovery runs `go list` with -mod=mod, which reconciles a
// toolchain-version mismatch by WRITING BACK to go.mod. Reading a
// program must never modify it, the same rule goeval's own build
// satisfies by building from a copy.
//
// This was already true before call-site attribution and rarely
// visible, since discovery only ran for a program that had produced a
// bare blueprint name. Running it for every Go program turned it into a
// mutation on every `ubx resolve --from-code`.
func TestDiscoverGoBlueprintRoots_DoesNotModifyTheProgram(t *testing.T) {
	parent := t.TempDir()
	bp := writeGoCodeBlueprint(t, parent, "widget-bp")
	entry := writeGoStackImporting(t, parent, bp)

	goModPath := filepath.Join(filepath.Dir(entry), "go.mod")
	before, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	// A go.sum `go list` might create, which would be the same unwanted
	// mutation as a changed go.mod.
	goSumPath := filepath.Join(filepath.Dir(entry), "go.sum")
	if _, err := os.Stat(goSumPath); !os.IsNotExist(err) {
		t.Fatalf("fixture already has a go.sum, which this test needs absent: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := DiscoverGoBlueprintRoots(ctx, entry); err != nil {
		t.Fatalf("DiscoverGoBlueprintRoots: %v", err)
	}

	after, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("go.mod was rewritten by reading the program:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := os.Stat(goSumPath); !os.IsNotExist(err) {
		t.Errorf("a go.sum was left behind by reading the program")
	}
}

// The entry file's own directory is not always the module root: a
// program in a subpackage has its go.mod above it. Snapshotting the
// entry directory alone left this repository's own
// goeval/testdata/go.mod modified in git status after running these
// tests, which is how the gap surfaced.
func TestDiscoverGoBlueprintRoots_DoesNotModifyAModuleRootAbove(t *testing.T) {
	parent := t.TempDir()
	bp := writeGoCodeBlueprint(t, parent, "widget-bp")
	entry := writeGoStackImporting(t, parent, bp)

	// Move the entry into a subpackage, leaving go.mod where it is.
	stackDir := filepath.Dir(entry)
	sub := filepath.Join(stackDir, "cmd", "stack")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	main, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(sub, "main.go")
	if err := os.WriteFile(nested, main, 0o644); err != nil {
		t.Fatal(err)
	}

	goModPath := filepath.Join(stackDir, "go.mod")
	before, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := DiscoverGoBlueprintRoots(ctx, nested); err != nil {
		t.Fatalf("DiscoverGoBlueprintRoots: %v", err)
	}

	after, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a go.mod ABOVE the entry file was rewritten:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
