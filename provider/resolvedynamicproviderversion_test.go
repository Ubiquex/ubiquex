package provider

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, manifestFilename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// captureStderr redirects os.Stderr for the duration of fn, returning
// everything written to it -- the real, direct proof the bootstrap
// fallback's own log line actually fires, not just that resolution
// succeeds.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestResolveDynamicProviderBinaryVersion_RealFieldPresent_NoFallback(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{"schema_format":3,"provider":"azure","version":"1.0.0","min_binary_version":"1.4.0"}`)

	var got string
	var err error
	stderr := captureStderr(t, func() {
		got, err = ResolveDynamicProviderBinaryVersion(dir)
	})
	if err != nil {
		t.Fatalf("ResolveDynamicProviderBinaryVersion: %v", err)
	}
	if got != "1.4.0" {
		t.Fatalf("version = %q, want the real, exact min_binary_version 1.4.0", got)
	}
	if stderr != "" {
		t.Fatalf("expected no real fallback log line when min_binary_version is present, got: %s", stderr)
	}
}

func TestResolveDynamicProviderBinaryVersion_AbsentField_LogsAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{"schema_format":3,"provider":"kubernetes","version":"3.0.0"}`)

	var got string
	var err error
	stderr := captureStderr(t, func() {
		got, err = ResolveDynamicProviderBinaryVersion(dir)
	})
	if err != nil {
		t.Fatalf("ResolveDynamicProviderBinaryVersion: %v", err)
	}
	want := dynamicProviderBinaryBootstrapVersions[3]
	if got != want {
		t.Fatalf("version = %q, want the real bootstrap fallback %q", got, want)
	}
	if !strings.Contains(stderr, "kubernetes") || !strings.Contains(stderr, "3.0.0") {
		t.Fatalf("real fallback log line doesn't name the real provider and version, got: %s", stderr)
	}
	if !strings.Contains(stderr, want) {
		t.Fatalf("real fallback log line doesn't name the real fallback version %q, got: %s", want, stderr)
	}
}

func TestResolveDynamicProviderBinaryVersion_AbsentField_UnregisteredSchemaFormat_Errors(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{"schema_format":99,"provider":"widget","version":"1.0.0"}`)

	_, err := ResolveDynamicProviderBinaryVersion(dir)
	if err == nil {
		t.Fatal("expected a real error for an absent min_binary_version with no registered bootstrap fallback")
	}
}
