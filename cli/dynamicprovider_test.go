package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
