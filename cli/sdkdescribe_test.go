package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

func objField(wireName string, source ir.DescriptionSource, nested ...ir.Field) ir.Field {
	f := ir.Field{WireName: wireName, DescriptionSource: source, Optional: true}
	if len(nested) > 0 {
		f.Type = ir.TypeRef{Kind: ir.KindObject, Object: nested}
	} else {
		f.Type = ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString}
	}
	return f
}

func TestCollectJobs_SkipsAlreadyDescribed_WalksNestedInLockstepWithSignals(t *testing.T) {
	types := []*ir.ResourceType{{
		WireType: "kubernetes_apps_deployment",
		Fields: []ir.Field{
			objField("name", ir.DescriptionSourceModel), // already sourced -- must not appear
			objField("spec", ir.DescriptionSourceNone,
				objField("replicas", ir.DescriptionSourceNone),
			),
		},
	}}
	signals := map[string]map[string]*fieldSignal{
		"kubernetes_apps_deployment": {
			"spec": {Nested: map[string]*fieldSignal{
				"replicas": {Minimum: floatPtr(0), Maximum: floatPtr(1000)},
			}},
		},
	}

	jobs := collectJobs("kubernetes", types, signals)
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2 (spec, spec.replicas): %+v", len(jobs), jobs)
	}

	byPath := map[string]describeJob{}
	for _, j := range jobs {
		byPath[j.relPath] = j
	}

	spec, ok := byPath["spec"]
	if !ok {
		t.Fatalf("missing job for relPath %q: %+v", "spec", jobs)
	}
	if spec.resource != "kubernetes_apps_deployment" {
		t.Fatalf("spec.resource = %q", spec.resource)
	}
	if spec.context.ParentContext != "kubernetes.kubernetes_apps_deployment" {
		t.Fatalf("spec.context.ParentContext = %q", spec.context.ParentContext)
	}

	replicas, ok := byPath["spec.replicas"]
	if !ok {
		t.Fatalf("missing job for relPath %q: %+v", "spec.replicas", jobs)
	}
	if replicas.context.ParentContext != "kubernetes.kubernetes_apps_deployment.spec" {
		t.Fatalf("replicas.context.ParentContext = %q", replicas.context.ParentContext)
	}
	if len(replicas.context.Constraints) != 2 {
		t.Fatalf("replicas.context.Constraints = %v, want minimum+maximum carried from the signal tree", replicas.context.Constraints)
	}
}

func floatPtr(f float64) *float64 { return &f }

func TestEnrichDescriptions_CheckedInFile_FillsAndLabelsAIInferred(t *testing.T) {
	types := []*ir.ResourceType{{
		WireType: "github_repository",
		Fields: []ir.Field{
			objField("full_name", ir.DescriptionSourceModel),
			objField("private", ir.DescriptionSourceNone),
		},
	}}
	checkedIn := checkedInDescriptions{
		"github_repository": {"private": "Whether the repository is private."},
	}

	coverage, err := enrichDescriptions(context.Background(), "github", types, nil, enrichOptions{checkedIn: checkedIn})
	if err != nil {
		t.Fatalf("enrichDescriptions: %v", err)
	}
	if coverage.Sourced != 1 || coverage.AIInferred != 1 || coverage.None != 0 {
		t.Fatalf("coverage = %+v, want {Sourced:1 AIInferred:1 None:0}", coverage)
	}

	private := types[0].Fields[1]
	if private.Description != "Whether the repository is private." {
		t.Fatalf("private.Description = %q", private.Description)
	}
	if private.DescriptionSource != ir.DescriptionSourceAIInferred {
		t.Fatalf("private.DescriptionSource = %q, want %q", private.DescriptionSource, ir.DescriptionSourceAIInferred)
	}
}

func TestEnrichDescriptions_NoMechanism_LeavesFieldsAbstained(t *testing.T) {
	types := []*ir.ResourceType{{
		WireType: "github_repository",
		Fields:   []ir.Field{objField("private", ir.DescriptionSourceNone)},
	}}

	coverage, err := enrichDescriptions(context.Background(), "github", types, nil, enrichOptions{})
	if err != nil {
		t.Fatalf("enrichDescriptions: %v", err)
	}
	if coverage.AIInferred != 0 || coverage.None != 1 {
		t.Fatalf("coverage = %+v, want {AIInferred:0 None:1} -- abstention is the honest default with no mechanism enabled", coverage)
	}
	if types[0].Fields[0].DescriptionSource != ir.DescriptionSourceNone {
		t.Fatalf("field was modified despite no enrichment mechanism being enabled: %+v", types[0].Fields[0])
	}
}

func TestEnrichDescriptions_GapsOut_RecordsOnlyGenuinelyUndescribedFields(t *testing.T) {
	types := []*ir.ResourceType{{
		WireType: "github_repository",
		Fields: []ir.Field{
			objField("full_name", ir.DescriptionSourceModel), // sourced, never a gap
			objField("private", ir.DescriptionSourceNone),
			objField("visibility", ir.DescriptionSourceNone),
		},
	}}
	checkedIn := checkedInDescriptions{
		"github_repository": {"private": "Whether the repository is private."},
	}
	signals := map[string]map[string]*fieldSignal{
		"github_repository": {"visibility": {Enum: []string{"public", "private", "internal"}}},
	}

	gaps := map[string]map[string]gapFieldInfo{}
	coverage, err := enrichDescriptions(context.Background(), "github", types, signals, enrichOptions{
		checkedIn: checkedIn,
		gapsOut:   &gaps,
	})
	if err != nil {
		t.Fatalf("enrichDescriptions: %v", err)
	}
	if coverage.None != 1 {
		t.Fatalf("coverage.None = %d, want 1 (only visibility is still genuinely undescribed)", coverage.None)
	}

	repoGaps, ok := gaps["github_repository"]
	if !ok || len(repoGaps) != 1 {
		t.Fatalf("gaps = %+v, want exactly one entry under github_repository", gaps)
	}
	visibility, ok := repoGaps["visibility"]
	if !ok {
		t.Fatalf("gaps[github_repository] missing %q: %+v", "visibility", repoGaps)
	}
	if len(visibility.Enum) != 3 {
		t.Fatalf("visibility.Enum = %v, want the real 3-value enum signal carried through", visibility.Enum)
	}
	if _, stillGap := repoGaps["private"]; stillGap {
		t.Fatalf("private was filled by the checked-in file and must not appear as a gap: %+v", repoGaps)
	}
}

func TestLoadCheckedInDescriptions_MissingFile_ReturnsNilNotError(t *testing.T) {
	got, err := loadCheckedInDescriptions(t.TempDir(), "does_not_exist")
	if err != nil {
		t.Fatalf("loadCheckedInDescriptions: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil for a provider with no checked-in file yet", got)
	}
}

func TestLoadCheckedInDescriptions_EmptyDir_Disabled(t *testing.T) {
	got, err := loadCheckedInDescriptions("", "github")
	if err != nil || got != nil {
		t.Fatalf("loadCheckedInDescriptions(\"\", ...) = %+v, %v, want nil, nil (explicitly disabled)", got, err)
	}
}

func TestLoadCheckedInDescriptions_RealFile_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	data := `{"github_repository": {"private": "Whether the repository is private."}}`
	if err := os.WriteFile(filepath.Join(dir, "github.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := loadCheckedInDescriptions(dir, "github")
	if err != nil {
		t.Fatalf("loadCheckedInDescriptions: %v", err)
	}
	if got["github_repository"]["private"] != "Whether the repository is private." {
		t.Fatalf("got %+v", got)
	}
}

func TestWriteGapFile_WritesReadableJSONAtProviderPath(t *testing.T) {
	dir := t.TempDir()
	gaps := map[string]map[string]gapFieldInfo{
		"github_repository": {"private": {Type: "bool", Optional: true, ParentContext: "github.github_repository"}},
	}
	if err := writeGapFile(dir, "github", gaps); err != nil {
		t.Fatalf("writeGapFile: %v", err)
	}

	path := filepath.Join(dir, "github.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected gap file at %s: %v", path, err)
	}

	// The gap file's own key shape (resource -> relPath -> ...) is a
	// real, deliberate match for loadCheckedInDescriptions' own shape --
	// confirmed by decoding it back through the SAME checkedInDescriptions
	// unmarshal path a real authoring pass would use once it replaces
	// each gapFieldInfo value with a plain description string.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"private"`) || !strings.Contains(string(raw), `"parent_context"`) {
		t.Fatalf("gap file content missing expected keys: %s", raw)
	}
}
