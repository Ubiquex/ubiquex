package gotmpl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
// resourceRenderer doc comment). Independent of GeneratedFile: a hand-built
// source string, so this exercises the checker's own parsing/reporting
// logic directly, not just "does today's fixed output happen to be clean."
func TestCheckNoDuplicateDeclarations_DetectsRealCollision(t *testing.T) {
	src := `package generated

type Foo struct {
	Bar any
}

var Foo = struct{}{}
`
	err := CheckNoDuplicateDeclarations(src)
	if err == nil {
		t.Fatal("expected an error for a package with both `type Foo` and `var Foo`, got nil")
	}
	if !strings.Contains(err.Error(), "Foo") {
		t.Fatalf("expected the error to name the colliding identifier %q, got: %v", "Foo", err)
	}
}

func TestCheckNoDuplicateDeclarations_CleanSource_NoError(t *testing.T) {
	src := `package generated

type Foo struct {
	Bar any
}

var Baz = struct{}{}
`
	if err := CheckNoDuplicateDeclarations(src); err != nil {
		t.Fatalf("expected no error for a collision-free package, got: %v", err)
	}
}

// TestGeneratedFile_CrossResourceNestedBlockVsSiblingResource_NoCollision
// reproduces UBI-96's own real, live-verified shape directly: AWS's own
// convention of splitting a legacy inline nested block out into its own
// standalone resource. aws_thing's own "logging" nested block and the
// separate aws_thing_logging resource are the exact same shape as the real
// aws_s3_bucket / aws_s3_bucket_logging collision found live against
// hashicorp/aws@6.54.0 (STATE.md/this session) -- before the "_" join fix,
// both would derive the bare name "AwsThingLogging" (a `type` from the
// first, a `var` from the second), which is exactly what failed to `go
// build` at full-provider scale.
func TestGeneratedFile_CrossResourceNestedBlockVsSiblingResource_NoCollision(t *testing.T) {
	loggingBlock := ir.Field{
		WireName: "logging",
		Type: ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
		}},
	}
	types := []*ir.ResourceType{
		rt("aws_thing", loggingBlock),
		rt("aws_thing_logging",
			scalarField("id", ir.ScalarString, false, false, true, false),
			scalarField("target", ir.ScalarString, false, true, false, false),
		),
	}
	out, err := GeneratedFile("generated", "hashicorp/aws", "6.54.0", types)
	if err != nil {
		t.Fatalf("GeneratedFile: %v", err)
	}

	// The nested struct (from aws_thing's own "logging" block) is now
	// namespaced with an underscore -- never the bare concatenation that
	// would collide with aws_thing_logging's own resource-level names.
	mustContain(t, out, "type AwsThing_Logging struct {\n\tEnabled any\n}")
	// aws_thing_logging (a REAL, distinct resource) still gets its own
	// unqualified Config/ResourceBinding names, completely unaffected.
	mustContain(t, out, "type AwsThingLoggingConfig struct {")
	mustContain(t, out, "var AwsThingLogging = sdk.ResourceBinding{")
	mustNotContain(t, out, "type AwsThingLogging struct {")

	if err := CheckNoDuplicateDeclarations(out); err != nil {
		t.Fatalf("GeneratedFile output has a real package-level collision: %v", err)
	}

	requireGoToolchain(t)
	buildGeneratedModule(t, out)
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

// buildGeneratedModule writes generatedSrc (package generated) into a
// fresh, throwaway Go module that requires+replaces
// github.com/ubiquex/ubx-sdk-go with THIS repo's own real sdk/go (the
// same local-replace shape sdk/conformance/programs/go/go.mod already
// uses for real), then runs `go build ./...` against it -- the strongest,
// most direct proof available that a collision fix actually holds: not a
// string comparison, a real compiler. Deliberately network-free
// (GOPROXY=off): the only external dependency is a local filesystem
// path, so this needs no live provider, no cache, nothing beyond the Go
// toolchain already required to run `go test` on this repo at all --
// this is the fully hermetic sibling of TestFullProvider_Go_CompilesClean
// (fullprovider_live_test.go), which does the identical `go build` proof
// but at real full-provider scale, gated behind UBX_CONFORMANCE_LIVE=1.
func buildGeneratedModule(t *testing.T, generatedSrc string) {
	t.Helper()

	sdkGoRoot := sdkGoModuleRoot(t)
	dir := t.TempDir()
	goMod := "module ubx-sdk-gen-collision-check\n\ngo 1.23\n\nrequire github.com/ubiquex/ubx-sdk-go v0.0.0\n\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "generated.go"), []byte(generatedSrc), 0o644); err != nil {
		t.Fatalf("write generated.go: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build of the generated package failed:\n%s", out)
	}
}
