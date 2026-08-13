package gotmpl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/provider"
	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// requireConformanceLive skips t unless UBX_CONFORMANCE_LIVE=1 is set --
// same gate, same reasoning, as conformance.RequireLive (top-level
// conformance/harness.go): acquiring a real provider binary is a real
// network round trip (registry.opentofu.org, or whatever's already
// cached under ~/.ubx/providers), so this must never run as part of a
// default `go test ./...`. Defined locally rather than importing the
// top-level `conformance` package (a different, unrelated harness --
// AWS/GCP resource-type conformance -- that this package has no other
// reason to depend on) for one shared env var check.
func requireConformanceLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_CONFORMANCE_LIVE") != "1" {
		t.Skip("skipping: set UBX_CONFORMANCE_LIVE=1 to run against the real hashicorp/aws provider binary")
	}
}

// TestFullProvider_Go_CompilesClean is UBI-98's own required verification,
// automated and made permanent: `ubx sdk gen --lang go` against the REAL,
// FULL hashicorp/aws@6.54.0 provider (1,682 resource types) must produce
// a repo-shaped tree (go.mod + one package per derived AWS-service
// boundary) where `go build ./...` compiles EVERY package clean.
//
// This supersedes the UBI-96-era version of this same test, which could
// only ever assert zero redeclarations at full-provider scale and had to
// treat a real Go compiler crash ("internal compiler error: NewBulk too
// big" -- a hard ceiling on how much can live in ONE flat package,
// confirmed unrelated to naming) as a known, separately-tracked, named
// skip rather than a hard pass -- see STATE.md's UBI-96 session and the
// UBI-98 Linear thread for the full diagnosis. UBI-98's own
// per-service-package restructure is exactly what closes that gap: the
// largest real derived service package (ec2, 56 real types) was already
// confirmed, in that same prior session, to build clean and instantly on
// its own -- this test is the permanent, full-tree version of that same
// proof, now a HARD pass/fail with no compiler-crash carve-out at all.
//
// Gated behind UBX_CONFORMANCE_LIVE=1 (acquiring the real provider binary
// is a real network round trip the first time; free/no-credentials
// afterward -- GetProviderSchema is a pure local gRPC call, no Configure,
// no AWS account, see this project's own prior "a provider's own schema
// costs nothing" finding, STATE.md 2026-07-10) -- CLAUDE.md's own
// standing rule keeps `go test ./...` hermetic and credential-free by
// default; TestGeneratedRepo_CrossResourceNestedBlockVsSiblingResource_NoCollision
// (collision_test.go, same package) is this test's fully hermetic,
// always-on sibling, proving the identical fix with a small synthetic
// fixture instead of a real provider download.
func TestFullProvider_Go_CompilesClean(t *testing.T) {
	requireConformanceLive(t)
	requireGoToolchain(t)

	const source = "hashicorp/aws"
	const version = "6.54.0"

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	parsed, err := provider.ParseSource(source)
	if err != nil {
		t.Fatalf("provider.ParseSource(%q): %v", source, err)
	}
	acquired, err := provider.Acquire(ctx, parsed, version)
	if err != nil {
		t.Fatalf("provider.Acquire(%s@%s): %v", source, version, err)
	}
	client, err := provider.Launch(ctx, acquired.Path)
	if err != nil {
		t.Fatalf("provider.Launch: %v", err)
	}
	defer client.Close()

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		t.Fatalf("fetch schema: %v", err)
	}

	// The founder's own repro named this exact figure -- assert it
	// directly rather than silently testing whatever count a future
	// provider release happens to report, so a version bump that
	// changes the type count is a visible, deliberate test update, not
	// a silent scope-narrowing.
	if len(schemas.Resources) != 1682 {
		t.Fatalf("hashicorp/aws@%s: got %d resource types, want 1682 (the founder's own named full-provider repro count -- update this test deliberately if a provider version bump changed it)", version, len(schemas.Resources))
	}

	typeNames := make([]string, 0, len(schemas.Resources))
	for name := range schemas.Resources {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)

	types := make([]*ir.ResourceType, 0, len(typeNames))
	for _, name := range typeNames {
		resType, err := ir.FromSchema(name, schemas.Resources[name])
		if err != nil {
			t.Fatalf("ir.FromSchema(%q): %v", name, err)
		}
		types = append(types, resType)
	}

	files, err := GeneratedRepo("aws", source, version, types, "1.23")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	// UBI-106: every service package now nests under one shared "aws/"
	// namespace directory, so grouping by the FIRST "/" segment alone
	// would always report "1" regardless of real service count -- group
	// by the file's own full DIRECTORY (everything before the LAST "/")
	// instead, which still correctly distinguishes aws/iam from aws/ecr.
	servicePackages := map[string]bool{}
	for path := range files {
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			servicePackages[path[:idx]] = true
		}
	}
	t.Logf("full-provider repo tree: %d files across %d service packages", len(files), len(servicePackages))

	// This is UBI-96's own actual reported bug, re-verified in this new
	// per-package shape: zero redeclarations, checked per service
	// package across the whole real full-provider tree.
	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("full-provider generated repo has real package-level collisions:\n%v", err)
	}

	buildGeneratedRepoAtFullScale(t, files)
}

// buildGeneratedRepoAtFullScale is buildGeneratedRepo's own full-scale
// sibling: the SAME real, live full-provider tree GeneratedRepo produced
// above, written to disk and `go build ./...`'d for real. Unlike the
// UBI-96-era version of this test, this is now a HARD pass/fail with no
// "known NewBulk crash" carve-out -- UBI-98's own per-service-package
// split is specifically what makes that legitimate: no single package in
// this tree is anywhere near the ~73,000-declaration/~40MB scale that
// crashed the Go compiler under the old flat-package scheme (the largest
// real derived service, ec2, is 56 types -- already confirmed, in the
// session that diagnosed the crash, to build clean and instantly on its
// own). Any build failure here is therefore a real regression, not a
// known, separately-tracked limitation.
func buildGeneratedRepoAtFullScale(t *testing.T, files map[string]string) {
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

	// UBI-138: the Go module now lives in the tree's own "sdk/go/"
	// subdirectory (GeneratedRepo's own doc comment has the full
	// account), so both the go.mod rewrite and the build itself target
	// dir/sdk/go, never dir's own root.
	goModDir := filepath.Join(dir, "sdk", "go")
	sdkGoRoot := sdkGoModuleRoot(t)
	goMod := files["sdk/go/go.mod"] + "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
	if err := os.WriteFile(filepath.Join(goModDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = goModDir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build of the full-provider generated repo tree failed -- this is now a hard failure, UBI-98's per-service-package split having removed the known NewBulk-crash carve-out this test used to have:\n%s", out)
	}
}
