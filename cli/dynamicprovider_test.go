package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPinnedSchemaFields_NoSource_NotPinned(t *testing.T) {
	source, version, ok, err := pinnedSchemaFields(map[string]any{
		"schema_source": "openapi",
		"schema_url":    "https://example.invalid/spec.json",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for a live-shaped entry, got source=%q version=%q", source, version)
	}
}

func TestPinnedSchemaFields_SourceAndVersion_Pinned(t *testing.T) {
	source, version, ok, err := pinnedSchemaFields(map[string]any{
		"source":  "ubiquex/aws",
		"version": "1.2.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if source != "ubiquex/aws" || version != "1.2.0" {
		t.Fatalf("source=%q version=%q, want ubiquex/aws / 1.2.0", source, version)
	}
}

func TestPinnedSchemaFields_SourceWithoutVersion_Errors(t *testing.T) {
	_, _, _, err := pinnedSchemaFields(map[string]any{"source": "ubiquex/aws"})
	if err == nil {
		t.Fatal("expected an error for source without version")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Fatalf("error %v doesn't mention the missing version", err)
	}
}

func TestPinnedSchemaFields_NonStringSource_Errors(t *testing.T) {
	_, _, _, err := pinnedSchemaFields(map[string]any{"source": 42, "version": "1.0.0"})
	if err == nil {
		t.Fatal("expected an error for a non-string source")
	}
}

func TestPinnedSchemaFields_NonStringVersion_Errors(t *testing.T) {
	_, _, _, err := pinnedSchemaFields(map[string]any{"source": "ubiquex/aws", "version": 1})
	if err == nil {
		t.Fatal("expected an error for a non-string version")
	}
}

// UBI-199: schemaProvenanceFields is what makes an unpinned fetch
// visible in PROVENANCE.json rather than merely disallowed by a
// separate flag -- these mirror TestPinnedSchemaFields_* above exactly,
// since it's a thin wrapper, but assert the RECORD shape a downstream
// consumer (ubiquex-docs' own provenance_check.py) actually reads.
func TestSchemaProvenanceFields_Live_RecordsURL(t *testing.T) {
	pinned, source, version, url, err := schemaProvenanceFields(map[string]any{
		"schema_source": "openapi",
		"schema_url":    "https://example.invalid/spec.json",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pinned {
		t.Fatal("expected pinned=false for a live-shaped entry")
	}
	if source != "" || version != "" {
		t.Fatalf("expected empty source/version for a live entry, got source=%q version=%q", source, version)
	}
	if url != "https://example.invalid/spec.json" {
		t.Fatalf("url=%q, want the real schema_url recorded so a live fetch is visible", url)
	}
}

func TestSchemaProvenanceFields_Pinned_RecordsSourceAndVersion(t *testing.T) {
	pinned, source, version, url, err := schemaProvenanceFields(map[string]any{
		"source":  "ubiquex/azure",
		"version": "1.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pinned {
		t.Fatal("expected pinned=true")
	}
	if source != "ubiquex/azure" || version != "1.0.0" {
		t.Fatalf("source=%q version=%q, want ubiquex/azure / 1.0.0", source, version)
	}
	if url != "" {
		t.Fatalf("expected empty url for a pinned entry, got %q", url)
	}
}

func TestSchemaProvenanceFields_PropagatesPinnedSchemaFieldsError(t *testing.T) {
	_, _, _, _, err := schemaProvenanceFields(map[string]any{"source": "ubiquex/azure"})
	if err == nil {
		t.Fatal("expected the underlying pinnedSchemaFields error (source without version) to propagate")
	}
}

func TestGroupSchemaProvenanceFields_AllMembersPinnedAndAgree_Pinned(t *testing.T) {
	dp := map[string]map[string]any{
		"azure":         {"source": "ubiquex/azure", "version": "1.0.0"},
		"azure_advisor": {"source": "ubiquex/azure", "version": "1.0.0"},
	}
	pinned, source, version, note, err := groupSchemaProvenanceFields([]string{"azure", "azure_advisor"}, dp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pinned {
		t.Fatalf("expected pinned=true when every member agrees, note=%q", note)
	}
	if source != "ubiquex/azure" || version != "1.0.0" {
		t.Fatalf("source=%q version=%q, want the real, shared pin", source, version)
	}
	if note != "" {
		t.Fatalf("expected no note for a genuinely coherent group, got %q", note)
	}
}

func TestGroupSchemaProvenanceFields_OneMemberLive_NotPinned(t *testing.T) {
	dp := map[string]map[string]any{
		"aws":             {"source": "ubiquex/aws", "version": "1.0.0"},
		"aws_data_zzznew": {"schema_source": "smithy", "schema_url": "https://example.invalid/new-service.json"},
	}
	pinned, _, _, note, err := groupSchemaProvenanceFields([]string{"aws", "aws_data_zzznew"}, dp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pinned {
		t.Fatal("expected pinned=false when even one real member is still live -- a group is only as pinned as its least-pinned member")
	}
	if note == "" {
		t.Fatal("expected a real note explaining why the group doesn't read as pinned")
	}
}

func TestGroupSchemaProvenanceFields_MembersDisagreeOnVersion_NotPinned(t *testing.T) {
	dp := map[string]map[string]any{
		"azure":         {"source": "ubiquex/azure", "version": "1.0.0"},
		"azure_advisor": {"source": "ubiquex/azure", "version": "0.9.0"},
	}
	pinned, _, _, note, err := groupSchemaProvenanceFields([]string{"azure", "azure_advisor"}, dp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pinned {
		t.Fatal("expected pinned=false when members disagree on version -- not one coherent, reproducible fetch")
	}
	if note == "" {
		t.Fatal("expected a real note explaining the disagreement")
	}
}

func TestGroupSchemaProvenanceFields_PropagatesMemberError(t *testing.T) {
	dp := map[string]map[string]any{
		"azure": {"source": "ubiquex/azure"},
	}
	_, _, _, _, err := groupSchemaProvenanceFields([]string{"azure"}, dp)
	if err == nil {
		t.Fatal("expected the underlying member error (source without version) to propagate")
	}
}

// TestDynamicProviderEnv_LiveMode_WritesConfig confirms the existing,
// pre-pinning behavior is completely unchanged: a schema_url-shaped entry
// still gets a real, temporary [dynamic_providers.<name>] config file and
// just the one, original env var.
func TestDynamicProviderEnv_LiveMode_WritesConfig(t *testing.T) {
	workDir := t.TempDir()
	params := map[string]any{
		"schema_source": "openapi",
		"schema_url":    "https://example.invalid/spec.json",
	}

	env, err := dynamicProviderEnv(context.Background(), workDir, "widget", params)
	if err != nil {
		t.Fatalf("dynamicProviderEnv: %v", err)
	}
	if len(env) != 1 || env[0] != "UBX_DYNAMIC_PROVIDER_NAME=widget" {
		t.Fatalf("env = %v, want exactly [UBX_DYNAMIC_PROVIDER_NAME=widget]", env)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".ubx", "config")); err != nil {
		t.Fatalf("expected a real .ubx/config to be written in live mode: %v", err)
	}
}

// TestDynamicProviderEnv_PinnedMode_UsesSchemaMirrorAndSkipsConfig proves
// the real, new pinned path end to end against provider.AcquireSchema's
// own already-tested UBX_SCHEMA_MIRROR short-circuit (provider/acquireschema.go)
// -- real production code, no seam/mock substituted, just a local mirror
// directory so no network is needed. Confirms UBX_SNAPSHOT_PATH is set to
// the real mirrored group DIRECTORY (UBI-182's own manifest.json +
// members/<name>.json shape, not a single file) and that workDir gets no
// .ubx/config at all, per runServeSnapshot's own doc comment: a pinned
// launch reads UBX_SNAPSHOT_PATH directly and never even looks for
// .ubx/config.
func TestDynamicProviderEnv_PinnedMode_UsesSchemaMirrorAndSkipsConfig(t *testing.T) {
	mirrorRoot := t.TempDir()
	snapDir := filepath.Join(mirrorRoot, "ubiquex", "aws", "1.2.0")
	if err := os.MkdirAll(filepath.Join(snapDir, "members"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schema_format":3,"provider":"aws","version":"1.2.0","members":["aws"]}`)
	member := []byte(`{"schema_source":"cloudformation","mode":"resource","raw_spec":{}}`)
	if err := os.WriteFile(filepath.Join(snapDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "members", "aws.json"), member, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UBX_SCHEMA_MIRROR", mirrorRoot)

	workDir := t.TempDir()
	params := map[string]any{"source": "ubiquex/aws", "version": "1.2.0"}

	env, err := dynamicProviderEnv(context.Background(), workDir, "aws", params)
	if err != nil {
		t.Fatalf("dynamicProviderEnv: %v", err)
	}

	var gotName, gotSnapshotPath string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "UBX_DYNAMIC_PROVIDER_NAME="); ok {
			gotName = v
		}
		if v, ok := strings.CutPrefix(kv, "UBX_SNAPSHOT_PATH="); ok {
			gotSnapshotPath = v
		}
	}
	if gotName != "aws" {
		t.Errorf("UBX_DYNAMIC_PROVIDER_NAME = %q, want aws", gotName)
	}
	if gotSnapshotPath == "" {
		t.Fatal("UBX_SNAPSHOT_PATH not set in pinned mode")
	}
	if gotSnapshotPath != snapDir {
		t.Fatalf("UBX_SNAPSHOT_PATH = %q, want the real mirrored group directory %q", gotSnapshotPath, snapDir)
	}
	got, err := os.ReadFile(filepath.Join(gotSnapshotPath, "manifest.json"))
	if err != nil {
		t.Fatalf("read resolved manifest: %v", err)
	}
	if string(got) != string(manifest) {
		t.Fatalf("resolved manifest content mismatch: got %s, want %s", got, manifest)
	}

	if _, err := os.Stat(filepath.Join(workDir, ".ubx", "config")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".ubx/config: got err=%v, want os.ErrNotExist -- pinned mode must not write one", err)
	}
}

func TestDynamicProviderEnv_PinnedSourceInvalid_Errors(t *testing.T) {
	workDir := t.TempDir()
	_, err := dynamicProviderEnv(context.Background(), workDir, "widget", map[string]any{
		"source":  "not-a-valid-source-with-too/many/many/slashes",
		"version": "1.0.0",
	})
	if err == nil {
		t.Fatal("expected an error for an unparseable source address")
	}
}

// setUpPinnedSchemaMirror puts a real, minimal group snapshot at
// <root>/ubiquex/widget/1.0.0/ (UBI-182's own manifest.json +
// members/<name>.json shape) and points UBX_SCHEMA_MIRROR at it -- the
// same real, already-proven provider.AcquireSchema short-circuit
// TestDynamicProviderEnv_PinnedMode_UsesSchemaMirrorAndSkipsConfig
// already uses, reused here so dynamicProviderSignals/dynamicProviderNamespaces
// resolve a real pinned entry with zero network.
func setUpPinnedSchemaMirror(t *testing.T) {
	t.Helper()
	mirrorRoot := t.TempDir()
	snapDir := filepath.Join(mirrorRoot, "ubiquex", "widget", "1.0.0")
	if err := os.MkdirAll(filepath.Join(snapDir, "members"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schema_format":3,"provider":"widget","version":"1.0.0","members":["widget"]}`)
	member := []byte(`{"schema_source":"openapi","mode":"resource","raw_spec":{}}`)
	if err := os.WriteFile(filepath.Join(snapDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "members", "widget.json"), member, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UBX_SCHEMA_MIRROR", mirrorRoot)
}

// writeFakeDumpBinary writes a real, tiny, executable shell script standing
// in for ubx-provider-dynamic's own --dump-signals/--dump-namespaces
// behavior -- it reports back (as real JSON output, decoded by the
// caller under test) whether it actually saw UBX_SNAPSHOT_PATH set and no
// .ubx/config in its own working directory, the exact real contract a
// pinned launch is supposed to honor (dynamicProviderEnv's own doc
// comment). Real subprocess exec, not a mocked Go call -- proves
// dynamicProviderSignals/dynamicProviderNamespaces actually reach a
// pinned launch now, instead of asserting on dynamicProviderEnv alone.
func writeFakeDumpBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-ubx-provider-dynamic")
	script := `#!/bin/sh
seen="no"
if [ -n "$UBX_SNAPSHOT_PATH" ] && [ ! -e .ubx/config ]; then
  seen="yes"
fi
case "$1" in
  --dump-signals)
    echo "{\"pinned_snapshot_path_seen\": {\"$seen\": {\"pattern\": \"ok\"}}}"
    ;;
  --dump-namespaces)
    echo "{\"pinned_snapshot_path_seen\": \"$seen\"}"
    ;;
  *)
    exit 1
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDynamicProviderSignals_PinnedMode_NoLongerRefused is UBI-182 Stage
// C's own real regression guard: a pinned entry (source+version) used to
// return a hard, immediate error here ("--dump-signals is not yet wired
// to a pinned schema source") before ever launching anything. It must now
// resolve the real snapshot via the mirror, launch the subprocess with
// UBX_SNAPSHOT_PATH set and no .ubx/config, and return real, decoded
// signal output.
func TestDynamicProviderSignals_PinnedMode_NoLongerRefused(t *testing.T) {
	setUpPinnedSchemaMirror(t)
	binPath := writeFakeDumpBinary(t)

	out, err := dynamicProviderSignals(context.Background(), binPath, "widget", map[string]any{
		"source":  "ubiquex/widget",
		"version": "1.0.0",
	})
	if err != nil {
		t.Fatalf("dynamicProviderSignals: %v", err)
	}
	if _, ok := out["pinned_snapshot_path_seen"]["yes"]; !ok {
		t.Fatalf("subprocess did not see a real UBX_SNAPSHOT_PATH with no .ubx/config: %v", out)
	}
}

// TestDynamicProviderNamespaces_PinnedMode_NoLongerRefused is
// TestDynamicProviderSignals_PinnedMode_NoLongerRefused's own identical
// sibling for --dump-namespaces.
func TestDynamicProviderNamespaces_PinnedMode_NoLongerRefused(t *testing.T) {
	setUpPinnedSchemaMirror(t)
	binPath := writeFakeDumpBinary(t)

	out, err := dynamicProviderNamespaces(context.Background(), binPath, "widget", map[string]any{
		"source":  "ubiquex/widget",
		"version": "1.0.0",
	})
	if err != nil {
		t.Fatalf("dynamicProviderNamespaces: %v", err)
	}
	if out["pinned_snapshot_path_seen"] != "yes" {
		t.Fatalf("subprocess did not see a real UBX_SNAPSHOT_PATH with no .ubx/config: %v", out)
	}
}

// setUpPinnedBinaryMirror puts a real, minimal group snapshot (WITH a
// real min_binary_version this time, unlike setUpPinnedSchemaMirror) at
// <schemaRoot>/ubiquex/widget/1.0.0/, points UBX_SCHEMA_MIRROR at it,
// puts a real, fake ubx-provider-dynamic "binary" at
// <binRoot>/9.9.9/<goos>_<goarch>/, and points UBX_DYNAMIC_PROVIDER_MIRROR
// at it -- the real, full, hermetic fixture acquirePinnedSchemaAndBinary
// needs end to end (UBI-194), no network, no real subprocess build.
func setUpPinnedBinaryMirror(t *testing.T) {
	t.Helper()
	schemaRoot := t.TempDir()
	snapDir := filepath.Join(schemaRoot, "ubiquex", "widget", "1.0.0")
	if err := os.MkdirAll(filepath.Join(snapDir, "members"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schema_format":3,"provider":"widget","version":"1.0.0","members":["widget"],"min_binary_version":"9.9.9"}`)
	member := []byte(`{"schema_source":"openapi","mode":"resource","raw_spec":{}}`)
	if err := os.WriteFile(filepath.Join(snapDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "members", "widget.json"), member, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UBX_SCHEMA_MIRROR", schemaRoot)

	binRoot := t.TempDir()
	binPlatformDir := filepath.Join(binRoot, "9.9.9", runtime.GOOS+"_"+runtime.GOARCH)
	if err := os.MkdirAll(binPlatformDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binPlatformDir, "ubx-provider-dynamic"), []byte("fake binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UBX_DYNAMIC_PROVIDER_MIRROR", binRoot)
}

// TestAcquirePinnedSchemaAndBinary_PinnedMode_Succeeds is
// TestDynamicProviderEnv_PinnedMode_UsesSchemaMirrorAndSkipsConfig's own
// real sibling for acquirePinnedSchemaAndBinary -- [providers.<name>]'s
// own, now-sole real shape, post-collapse (UBI-182 Stage E), now also
// resolving the real binary the snapshot itself requires (UBI-194).
// Proves both resolve correctly against real mirror short-circuits, no
// network, no real subprocess build.
func TestAcquirePinnedSchemaAndBinary_PinnedMode_Succeeds(t *testing.T) {
	setUpPinnedBinaryMirror(t)

	binPath, env, err := acquirePinnedSchemaAndBinary(context.Background(), "widget", map[string]any{
		"source":  "ubiquex/widget",
		"version": "1.0.0",
	})
	if err != nil {
		t.Fatalf("acquirePinnedSchemaAndBinary: %v", err)
	}
	if binPath == "" {
		t.Fatal("binPath not resolved")
	}
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read resolved binary: %v", err)
	}
	if string(got) != "fake binary" {
		t.Fatalf("resolved binary content = %q, want the real mirrored fake binary", got)
	}
	var gotName, gotSnapshotPath string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "UBX_DYNAMIC_PROVIDER_NAME="); ok {
			gotName = v
		}
		if v, ok := strings.CutPrefix(kv, "UBX_SNAPSHOT_PATH="); ok {
			gotSnapshotPath = v
		}
	}
	if gotName != "widget" {
		t.Errorf("UBX_DYNAMIC_PROVIDER_NAME = %q, want widget", gotName)
	}
	if gotSnapshotPath == "" {
		t.Fatal("UBX_SNAPSHOT_PATH not set")
	}
}

// TestAcquirePinnedSchemaAndBinary_RepoOverride_BypassesRealAcquisition
// is UBI-194's own real proof that UBX_PROVIDER_DYNAMIC_REPO still
// works as an explicit, real development override on the pinned path --
// checked BEFORE the real acquire-by-manifest-version mechanism, not
// instead of it structurally removed.
func TestAcquirePinnedSchemaAndBinary_RepoOverride_BypassesRealAcquisition(t *testing.T) {
	setUpPinnedSchemaMirror(t)
	t.Setenv("UBX_PROVIDER_DYNAMIC_REPO", "/this/checkout/does/not/exist")

	_, _, err := acquirePinnedSchemaAndBinary(context.Background(), "widget", map[string]any{
		"source":  "ubiquex/widget",
		"version": "1.0.0",
	})
	if err == nil {
		t.Fatal("expected a real error -- the override checkout path doesn't exist")
	}
	if !strings.Contains(err.Error(), "checkout") {
		t.Fatalf("error %v doesn't look like it came from the real UBX_PROVIDER_DYNAMIC_REPO override path (expected a real 'no checkout found' error, proving the override was actually taken, not the real acquire-by-manifest-version path)", err)
	}
}

// TestAcquirePinnedSchemaAndBinary_LiveShapedParams_FailsLoud is UBI-182
// Stage E's own real, direct proof: a [providers.<name>] entry shaped
// like a live-fetch [dynamic_providers.<name>] one (schema_source/
// schema_url, no source/version) used to silently fall through to a
// real live fetch under the OLD dual-meaning dynamicProviderEnv. It must
// now fail loud, immediately, with a real, named error pointing at
// [dynamic_providers.<name>] as the correct table -- never attempt
// writeDynamicProviderConfig, any live fetch, or any real acquisition
// at all.
func TestAcquirePinnedSchemaAndBinary_LiveShapedParams_FailsLoud(t *testing.T) {
	_, _, err := acquirePinnedSchemaAndBinary(context.Background(), "widget", map[string]any{
		"schema_source": "openapi",
		"schema_url":    "https://example.invalid/spec.json",
	})
	if err == nil {
		t.Fatal("expected a real error for a live-shaped [providers.<name>] entry -- the dual meaning is supposed to be gone")
	}
	if !strings.Contains(err.Error(), "must be pinned") || !strings.Contains(err.Error(), "dynamic_providers") {
		t.Fatalf("error %v doesn't explain the real collapse (must name both the pinned requirement and dynamic_providers as the live-fetch alternative)", err)
	}
}

// TestAcquirePinnedSchemaAndBinary_MissingVersion_Errors proves
// pinnedSchemaFields' own existing "source without version" error still
// propagates correctly through the new, fuller function.
func TestAcquirePinnedSchemaAndBinary_MissingVersion_Errors(t *testing.T) {
	_, _, err := acquirePinnedSchemaAndBinary(context.Background(), "widget", map[string]any{
		"source": "ubiquex/widget",
	})
	if err == nil {
		t.Fatal("expected an error for source without version")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Fatalf("error %v doesn't mention the missing version", err)
	}
}
