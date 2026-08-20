package provider

import (
	"bytes"
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

// testSchemaRelease is a fully scriptable fake GitHub Releases server,
// mirroring testRegistry's own shape one level up: swap in snapshot/sums
// bytes or an explicit HTTP status to corrupt exactly one part of the
// story per test.
type testSchemaRelease struct {
	notFound bool
	snapshot []byte
	sums     []byte
	// omitSums/omitSnapshot simulate a malformed release missing one of
	// its two required assets.
	omitSums     bool
	omitSnapshot bool
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
		if !r.omitSnapshot {
			rel.Assets = append(rel.Assets, githubAsset{Name: snapshotFilename, BrowserDownloadURL: srv.URL + "/dl/" + snapshotFilename})
		}
		if !r.omitSums {
			rel.Assets = append(rel.Assets, githubAsset{Name: checksumsFilename, BrowserDownloadURL: srv.URL + "/dl/" + checksumsFilename})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rel); err != nil {
			t.Errorf("encode JSON response: %v", err)
		}
	})
	mux.HandleFunc("/dl/"+snapshotFilename, func(w http.ResponseWriter, req *http.Request) {
		w.Write(r.snapshot)
	})
	mux.HandleFunc("/dl/"+checksumsFilename, func(w http.ResponseWriter, req *http.Request) {
		w.Write(r.sums)
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAcquireSchema_HappyPath(t *testing.T) {
	snapshot := []byte(`{"schema_format":1,"provider":"widget","version":"1.0.0"}`)
	sums := shasumsLine(sha256HexOf(snapshot), snapshotFilename)
	rel := &testSchemaRelease{snapshot: snapshot, sums: sums}
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
	if result.SHA256 != sha256HexOf(snapshot) {
		t.Fatalf("SHA256 = %s, want %s", result.SHA256, sha256HexOf(snapshot))
	}
	got, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read acquired snapshot: %v", err)
	}
	if !bytes.Equal(got, snapshot) {
		t.Fatal("acquired snapshot content mismatch")
	}
}

func TestAcquireSchema_SecondCallHitsCache(t *testing.T) {
	snapshot := []byte(`{"schema_format":1}`)
	sums := shasumsLine(sha256HexOf(snapshot), snapshotFilename)
	rel := &testSchemaRelease{snapshot: snapshot, sums: sums}
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
	if second.Path != first.Path || second.SHA256 != first.SHA256 {
		t.Fatalf("cache hit diverged from original acquisition: %+v vs %+v", second, first)
	}
}

func TestAcquireSchema_MirrorHitSkipsNetworkAndVerification(t *testing.T) {
	mirrorRoot := t.TempDir()
	src := testSchemaSource()
	dir := filepath.Join(mirrorRoot, src.Namespace, src.Type, "1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("whatever the operator put here, unverified")
	if err := os.WriteFile(filepath.Join(dir, snapshotFilename), content, 0o644); err != nil {
		t.Fatal(err)
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
	if result.SHA256 != sha256HexOf(content) {
		t.Fatalf("SHA256 = %s, want %s", result.SHA256, sha256HexOf(content))
	}
}

func TestAcquireSchema_ChecksumMismatch(t *testing.T) {
	snapshot := []byte(`{"schema_format":1}`)
	sums := shasumsLine(sha256HexOf([]byte("not the real snapshot bytes")), snapshotFilename)
	rel := &testSchemaRelease{snapshot: snapshot, sums: sums}
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
		{"no snapshot.json", &testSchemaRelease{omitSnapshot: true, sums: []byte("x")}},
		{"no SHA256SUMS", &testSchemaRelease{omitSums: true, snapshot: []byte("x")}},
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
