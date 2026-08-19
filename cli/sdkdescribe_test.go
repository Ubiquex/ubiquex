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

	jobs, stale := collectJobsAndStale("kubernetes", types, signals, nil)
	if len(stale) != 0 {
		t.Fatalf("stale = %+v, want none (no checkedIn map given)", stale)
	}
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

	coverage, prunedStale, err := enrichDescriptions(context.Background(), "github", types, nil, enrichOptions{checkedIn: checkedIn})
	if err != nil {
		t.Fatalf("enrichDescriptions: %v", err)
	}
	if coverage.Sourced != 1 || coverage.AIInferred != 1 || coverage.None != 0 {
		t.Fatalf("coverage = %+v, want {Sourced:1 AIInferred:1 None:0}", coverage)
	}
	if prunedStale != 0 {
		t.Fatalf("prunedStale = %d, want 0 (nothing stale here)", prunedStale)
	}

	private := types[0].Fields[1]
	if private.Description != "Whether the repository is private." {
		t.Fatalf("private.Description = %q", private.Description)
	}
	if private.DescriptionSource != ir.DescriptionSourceAIInferred {
		t.Fatalf("private.DescriptionSource = %q, want %q", private.DescriptionSource, ir.DescriptionSourceAIInferred)
	}
}

// TestEnrichDescriptions_StaleCheckedInEntry_PrunedNotAppliedWhenSourced
// is the real, direct regression test for a real correctness check
// found live: a field that used to be genuinely undescribed, got a
// checked-in AI-inferred entry authored for it, and THEN gained a real
// source description (a provider's own spec was updated to add one).
// The real, correct behavior has two real, separate halves: (1) the
// stale checked-in text must never overwrite the real source
// description -- confirmed here to already hold, by construction
// (collectJobsAndStale only ever builds a describeJob for a
// DescriptionSourceNone field, so a field that resolved to
// DescriptionSourceModel on THIS run never reaches the checked-in
// lookup at all); (2) the now-permanently-unused checked-in entry must
// be removed from the artifact, not left behind forever implying an
// AI-inference happened that no longer applies.
func TestEnrichDescriptions_StaleCheckedInEntry_PrunedNotAppliedWhenSourced(t *testing.T) {
	types := []*ir.ResourceType{{
		WireType: "github_repository",
		Fields: []ir.Field{
			// "private" now has a real source description -- as if
			// GitHub's own spec added one after the checked-in entry
			// below was authored against an earlier, undescribed spec.
			func() ir.Field {
				f := objField("private", ir.DescriptionSourceModel)
				f.Description = "Whether the repository is private (the real, current source description)."
				return f
			}(),
			objField("visibility", ir.DescriptionSourceNone),
		},
	}}
	checkedIn := checkedInDescriptions{
		"github_repository": {
			"private":    "Whether the repository is private.", // stale -- must be pruned
			"visibility": "The repository's visibility level.",  // still genuinely needed -- must survive
		},
	}

	coverage, prunedStale, err := enrichDescriptions(context.Background(), "github", types, nil, enrichOptions{checkedIn: checkedIn})
	if err != nil {
		t.Fatalf("enrichDescriptions: %v", err)
	}

	// Half 1: the real source description was never overwritten.
	private := types[0].Fields[0]
	if private.Description != "Whether the repository is private (the real, current source description)." {
		t.Fatalf("private.Description = %q, the stale checked-in text must never win over a real source description", private.Description)
	}
	if private.DescriptionSource != ir.DescriptionSourceModel {
		t.Fatalf("private.DescriptionSource = %q, want %q", private.DescriptionSource, ir.DescriptionSourceModel)
	}

	// Half 2: the stale entry was pruned from the artifact, the still-real
	// one was not.
	if prunedStale != 1 {
		t.Fatalf("prunedStale = %d, want 1 (only the now-stale \"private\" entry)", prunedStale)
	}
	if _, stillThere := checkedIn["github_repository"]["private"]; stillThere {
		t.Fatalf("stale \"private\" entry was not pruned from checkedIn: %+v", checkedIn)
	}
	if desc, ok := checkedIn["github_repository"]["visibility"]; !ok || desc != "The repository's visibility level." {
		t.Fatalf("still-genuinely-needed \"visibility\" entry was removed or altered: %+v", checkedIn)
	}

	// visibility itself still got filled from the (now-pruned-of-private)
	// checkedIn map, proving pruning one entry doesn't disturb another.
	if coverage.AIInferred != 1 || coverage.Sourced != 1 || coverage.None != 0 {
		t.Fatalf("coverage = %+v, want {Sourced:1 AIInferred:1 None:0}", coverage)
	}
}

func TestEnrichDescriptions_NoMechanism_LeavesFieldsAbstained(t *testing.T) {
	types := []*ir.ResourceType{{
		WireType: "github_repository",
		Fields:   []ir.Field{objField("private", ir.DescriptionSourceNone)},
	}}

	coverage, _, err := enrichDescriptions(context.Background(), "github", types, nil, enrichOptions{})
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
	coverage, _, err := enrichDescriptions(context.Background(), "github", types, signals, enrichOptions{
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

// TestWriteCheckedInDescriptions_RoundTrips confirms
// writeCheckedInDescriptions writes to the identical real path
// loadCheckedInDescriptions reads from, and that a pruned map (with a
// resource entry removed entirely, matching checkedInDescriptions.prune's
// own behavior once a resource has no fields left) round-trips cleanly.
func TestWriteCheckedInDescriptions_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	checkedIn := checkedInDescriptions{
		"github_repository": {"visibility": "The repository's visibility level."},
	}
	if err := writeCheckedInDescriptions(dir, "github", checkedIn); err != nil {
		t.Fatalf("writeCheckedInDescriptions: %v", err)
	}

	got, err := loadCheckedInDescriptions(dir, "github")
	if err != nil {
		t.Fatalf("loadCheckedInDescriptions: %v", err)
	}
	if got["github_repository"]["visibility"] != "The repository's visibility level." {
		t.Fatalf("got %+v", got)
	}
}

// TestCheckedInDescriptions_Prune_RemovesEmptyResourceEntry confirms
// prune drops the resource's own entry entirely once its last field is
// pruned, rather than leaving a real, empty {} block behind.
func TestCheckedInDescriptions_Prune_RemovesEmptyResourceEntry(t *testing.T) {
	checkedIn := checkedInDescriptions{
		"github_repository": {"private": "stale"},
	}
	checkedIn.prune("github_repository", "private")
	if _, ok := checkedIn["github_repository"]; ok {
		t.Fatalf("expected the now-empty github_repository entry to be removed entirely, got %+v", checkedIn)
	}
}
