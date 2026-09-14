package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

// TestResolveDynamicProviderBinaryVersion_LegacyNameStillResolves is the
// regression guard for UBI-249's rename of min_binary_version to
// generated_by_binary_version. All eight already-published
// ubx-schema-<provider> snapshots carry only the legacy key, so if this
// read were dropped every one of them would resolve as absent and fall
// through to the bootstrap table, silently downgrading cloudflare's real
// 1.0.10 to 1.0.0 and logging a "published before UBI-194" line that is
// false for them.
func TestResolveDynamicProviderBinaryVersion_LegacyNameStillResolves(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{"schema_format":3,"provider":"cloudflare","version":"1.0.2","min_binary_version":"1.0.10"}`)

	var got string
	var err error
	stderr := captureStderr(t, func() {
		got, err = ResolveDynamicProviderBinaryVersion(dir)
	})
	if err != nil {
		t.Fatalf("ResolveDynamicProviderBinaryVersion: %v", err)
	}
	if got != "1.0.10" {
		t.Fatalf("version = %q, want 1.0.10 read from the pre-rename min_binary_version key", got)
	}
	if stderr != "" {
		t.Fatalf("expected no bootstrap-fallback log line for a legacy-named but present value, got: %s", stderr)
	}
}

// TestResolveDynamicProviderBinaryVersion_NewNameWinsOverLegacy proves
// precedence runs the way the rename intends: a snapshot re-cut by a
// post-rename binary carries both keys, and the new one is authoritative.
func TestResolveDynamicProviderBinaryVersion_NewNameWinsOverLegacy(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{"schema_format":3,"provider":"azure","version":"2.0.0","generated_by_binary_version":"1.0.13","min_binary_version":"1.0.10"}`)

	var got string
	var err error
	captureStderr(t, func() {
		got, err = ResolveDynamicProviderBinaryVersion(dir)
	})
	if err != nil {
		t.Fatalf("ResolveDynamicProviderBinaryVersion: %v", err)
	}
	if got != "1.0.13" {
		t.Fatalf("version = %q, want 1.0.13 (new name must win over the legacy mirror)", got)
	}
}

// TestNewestCompatibleVersion covers the selection rule without a
// network. The cases are the real ones: every published schema snapshot's
// own stamped floor at the time this was written, against the real
// release list.
func TestNewestCompatibleVersion(t *testing.T) {
	// The real ubx-provider-dynamic release history, newest first, as
	// GitHub returns it.
	rels := []githubRelease{
		{TagName: "v1.3.1"}, {TagName: "v1.3.0"}, {TagName: "v1.2.1"},
		{TagName: "v1.2.0"}, {TagName: "v1.1.0"}, {TagName: "v1.0.13"},
		{TagName: "v1.0.10"}, {TagName: "v1.0.4"},
	}

	for _, tc := range []struct {
		name  string
		floor string
		want  string
	}{
		// The incident: an encoder fix released as 1.3.1 could not reach
		// a snapshot stamped 1.3.0.
		{"aws", "1.3.0", "1.3.1"},
		// The five stamped a full minor back. A major.minor bound would
		// have left every one of these stuck, since no 1.2.x newer than
		// 1.2.1 exists and 1.2.1 predates the fix.
		{"azure", "1.2.0", "1.3.1"},
		{"datadog", "1.2.0", "1.3.1"},
		{"github", "1.2.0", "1.3.1"},
		{"google", "1.2.0", "1.3.1"},
		{"kubernetes", "1.2.0", "1.3.1"},
		// Sixteen releases behind, and still served by the same major.
		{"cloudflare", "1.0.10", "1.3.1"},
		{"digitalocean", "1.0.4", "1.3.1"},
		// Already newest: no change, and never a downgrade.
		{"already newest", "1.3.1", "1.3.1"},
		// A floor ahead of everything published (a snapshot cut from an
		// unreleased build) keeps itself rather than silently downgrading.
		{"floor ahead of the list", "1.9.0", "1.9.0"},
		// Unparseable floors are returned untouched: guessing is worse
		// than the status quo.
		{"not semver", "dev", "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := newestCompatibleVersion(tc.floor, rels); got != tc.want {
				t.Fatalf("newestCompatibleVersion(%q) = %q, want %q", tc.floor, got, tc.want)
			}
		})
	}
}

// TestNewestCompatibleVersion_NeverCrossesAMajor is the bound itself.
// schema_format is the real compatibility contract and a major bump is
// where this resolution stops guessing.
func TestNewestCompatibleVersion_NeverCrossesAMajor(t *testing.T) {
	rels := []githubRelease{{TagName: "v2.0.0"}, {TagName: "v1.3.1"}, {TagName: "v1.3.0"}}
	if got := newestCompatibleVersion("1.3.0", rels); got != "1.3.1" {
		t.Fatalf("got %q, want 1.3.1 -- a 2.x release must never serve a 1.x snapshot", got)
	}
}

// TestNewestCompatibleVersion_SkipsPreReleases: a snapshot stamped 1.3.0
// must not silently start running a release candidate. Same exclusion
// provider/versions.go already applies when picking a registry provider.
func TestNewestCompatibleVersion_SkipsPreReleases(t *testing.T) {
	rels := []githubRelease{{TagName: "v1.4.0-rc1"}, {TagName: "v1.3.1"}}
	if got := newestCompatibleVersion("1.3.0", rels); got != "1.3.1" {
		t.Fatalf("got %q, want 1.3.1 -- a pre-release is not something to be upgraded into silently", got)
	}
}

// TestResolveNewestCompatible_ServerAndFallbacks covers the half that
// talks to GitHub: that it uses what it finds, and that every way of
// failing to find anything lands back on the floor rather than on an
// error.
//
// Degrading silently is deliberate. Acquiring the stamped version is
// exactly what happened before this resolution existed and is known to
// work, so being offline, rate-limited, or pointed at a GitHub outage
// must never turn a working run into a failing one.
func TestResolveNewestCompatible_ServerAndFallbacks(t *testing.T) {
	t.Run("uses the newest release sharing the major", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.URL.Path, "/releases") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `[{"tag_name":"v1.3.1"},{"tag_name":"v1.3.0"}]`)
		}))
		defer srv.Close()

		got := ResolveNewestCompatibleDynamicProviderBinary(context.Background(), "1.3.0",
			WithDynamicProviderBinaryAPIBase(srv.URL))
		if got != "1.3.1" {
			t.Fatalf("got %q, want 1.3.1", got)
		}
	})

	t.Run("server error degrades to the floor", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden) // a rate limit looks like this
		}))
		defer srv.Close()

		got := ResolveNewestCompatibleDynamicProviderBinary(context.Background(), "1.3.0",
			WithDynamicProviderBinaryAPIBase(srv.URL))
		if got != "1.3.0" {
			t.Fatalf("got %q, want the floor 1.3.0 -- a failure to find something better must not become a failure to run", got)
		}
	})

	t.Run("unreachable host degrades to the floor", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		base := srv.URL
		srv.Close() // nothing is listening now: the offline case

		got := ResolveNewestCompatibleDynamicProviderBinary(context.Background(), "1.3.0",
			WithDynamicProviderBinaryAPIBase(base))
		if got != "1.3.0" {
			t.Fatalf("got %q, want the floor 1.3.0 when offline", got)
		}
	})

	t.Run("garbage body degrades to the floor", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `not json`)
		}))
		defer srv.Close()

		got := ResolveNewestCompatibleDynamicProviderBinary(context.Background(), "1.3.0",
			WithDynamicProviderBinaryAPIBase(srv.URL))
		if got != "1.3.0" {
			t.Fatalf("got %q, want the floor 1.3.0", got)
		}
	})

	t.Run("the exact escape hatch never asks at all", func(t *testing.T) {
		asked := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked = true
			_, _ = io.WriteString(w, `[{"tag_name":"v1.3.1"}]`)
		}))
		defer srv.Close()

		t.Setenv(exactDynamicProviderBinaryEnv, "1")
		got := ResolveNewestCompatibleDynamicProviderBinary(context.Background(), "1.3.0",
			WithDynamicProviderBinaryAPIBase(srv.URL))
		if got != "1.3.0" {
			t.Fatalf("got %q, want the stamped 1.3.0 exactly", got)
		}
		if asked {
			t.Fatal("the exact pin made a network request, so it is not usable offline for a byte-identical rebuild")
		}
	})
}
