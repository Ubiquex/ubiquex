package ts

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// TestCheckNoDuplicateDeclarations_DetectsRealCollision proves the checker
// catches two `export interface` declarations sharing one name -- see
// this package's own resourceRenderer doc comment for why that's a real,
// live-verified failure mode (UBI-96) WITHIN one file, not a
// hypothetical.
func TestCheckNoDuplicateDeclarations_DetectsRealCollision(t *testing.T) {
	src := `export interface Foo {
  bar: string;
}

export interface Foo {
  baz: number;
}
`
	err := CheckNoDuplicateDeclarations(src)
	if err == nil {
		t.Fatal("expected an error for two `export interface Foo` declarations, got nil")
	}
	mustContain(t, err.Error(), "Foo")
}

func TestCheckNoDuplicateDeclarations_CleanSource_NoError(t *testing.T) {
	src := `export interface Foo {
  bar: string;
}

export const Foo: Foo = { bar: "x" };
`
	// A const and an interface sharing a name is FINE in TS (separate
	// type/value namespaces) -- must not be flagged.
	if err := CheckNoDuplicateDeclarations(src); err != nil {
		t.Fatalf("expected no error (interface/const namespaces are separate in TS), got: %v", err)
	}
}

// TestCheckRepoNoDuplicateDeclarations_SameNameDifferentFiles_NeverCollides
// is this restructure's own central TS-specific proof, confirmed not
// assumed: the SAME identifier name in TWO DIFFERENT resource types' own
// files is never a collision -- ES modules give every file its own
// independent scope (unlike Go, where the identical shape would need
// its own package boundary to be safe).
func TestCheckRepoNoDuplicateDeclarations_SameNameDifferentFiles_NeverCollides(t *testing.T) {
	repo := map[string]string{
		"package.json":      `{}`,
		"ecr/repository.ts": "export interface Thing {\n  bar: string;\n}\n",
		"iam/role.ts":       "export interface Thing {\n  bar: string;\n}\n",
	}
	if err := CheckRepoNoDuplicateDeclarations(repo); err != nil {
		t.Fatalf("identically-named interfaces in different files should never collide: %v", err)
	}
}

func TestCheckRepoNoDuplicateDeclarations_WithinOneFile_StillCollides(t *testing.T) {
	repo := map[string]string{
		"package.json": `{}`,
		"iam/role.ts":  "export interface Foo {\n  bar: string;\n}\n\nexport interface Foo {\n  baz: number;\n}\n",
	}
	err := CheckRepoNoDuplicateDeclarations(repo)
	if err == nil {
		t.Fatal("expected an error for a real within-file collision, got nil")
	}
	mustContain(t, err.Error(), "iam/role.ts")
}

// TestGeneratedRepo_CrossResourceNestedBlockVsSiblingResource_NoCollision
// reproduces UBI-96's own real, live-verified shape directly, now scoped
// to ONE FILE (UBI-98's own restructure): AWS's own convention of
// splitting a legacy inline nested block out into its own standalone
// resource. aws_thing's own "logging" nested block and the separate
// aws_thing_logging resource are the exact same shape as the real
// aws_s3_bucket / aws_s3_bucket_logging collision found live against
// hashicorp/aws@6.54.0 (STATE.md) -- now, under the per-type-file
// restructure, these two types don't even SHARE a file at all (a
// stronger guarantee than the "_" join alone), but the join is still
// exercised and checked here for the WITHIN-one-file case the join
// exists for (a resource's own nested tree vs. its own bare name).
func TestGeneratedRepo_CrossResourceNestedBlockVsSiblingResource_NoCollision(t *testing.T) {
	loggingBlock := ir.Field{
		WireName: "logging",
		Type: ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
		}},
	}
	types := []*ir.ResourceType{
		rt("aws_svc_thing", loggingBlock),
		rt("aws_svc_thing_logging",
			scalarField("id", ir.ScalarString, false, false, true, false),
			scalarField("target", ir.ScalarString, false, true, false, false),
		),
	}
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types)
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	thingSrc, ok := files["aws/svc/thing.ts"]
	if !ok {
		t.Fatalf("expected aws/svc/thing.ts, got paths: %v", keys(files))
	}
	loggingSrc, ok := files["aws/svc/thing_logging.ts"]
	if !ok {
		t.Fatalf("expected aws/svc/thing_logging.ts, got paths: %v", keys(files))
	}

	mustContain(t, thingSrc, "export interface Thing_Logging {\n  enabled: boolean;\n}")
	mustContain(t, loggingSrc, "export interface ThingLoggingConfig {")
	mustContain(t, loggingSrc, "export const ThingLogging:")
	mustNotContain(t, thingSrc, "export interface ThingLogging {")

	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("GeneratedRepo output has a real declaration collision: %v", err)
	}

	requireDeno(t)
	checkGeneratedRepo(t, files)
}

// requireDeno skips when `deno` isn't on PATH -- matches this project's
// own requireGoToolchain/requireHermeticSandbox pattern (failing open
// rather than panicking when an ordinarily-present tool is missing).
func requireDeno(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH -- skipping the real `deno check` half of this test")
	}
}

// checkGeneratedRepo writes files (a GeneratedRepo's own output) to a
// throwaway directory alongside a deno.json import map pointing the bare
// "@ubx/sdk" specifier every generated file imports at the real
// sdk/ts/runtime/src/index.ts source (mirroring exactly how
// tseval's own extractAssets wires up "@ubx/sdk" for a real evaluation,
// tseval/assets.go -- reused in spirit here, not by direct import, since
// that package is unexported and this test needs only the one import-map
// step, not the rest of the evaluator harness), then runs
// `deno check --no-remote` against every ".ts" file in the tree -- the
// strongest, most direct proof available that a fix actually holds: not
// a string comparison, the real TypeScript checker.
func checkGeneratedRepo(t *testing.T, files map[string]string) {
	t.Helper()

	sdkTSRuntime := sdkTSRuntimeIndexPath(t)
	dir := t.TempDir()
	var tsFiles []string
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if filepath.Ext(full) == ".ts" {
			tsFiles = append(tsFiles, full)
		}
	}

	importMap := fmt.Sprintf(`{"imports":{"@ubx/sdk":%q}}`, sdkTSRuntime)
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte(importMap), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args := append([]string{"check", "--no-remote"}, tsFiles...)
	cmd := exec.CommandContext(ctx, "deno", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deno check of the generated repo failed:\n%s", out)
	}
}

// sdkTSRuntimeIndexPath resolves this repo's own real sdk/ts/runtime/src/index.ts
// (the @ubx/sdk runtime this package's own generated output imports) --
// shared by every test in this file that needs a real `deno check`.
func sdkTSRuntimeIndexPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not determine this test file's own path")
	}
	path, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "ts", "runtime", "src", "index.ts"))
	if err != nil {
		t.Fatalf("resolve sdk/ts/runtime/src/index.ts path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist (sdk/ts/runtime's own real index.ts): %v", path, err)
	}
	return path
}
