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
// the real mirrored file and that workDir gets no .ubx/config at all, per
// runServeSnapshot's own doc comment: a pinned launch reads
// UBX_SNAPSHOT_PATH directly and never even looks for .ubx/config.
func TestDynamicProviderEnv_PinnedMode_UsesSchemaMirrorAndSkipsConfig(t *testing.T) {
	mirrorRoot := t.TempDir()
	snapDir := filepath.Join(mirrorRoot, "ubiquex", "aws", "1.2.0")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"schema_format":1,"provider":"aws","version":"1.2.0"}`)
	if err := os.WriteFile(filepath.Join(snapDir, "snapshot.json"), content, 0o644); err != nil {
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
	got, err := os.ReadFile(gotSnapshotPath)
	if err != nil {
		t.Fatalf("read resolved snapshot path: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("resolved snapshot content mismatch: got %s, want %s", got, content)
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
