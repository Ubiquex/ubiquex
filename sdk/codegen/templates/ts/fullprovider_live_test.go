package ts

import (
	"context"
	"fmt"
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
// same gate, same reasoning, as sdk/codegen/templates/go's own function
// of the same name (fullprovider_live_test.go there): acquiring a real
// provider binary is a real network round trip the first time, so this
// must never run as part of a default `go test ./...`.
func requireConformanceLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_CONFORMANCE_LIVE") != "1" {
		t.Skip("skipping: set UBX_CONFORMANCE_LIVE=1 to run against the real hashicorp/aws provider binary")
	}
}

// TestFullProvider_TS_ChecksClean is UBI-98's own required verification
// for TypeScript, mirroring sdk/codegen/templates/go's own
// TestFullProvider_Go_CompilesClean exactly in shape and intent: `ubx sdk
// gen --lang ts` against the REAL, FULL hashicorp/aws@6.54.0 provider
// (1,682 resource types) must produce a repo-shaped tree where
// `deno check --no-remote` accepts every generated file clean.
//
// Unlike Go, this was NOT chasing a known crash -- checked explicitly
// this session, not assumed: a synthetic single-file reproduction of
// Go's own real aws_wafv2_web_acl_rule finding (16.7MB/~253,000 lines of
// naively-unrolled TS, the worst real case in the whole provider)
// type-checked clean in ~2 real seconds before any dedup fix was even
// applied. This test is still made permanent, at true full-provider
// scale, for the same reason Go's is: it is the ticket's own explicit
// required check, and "checked once by hand this session" is not the
// same guarantee as "a permanent, always-available regression check."
//
// Gated behind UBX_CONFORMANCE_LIVE=1, same reasoning as Go's own
// sibling test; TestGeneratedRepo_CrossResourceNestedBlockVsSiblingResource_NoCollision
// (collision_test.go, same package) is this test's fully hermetic,
// always-on sibling, proving the identical fix with a small synthetic
// fixture instead of a real provider download.
func TestFullProvider_TS_ChecksClean(t *testing.T) {
	requireConformanceLive(t)
	requireDeno(t)

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

	// Matches Go's own live test's explicit count assertion -- a version
	// bump that changes the type count should be a deliberate test
	// update, not a silent scope-narrowing.
	if len(schemas.Resources) != 1682 {
		t.Fatalf("hashicorp/aws@%s: got %d resource types, want 1682 (update this test deliberately if a provider version bump changed it)", version, len(schemas.Resources))
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

	files, err := GeneratedRepo("aws", source, version, types)
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	// UBI-106: every service directory now nests under one shared "aws/"
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
	t.Logf("full-provider repo tree: %d files across %d service directories", len(files), len(servicePackages))

	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("full-provider generated repo has real declaration collisions:\n%v", err)
	}

	checkGeneratedRepoAtFullScale(t, files)
}

// checkGeneratedRepoAtFullScale is checkGeneratedRepo's own full-scale
// sibling: the SAME real, live full-provider tree GeneratedRepo produced
// above, written to disk and `deno check --no-remote`'d for real, all
// 1,682 types' worth of files in one invocation.
func checkGeneratedRepoAtFullScale(t *testing.T, files map[string]string) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	args := append([]string{"check", "--no-remote"}, tsFiles...)
	cmd := exec.CommandContext(ctx, "deno", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deno check of the full-provider generated repo tree failed:\n%s", out)
	}
}
