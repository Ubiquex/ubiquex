package gotmpl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// TestCheckNoDuplicateDeclarations_DetectsRealCollision proves the checker
// itself catches a genuine Go redeclaration -- a `var Foo` and a `type Foo
// struct` sharing one package-level identifier, Go's own real failure mode
// for UBI-96 (var and type share ONE namespace at package level, unlike
// TypeScript's separate type/value namespaces -- see this package's own
// resourceRenderer doc comment). Independent of GeneratedRepo: hand-built
// source strings, so this exercises the checker's own parsing/reporting
// logic directly, not just "does today's fixed output happen to be clean."
// Both declarations in the SAME file here; TestCheckNoDuplicateDeclarations_DetectsCollisionAcrossFiles,
// below, proves the harder case UBI-98 actually introduced: a collision
// split across two SIBLING files of one package.
func TestCheckNoDuplicateDeclarations_DetectsRealCollision(t *testing.T) {
	src := `package generated

type Foo struct {
	Bar any
}

var Foo = struct{}{}
`
	err := CheckNoDuplicateDeclarations(map[string]string{"a.go": src})
	if err == nil {
		t.Fatal("expected an error for a package with both `type Foo` and `var Foo`, got nil")
	}
	if !strings.Contains(err.Error(), "Foo") {
		t.Fatalf("expected the error to name the colliding identifier %q, got: %v", "Foo", err)
	}
}

// TestCheckNoDuplicateDeclarations_DetectsCollisionAcrossFiles is UBI-98's
// own real shape: GeneratedRepo emits one FILE per resource type, all
// sharing one service package's own directory -- a collision between two
// SIBLING files must be caught exactly as hard as one within a single
// file, since Go's package-level namespace spans every file in a
// directory combined, not just one file at a time.
func TestCheckNoDuplicateDeclarations_DetectsCollisionAcrossFiles(t *testing.T) {
	files := map[string]string{
		"a.go": "package generated\n\ntype Foo struct {\n\tBar any\n}\n",
		"b.go": "package generated\n\nvar Foo = struct{}{}\n",
	}
	err := CheckNoDuplicateDeclarations(files)
	if err == nil {
		t.Fatal("expected an error for a cross-file `type Foo`/`var Foo` collision, got nil")
	}
	if !strings.Contains(err.Error(), "Foo") {
		t.Fatalf("expected the error to name the colliding identifier %q, got: %v", "Foo", err)
	}
}

func TestCheckNoDuplicateDeclarations_CleanSource_NoError(t *testing.T) {
	files := map[string]string{
		"a.go": "package generated\n\ntype Foo struct {\n\tBar any\n}\n",
		"b.go": "package generated\n\nvar Baz = struct{}{}\n",
	}
	if err := CheckNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("expected no error for a collision-free package, got: %v", err)
	}
}

// TestCheckRepoNoDuplicateDeclarations_DifferentPackages_NeverCollide is
// this restructure's own central proof: the SAME identifier name in TWO
// DIFFERENT service packages (separate directories) is never a
// collision, since they are separate Go compilation units by
// construction -- the whole reason UBI-98's per-service-package split
// exists.
func TestCheckRepoNoDuplicateDeclarations_DifferentPackages_NeverCollide(t *testing.T) {
	repo := map[string]string{
		"go.mod":       "module github.com/ubiquex/ubx-sdk-aws\n\ngo 1.23\n",
		"ecr/thing.go": "package ecr\n\ntype Thing struct {\n\tBar any\n}\n",
		"iam/thing.go": "package iam\n\ntype Thing struct {\n\tBar any\n}\n",
		"iam/other.go": "package iam\n\nvar Other = 1\n",
	}
	if err := CheckRepoNoDuplicateDeclarations(repo); err != nil {
		t.Fatalf("identically-named types in different service packages should never collide: %v", err)
	}
}

func TestCheckRepoNoDuplicateDeclarations_SamePackageAcrossFiles_Collides(t *testing.T) {
	repo := map[string]string{
		"go.mod":   "module github.com/ubiquex/ubx-sdk-aws\n\ngo 1.23\n",
		"iam/a.go": "package iam\n\ntype Role struct {\n\tBar any\n}\n",
		"iam/b.go": "package iam\n\nvar Role = struct{}{}\n",
	}
	err := CheckRepoNoDuplicateDeclarations(repo)
	if err == nil {
		t.Fatal("expected an error for a same-package cross-file collision, got nil")
	}
	if !strings.Contains(err.Error(), "iam") || !strings.Contains(err.Error(), "Role") {
		t.Fatalf("expected the error to name the package (iam) and identifier (Role), got: %v", err)
	}
}

// TestGeneratedRepo_CrossResourceNestedBlockVsSiblingResource_NoCollision
// reproduces UBI-96's own real, live-verified shape directly, now scoped
// to ONE service package (UBI-98's own restructure): AWS's own
// convention of splitting a legacy inline nested block out into its own
// standalone resource. aws_thing's own "logging" nested block and the
// separate aws_thing_logging resource are the exact same shape as the
// real aws_s3_bucket / aws_s3_bucket_logging collision found live
// against hashicorp/aws@6.54.0 (STATE.md/UBI-96 session) -- before the
// "_" join fix, both would derive the bare local name "ThingLogging" (a
// `type` from the first, a `var` from the second, in the SAME service
// package's own two separate files), which is exactly what failed to
// `go build` at full-provider scale.
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

	// Both real types share one derived service package ("svc").
	thingSrc, ok := files["go/aws/svc/thing.go"]
	if !ok {
		t.Fatalf("expected go/aws/svc/thing.go, got paths: %v", keys(files))
	}
	loggingSrc, ok := files["go/aws/svc/thing_logging.go"]
	if !ok {
		t.Fatalf("expected go/aws/svc/thing_logging.go, got paths: %v", keys(files))
	}

	// The nested struct (from aws_svc_thing's own "logging" block) is
	// now namespaced with an underscore -- never the bare concatenation
	// that would collide with aws_svc_thing_logging's own resource-level
	// names.
	mustContain(t, thingSrc, "type Thing_Logging struct {\n\tEnabled any\n}")
	// aws_svc_thing_logging (a REAL, distinct resource, same package)
	// still gets its own unqualified Config/ResourceBinding names,
	// completely unaffected.
	mustContain(t, loggingSrc, "type ThingLoggingConfig struct {")
	mustContain(t, loggingSrc, "var ThingLogging = ubx.ResourceBinding{")
	mustNotContain(t, thingSrc, "type ThingLogging struct {")

	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("GeneratedRepo output has a real package-level collision: %v", err)
	}

	requireGoToolchain(t)
	buildGeneratedRepo(t, files)
}

// requireGoToolchain skips when `go` isn't on PATH -- go build ./sdk/...
// already requires a Go toolchain to run this test file at all, so this
// only ever actually skips in a deliberately stripped-down environment;
// matches this project's own requireDeno/requireHermeticSandbox pattern
// (sdk/conformance/runner/runner_test.go) of failing open rather than
// panicking when an ordinarily-present tool is missing.
func requireGoToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not found in PATH -- skipping the real `go build` half of this test")
	}
}

// buildGeneratedRepo writes files (a GeneratedRepo's own output: go.mod +
// every service package's own subdirectory/files) to a throwaway
// directory, rewrites the emitted go.mod to ALSO replace
// github.com/ubiquex/ubx-sdk-go with THIS repo's own real sdk/go (the
// generator's own emitted go.mod deliberately has no such replace --
// see GeneratedRepo's own doc comment -- a real consumer would pin a
// real published version; only this test needs the local override), then
// runs `go build ./...` against it -- the strongest, most direct proof
// available that a fix actually holds: not a string comparison, a real
// compiler, across every package in the tree at once (the same shape
// TestFullProvider_Go_CompilesClean uses at real full-provider scale).
func buildGeneratedRepo(t *testing.T, files map[string]string) {
	t.Helper()

	dir := t.TempDir()
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// UBI-138: the Go module now lives in the tree's own "go/"
	// subdirectory (GeneratedRepo's own doc comment has the full
	// account), so both the go.mod rewrite and the build itself target
	// dir/go, never dir's own root.
	goModDir := filepath.Join(dir, "go")
	sdkGoRoot := sdkGoModuleRoot(t)
	goMod := files["go/go.mod"] + "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
	if err := os.WriteFile(filepath.Join(goModDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = goModDir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build of the generated repo failed:\n%s", out)
	}
}

// sdkGoModuleRoot resolves this repo's own sdk/go module root (the
// hand-authored runtime every generated Go binding requires+replaces) --
// shared by buildGeneratedRepo and fullprovider_live_test.go's own
// buildGeneratedRepoAtFullScale so both compute the identical, verified
// path.
func sdkGoModuleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not determine this test file's own path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "go"))
	if err != nil {
		t.Fatalf("resolve sdk/go path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected %s to be sdk/go's own module root (containing go.mod): %v", root, err)
	}
	return root
}
