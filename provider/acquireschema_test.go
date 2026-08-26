package provider

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// testSchemaSource returns a real, deterministic SchemaSource fixture.
func testSchemaSource() SchemaSource {
	return SchemaSource{Hostname: "github.com", Namespace: "acme", Type: "widget"}
}

// buildTarGz builds a real gzip-compressed tar archive from files (path ->
// content), mirroring the real shape a schema repo's own publish.yml
// produces (manifest.json plus members/<name>.json, packed at release
// time) -- used both to build valid fixtures and, in the unsafe-entry
// test, a real, deliberately malicious one.
func buildTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar.WriteHeader(%s): %v", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip.Close: %v", err)
	}
	return buf.Bytes()
}

// realManifestFixture is a real, minimal, valid manifest.json + one
// member -- the smallest real archive AcquireSchema is expected to
// extract successfully.
func realManifestFixture() map[string][]byte {
	return map[string][]byte{
		"manifest.json":       []byte(`{"schema_format":3,"provider":"widget","version":"1.0.0","members":["widget"]}`),
		"members/widget.json": []byte(`{"schema_source":"openapi","mode":"resource","raw_spec":{}}`),
	}
}

// testSchemaRelease is a fully scriptable fake GitHub Releases server,
// mirroring testRegistry's own shape one level up: swap in archive/sums
// bytes or an explicit HTTP status to corrupt exactly one part of the
// story per test.
type testSchemaRelease struct {
	notFound bool
	archive  []byte
	sums     []byte
	// omitSums/omitArchive simulate a malformed release missing one of
	// its two required assets.
	omitSums    bool
	omitArchive bool
}

func (r *testSchemaRelease) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/repos/acme/ubx-schema-widget/releases/tags/v1.0.0", func(w http.ResponseWriter, req *http.Request) {
		if r.notFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rel := githubRelease{TagName: "v1.0.0"}
		if !r.omitArchive {
			rel.Assets = append(rel.Assets, githubAsset{Name: archiveFilename, BrowserDownloadURL: srv.URL + "/dl/" + archiveFilename})
		}
		if !r.omitSums {
			rel.Assets = append(rel.Assets, githubAsset{Name: checksumsFilename, BrowserDownloadURL: srv.URL + "/dl/" + checksumsFilename})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rel); err != nil {
			t.Errorf("encode JSON response: %v", err)
		}
	})
	mux.HandleFunc("/dl/"+archiveFilename, func(w http.ResponseWriter, req *http.Request) {
		w.Write(r.archive)
	})
	mux.HandleFunc("/dl/"+checksumsFilename, func(w http.ResponseWriter, req *http.Request) {
		w.Write(r.sums)
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAcquireSchema_HappyPath(t *testing.T) {
	archive := buildTarGz(t, realManifestFixture())
	sums := shasumsLine(sha256HexOf(archive), archiveFilename)
	rel := &testSchemaRelease{archive: archive, sums: sums}
	srv := rel.start(t)

	cacheRoot := t.TempDir()
	result, err := AcquireSchema(context.Background(), testSchemaSource(), "1.0.0",
		WithSchemaHTTPClient(srv.Client()), WithSchemaAPIBase(srv.URL), WithSchemaCacheRoot(cacheRoot))
	if err != nil {
		t.Fatalf("AcquireSchema: %v", err)
	}
	if result.FromMirror || result.FromCache {
		t.Fatalf("expected a fresh network acquisition, got %+v", result)
	}
	if result.SHA256 != sha256HexOf(archive) {
		t.Fatalf("SHA256 = %s, want %s", result.SHA256, sha256HexOf(archive))
	}
	manifest, err := os.ReadFile(filepath.Join(result.Path, manifestFilename))
	if err != nil {
		t.Fatalf("read extracted manifest: %v", err)
	}
	if !bytes.Equal(manifest, realManifestFixture()["manifest.json"]) {
		t.Fatal("extracted manifest content mismatch")
	}
	member, err := os.ReadFile(filepath.Join(result.Path, "members", "widget.json"))
	if err != nil {
		t.Fatalf("read extracted member: %v", err)
	}
	if !bytes.Equal(member, realManifestFixture()["members/widget.json"]) {
		t.Fatal("extracted member content mismatch")
	}
}

func TestAcquireSchema_SecondCallHitsCache(t *testing.T) {
	archive := buildTarGz(t, realManifestFixture())
	sums := shasumsLine(sha256HexOf(archive), archiveFilename)
	rel := &testSchemaRelease{archive: archive, sums: sums}
	srv := rel.start(t)
	cacheRoot := t.TempDir()

	first, err := AcquireSchema(context.Background(), testSchemaSource(), "1.0.0",
		WithSchemaHTTPClient(srv.Client()), WithSchemaAPIBase(srv.URL), WithSchemaCacheRoot(cacheRoot))
	if err != nil {
		t.Fatalf("first AcquireSchema: %v", err)
	}

	srv.Close() // second call must not touch the network at all
	second, err := AcquireSchema(context.Background(), testSchemaSource(), "1.0.0",
		WithSchemaCacheRoot(cacheRoot))
	if err != nil {
		t.Fatalf("second AcquireSchema (should be a pure cache hit): %v", err)
	}
	if !second.FromCache {
		t.Fatalf("expected FromCache, got %+v", second)
	}
	if second.Path != first.Path {
		t.Fatalf("cache hit diverged from original acquisition: %+v vs %+v", second, first)
	}
}

func TestAcquireSchema_MirrorHitSkipsNetworkAndVerification(t *testing.T) {
	mirrorRoot := t.TempDir()
	src := testSchemaSource()
	dir := filepath.Join(mirrorRoot, src.Namespace, src.Type, "1.0.0")
	if err := os.MkdirAll(filepath.Join(dir, "members"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range realManifestFixture() {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv(schemaMirrorEnv, mirrorRoot)

	result, err := AcquireSchema(context.Background(), src, "1.0.0",
		WithSchemaHTTPClient(http.DefaultClient)) // no server configured -- a network call would fail loudly
	if err != nil {
		t.Fatalf("AcquireSchema: %v", err)
	}
	if !result.FromMirror {
		t.Fatalf("expected FromMirror, got %+v", result)
	}
	if result.Path != dir {
		t.Fatalf("Path = %s, want the real mirror dir %s", result.Path, dir)
	}
}

func TestAcquireSchema_ChecksumMismatch(t *testing.T) {
	archive := buildTarGz(t, realManifestFixture())
	sums := shasumsLine(sha256HexOf([]byte("not the real archive bytes")), archiveFilename)
	rel := &testSchemaRelease{archive: archive, sums: sums}
	srv := rel.start(t)

	_, err := AcquireSchema(context.Background(), testSchemaSource(), "1.0.0",
		WithSchemaHTTPClient(srv.Client()), WithSchemaAPIBase(srv.URL), WithSchemaCacheRoot(t.TempDir()))
	if !errors.Is(err, ErrSchemaChecksumMismatch) {
		t.Fatalf("err = %v, want ErrSchemaChecksumMismatch", err)
	}
}

func TestAcquireSchema_ReleaseNotFound(t *testing.T) {
	rel := &testSchemaRelease{notFound: true}
	srv := rel.start(t)

	_, err := AcquireSchema(context.Background(), testSchemaSource(), "1.0.0",
		WithSchemaHTTPClient(srv.Client()), WithSchemaAPIBase(srv.URL), WithSchemaCacheRoot(t.TempDir()))
	if !errors.Is(err, ErrSchemaNotFound) {
		t.Fatalf("err = %v, want ErrSchemaNotFound", err)
	}
}

func TestAcquireSchema_MissingAsset(t *testing.T) {
	for _, tc := range []struct {
		name string
		rel  *testSchemaRelease
	}{
		{"no snapshot.tar.gz", &testSchemaRelease{omitArchive: true, sums: []byte("x")}},
		{"no SHA256SUMS", &testSchemaRelease{omitSums: true, archive: []byte("x")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := tc.rel.start(t)
			_, err := AcquireSchema(context.Background(), testSchemaSource(), "1.0.0",
				WithSchemaHTTPClient(srv.Client()), WithSchemaAPIBase(srv.URL), WithSchemaCacheRoot(t.TempDir()))
			if !errors.Is(err, ErrSchemaAssetMissing) {
				t.Fatalf("err = %v, want ErrSchemaAssetMissing", err)
			}
		})
	}
}

// TestAcquireSchema_UnsafeArchiveEntry_RealRefusal proves extractTarGz
// genuinely refuses a real, malicious path-traversal entry rather than
// silently writing outside the cache directory -- a real archive, a
// real ".." escape attempt, not a hypothetical.
func TestAcquireSchema_UnsafeArchiveEntry_RealRefusal(t *testing.T) {
	malicious := buildTarGz(t, map[string][]byte{
		"../../escaped.json": []byte(`{"malicious":true}`),
	})
	sums := shasumsLine(sha256HexOf(malicious), archiveFilename)
	rel := &testSchemaRelease{archive: malicious, sums: sums}
	srv := rel.start(t)

	cacheRoot := t.TempDir()
	_, err := AcquireSchema(context.Background(), testSchemaSource(), "1.0.0",
		WithSchemaHTTPClient(srv.Client()), WithSchemaAPIBase(srv.URL), WithSchemaCacheRoot(cacheRoot))
	if !errors.Is(err, ErrSchemaArchiveUnsafe) {
		t.Fatalf("err = %v, want ErrSchemaArchiveUnsafe", err)
	}
	if _, statErr := os.Stat(filepath.Join(cacheRoot, "..", "..", "escaped.json")); statErr == nil {
		t.Fatal("the malicious entry actually escaped the cache root -- real path traversal succeeded")
	}
}

func TestParseSchemaSource(t *testing.T) {
	cases := []struct {
		in   string
		want SchemaSource
	}{
		{"ubiquex/aws", SchemaSource{Hostname: "github.com", Namespace: "ubiquex", Type: "aws"}},
		{"github.com/ubiquex/aws", SchemaSource{Hostname: "github.com", Namespace: "ubiquex", Type: "aws"}},
	}
	for _, c := range cases {
		got, err := ParseSchemaSource(c.in)
		if err != nil {
			t.Fatalf("ParseSchemaSource(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseSchemaSource(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseSchemaSource_Invalid(t *testing.T) {
	if _, err := ParseSchemaSource("just-one-part"); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("err = %v, want ErrInvalidSource", err)
	}
}
