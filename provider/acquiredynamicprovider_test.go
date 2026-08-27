package provider

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// buildDynamicProviderBinaryTarGz builds a real gzip-compressed tar
// archive containing exactly one real file (the fake binary content),
// matching publish.yml's own real per-platform archive shape.
func buildDynamicProviderBinaryTarGz(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "ubx-provider-dynamic", Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("tar.WriteHeader: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip.Close: %v", err)
	}
	return buf.Bytes()
}

// testDynamicProviderBinaryRelease is a fully scriptable fake GitHub
// Releases server for the real, fixed Ubiquex/ubx-provider-dynamic
// identity -- mirrors testSchemaRelease's own shape one level down (one
// real repo, not one per caller, so the URL path is fixed).
type testDynamicProviderBinaryRelease struct {
	notFound     bool
	archive      []byte
	sums         []byte
	omitSums     bool
	omitArchive  bool
	archiveName  string
	checksumName string
}

func (r *testDynamicProviderBinaryRelease) start(t *testing.T) *httptest.Server {
	t.Helper()
	if r.archiveName == "" {
		r.archiveName = dynamicProviderBinaryArchiveFilename("1.0.0", "linux", "amd64")
	}
	if r.checksumName == "" {
		r.checksumName = dynamicProviderBinaryChecksumsFilename
	}
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/repos/Ubiquex/ubx-provider-dynamic/releases/tags/v1.0.0", func(w http.ResponseWriter, req *http.Request) {
		if r.notFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rel := githubRelease{TagName: "v1.0.0"}
		if !r.omitArchive {
			rel.Assets = append(rel.Assets, githubAsset{Name: r.archiveName, BrowserDownloadURL: srv.URL + "/dl/" + r.archiveName})
		}
		if !r.omitSums {
			rel.Assets = append(rel.Assets, githubAsset{Name: r.checksumName, BrowserDownloadURL: srv.URL + "/dl/" + r.checksumName})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rel); err != nil {
			t.Errorf("encode JSON response: %v", err)
		}
	})
	mux.HandleFunc("/dl/"+r.archiveName, func(w http.ResponseWriter, req *http.Request) {
		w.Write(r.archive)
	})
	mux.HandleFunc("/dl/"+r.checksumName, func(w http.ResponseWriter, req *http.Request) {
		w.Write(r.sums)
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAcquireDynamicProviderBinary_HappyPath(t *testing.T) {
	content := []byte("fake ubx-provider-dynamic binary content")
	archive := buildDynamicProviderBinaryTarGz(t, content)
	sums := shasumsLine(sha256HexOf(archive), dynamicProviderBinaryArchiveFilename("1.0.0", "linux", "amd64"))
	rel := &testDynamicProviderBinaryRelease{archive: archive, sums: sums}
	srv := rel.start(t)

	cacheRoot := t.TempDir()
	result, err := AcquireDynamicProviderBinary(context.Background(), "1.0.0",
		WithDynamicProviderBinaryHTTPClient(srv.Client()),
		WithDynamicProviderBinaryAPIBase(srv.URL),
		WithDynamicProviderBinaryCacheRoot(cacheRoot),
		WithDynamicProviderBinaryPlatform("linux", "amd64"))
	if err != nil {
		t.Fatalf("AcquireDynamicProviderBinary: %v", err)
	}
	if result.FromMirror || result.FromCache {
		t.Fatalf("expected a fresh network acquisition, got %+v", result)
	}
	if result.SHA256 != sha256HexOf(content) {
		t.Fatalf("SHA256 = %s, want %s (the real, extracted binary's own digest, matching AcquireResult's own convention -- not the archive's)", result.SHA256, sha256HexOf(content))
	}
	got, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("extracted binary content mismatch")
	}
	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o100 == 0 {
		t.Fatalf("extracted binary is not executable: mode %v", info.Mode())
	}
}

func TestAcquireDynamicProviderBinary_SecondCallHitsCache(t *testing.T) {
	content := []byte("fake binary v2")
	archive := buildDynamicProviderBinaryTarGz(t, content)
	sums := shasumsLine(sha256HexOf(archive), dynamicProviderBinaryArchiveFilename("1.0.0", "linux", "amd64"))
	rel := &testDynamicProviderBinaryRelease{archive: archive, sums: sums}
	srv := rel.start(t)

	cacheRoot := t.TempDir()
	opts := []AcquireDynamicProviderBinaryOption{
		WithDynamicProviderBinaryHTTPClient(srv.Client()),
		WithDynamicProviderBinaryAPIBase(srv.URL),
		WithDynamicProviderBinaryCacheRoot(cacheRoot),
		WithDynamicProviderBinaryPlatform("linux", "amd64"),
	}
	first, err := AcquireDynamicProviderBinary(context.Background(), "1.0.0", opts...)
	if err != nil {
		t.Fatalf("first AcquireDynamicProviderBinary: %v", err)
	}
	if first.FromCache {
		t.Fatal("first call should not be a cache hit")
	}

	rel.notFound = true // prove the second call never touches the network again
	second, err := AcquireDynamicProviderBinary(context.Background(), "1.0.0", opts...)
	if err != nil {
		t.Fatalf("second AcquireDynamicProviderBinary: %v", err)
	}
	if !second.FromCache {
		t.Fatal("second call should be a real cache hit")
	}
	if second.Path != first.Path {
		t.Fatalf("second call resolved a different path: %s vs %s", second.Path, first.Path)
	}
}

func TestAcquireDynamicProviderBinary_MirrorHitSkipsNetwork(t *testing.T) {
	mirrorRoot := t.TempDir()
	platformDir := filepath.Join(mirrorRoot, "1.0.0", "linux_amd64")
	if err := os.MkdirAll(platformDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(platformDir, "ubx-provider-dynamic")
	if err := os.WriteFile(binPath, []byte("mirrored binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(dynamicProviderBinaryMirrorEnv, mirrorRoot)

	result, err := AcquireDynamicProviderBinary(context.Background(), "1.0.0",
		WithDynamicProviderBinaryAPIBase("http://127.0.0.1:1"), // real, unreachable -- proves no network is touched
		WithDynamicProviderBinaryPlatform("linux", "amd64"))
	if err != nil {
		t.Fatalf("AcquireDynamicProviderBinary: %v", err)
	}
	if !result.FromMirror {
		t.Fatal("expected a real mirror hit")
	}
	if result.Path != binPath {
		t.Fatalf("Path = %s, want the real mirrored file %s", result.Path, binPath)
	}
}

func TestAcquireDynamicProviderBinary_ChecksumMismatch(t *testing.T) {
	content := []byte("real content")
	archive := buildDynamicProviderBinaryTarGz(t, content)
	wrongSums := shasumsLine("0000000000000000000000000000000000000000000000000000000000000000", dynamicProviderBinaryArchiveFilename("1.0.0", "linux", "amd64"))
	rel := &testDynamicProviderBinaryRelease{archive: archive, sums: wrongSums}
	srv := rel.start(t)

	_, err := AcquireDynamicProviderBinary(context.Background(), "1.0.0",
		WithDynamicProviderBinaryHTTPClient(srv.Client()),
		WithDynamicProviderBinaryAPIBase(srv.URL),
		WithDynamicProviderBinaryCacheRoot(t.TempDir()),
		WithDynamicProviderBinaryPlatform("linux", "amd64"))
	if err == nil {
		t.Fatal("expected a real checksum-mismatch error")
	}
}

func TestAcquireDynamicProviderBinary_ReleaseNotFound(t *testing.T) {
	rel := &testDynamicProviderBinaryRelease{notFound: true}
	srv := rel.start(t)

	_, err := AcquireDynamicProviderBinary(context.Background(), "1.0.0",
		WithDynamicProviderBinaryHTTPClient(srv.Client()),
		WithDynamicProviderBinaryAPIBase(srv.URL),
		WithDynamicProviderBinaryCacheRoot(t.TempDir()),
		WithDynamicProviderBinaryPlatform("linux", "amd64"))
	if err == nil {
		t.Fatal("expected a real not-found error")
	}
}

func TestAcquireDynamicProviderBinary_MissingAsset(t *testing.T) {
	rel := &testDynamicProviderBinaryRelease{omitSums: true}
	srv := rel.start(t)

	_, err := AcquireDynamicProviderBinary(context.Background(), "1.0.0",
		WithDynamicProviderBinaryHTTPClient(srv.Client()),
		WithDynamicProviderBinaryAPIBase(srv.URL),
		WithDynamicProviderBinaryCacheRoot(t.TempDir()),
		WithDynamicProviderBinaryPlatform("linux", "amd64"))
	if err == nil {
		t.Fatal("expected a real missing-asset error")
	}
}

func TestAcquireDynamicProviderBinary_UnsafeArchive_TwoFiles_RealRefusal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"ubx-provider-dynamic", "extra-file"} {
		content := []byte("x")
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sums := shasumsLine(sha256HexOf(archive), dynamicProviderBinaryArchiveFilename("1.0.0", "linux", "amd64"))
	rel := &testDynamicProviderBinaryRelease{archive: archive, sums: sums}
	srv := rel.start(t)

	_, err := AcquireDynamicProviderBinary(context.Background(), "1.0.0",
		WithDynamicProviderBinaryHTTPClient(srv.Client()),
		WithDynamicProviderBinaryAPIBase(srv.URL),
		WithDynamicProviderBinaryCacheRoot(t.TempDir()),
		WithDynamicProviderBinaryPlatform("linux", "amd64"))
	if err == nil {
		t.Fatal("expected a real refusal for an archive with more than one real file entry")
	}
}
