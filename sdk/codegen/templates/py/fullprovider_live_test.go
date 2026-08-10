package pytmpl

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/provider"
	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// requireConformanceLive skips t unless UBX_CONFORMANCE_LIVE=1 is set --
// same gate, same reasoning, as sdk/codegen/templates/go and .../ts's
// own function of the same name: acquiring a real provider binary is a
// real network round trip the first time, so this must never run as
// part of a default `go test ./...`.
func requireConformanceLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_CONFORMANCE_LIVE") != "1" {
		t.Skip("skipping: set UBX_CONFORMANCE_LIVE=1 to run against the real hashicorp/aws provider binary")
	}
}

// TestFullProvider_Py_ImportsClean is UBI-98's own required verification
// for Python, mirroring sdk/codegen/templates/go and .../ts's own
// TestFullProvider_*_CompilesClean/ChecksClean tests exactly in shape and
// intent: `ubx sdk gen --lang py` against the REAL, FULL
// hashicorp/aws@6.54.0 provider (1,682 resource types) must produce a
// repo-shaped tree where a real Python interpreter can import EVERY
// generated module clean.
//
// Unlike Go, this was NOT chasing a known crash -- checked explicitly
// this session, not assumed: a synthetic single-file reproduction of
// Go's own real aws_wafv2_web_acl_rule finding (21.2MB/~258,000 lines/
// 21,026 dataclasses of naively-unrolled Python, the worst real case in
// the whole provider) imported clean in ~4.4 real seconds before any
// dedup fix was even applied. This test is still made permanent, at true
// full-provider scale, for the same reason Go's is: it is the ticket's
// own explicit required check.
//
// Gated behind UBX_CONFORMANCE_LIVE=1, same reasoning as Go/TS's own
// sibling tests; TestGeneratedRepo_CrossResourceNestedBlockVsSiblingResource_NoCollision
// (collision_test.go, same package) is this test's fully hermetic,
// always-on sibling.
func TestFullProvider_Py_ImportsClean(t *testing.T) {
	requireConformanceLive(t)
	requirePython3(t)

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

	// UBI-106: every service package now nests under one shared "aws/"
	// namespace directory (now itself nested one level further under the
	// shared "ubx/" namespace-package root), so grouping by the FIRST
	// "/" segment alone would always report "1" regardless of real
	// service count -- group by the file's own full DIRECTORY (everything
	// before the LAST "/") instead, which still correctly distinguishes
	// ubx/aws/iam from ubx/aws/ecr (off by one here specifically:
	// ubx/aws/__init__.py's own directory, "ubx/aws", counts as one extra
	// entry alongside the real per-service ones -- a cosmetic log-count
	// quirk, not worth a special case).
	servicePackages := map[string]bool{}
	for path := range files {
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			servicePackages[path[:idx]] = true
		}
	}
	t.Logf("full-provider repo tree: %d files across %d service packages", len(files), len(servicePackages))

	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("full-provider generated repo has real declaration collisions:\n%v", err)
	}

	importGeneratedRepoAtFullScale(t, files)
}

// importGeneratedRepoAtFullScale is importGeneratedRepo's own full-scale
// sibling (collision_test.go's own importGeneratedRepoWithTimeout, given
// a longer budget): the SAME real, live full-provider tree GeneratedRepo
// produced above, written to disk and imported for real, EVERY one of
// its ~1,682 modules in one Python subprocess invocation.
func importGeneratedRepoAtFullScale(t *testing.T, files map[string]string) {
	t.Helper()
	importGeneratedRepoWithTimeout(t, files, 300*time.Second)
}
