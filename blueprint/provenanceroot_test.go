package blueprint

import (
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
