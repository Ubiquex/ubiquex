package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// --- test fixture helpers -------------------------------------------------

// testSigningKey is a throwaway OpenPGP keypair generated once per test
// binary run, used to sign fixture SHA256SUMS files exactly the way a real
// registry's signing key would.
func testSigningKey(t *testing.T) *openpgp.Entity {
	t.Helper()
	entity, err := openpgp.NewEntity("ubx test", "test fixture key, not a real signing key", "test@example.invalid", nil)
	if err != nil {
		t.Fatalf("generate test signing key: %v", err)
	}
	return entity
}

func armoredPublicKey(t *testing.T, entity *openpgp.Entity) string {
	t.Helper()
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, "PGP PUBLIC KEY BLOCK", nil)
	if err != nil {
		t.Fatalf("armor.Encode: %v", err)
	}
	if err := entity.Serialize(w); err != nil {
		t.Fatalf("serialize public key: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close armor writer: %v", err)
	}
	return buf.String()
}

func detachSign(t *testing.T, entity *openpgp.Entity, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := openpgp.DetachSign(&buf, entity, bytes.NewReader(content), nil); err != nil {
		t.Fatalf("detach sign: %v", err)
	}
	return buf.Bytes()
}

// buildProviderZip builds a minimal, valid provider archive: one regular
// file, the shape most Terraform/OpenTofu provider releases ship.
func buildProviderZip(t *testing.T, filename string, content []byte) []byte {
	t.Helper()
	return buildProviderZipWithExtras(t, filename, content, nil)
}

// buildProviderZipWithExtras builds a provider archive with extra
// non-binary files alongside the real one — the shape empirically found in
// OpenTofu's mirrored terraform-provider-aws release, which also ships
// CHANGELOG.md/LICENSE/README.md next to the binary.
func buildProviderZipWithExtras(t *testing.T, filename string, content []byte, extras map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(filename)
	if err != nil {
		t.Fatalf("zip.Create: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	for name, data := range extras {
		ew, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%q): %v", name, err)
		}
		if _, err := ew.Write(data); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

func shasumsLine(digest, filename string) []byte {
	return []byte(fmt.Sprintf("%s  %s\n", digest, filename))
}

// testRegistry is a fully scriptable fake registry server: each field is
// swapped in for the corresponding real thing, so a test can corrupt
// exactly one part of the story (archive bytes, SHA256SUMS content,
// signature, or metadata itself) while leaving the rest realistic.
type testRegistry struct {
	metadataStatus int // 0 means 200
	archive        []byte
	shasums        []byte
	signature      []byte
	filename       string
}

// startWithKeys starts a fake registry server advertising keys as its
// signing keys for the one release testRegistry describes.
func (r *testRegistry) startWithKeys(t *testing.T, keys []registryGPGPublicKey) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/v1/providers/", func(w http.ResponseWriter, req *http.Request) {
		if r.metadataStatus != 0 {
			w.WriteHeader(r.metadataStatus)
			return
		}
		meta := registryPackageMetadata{
			Filename:            r.filename,
			DownloadURL:         srv.URL + "/dl/archive.zip",
			ShasumsURL:          srv.URL + "/dl/SHA256SUMS",
			ShasumsSignatureURL: srv.URL + "/dl/SHA256SUMS.sig",
			SigningKeys:         registrySigningKeys{GPGPublicKeys: keys},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(meta); err != nil {
			t.Errorf("encode JSON response: %v", err)
		}
	})
	mux.HandleFunc("/dl/archive.zip", func(w http.ResponseWriter, req *http.Request) {
		w.Write(r.archive)
	})
	mux.HandleFunc("/dl/SHA256SUMS", func(w http.ResponseWriter, req *http.Request) {
		w.Write(r.shasums)
	})
	mux.HandleFunc("/dl/SHA256SUMS.sig", func(w http.ResponseWriter, req *http.Request) {
		w.Write(r.signature)
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// --- tests -----------------------------------------------------------------

func testSource() Source {
	return Source{Hostname: "registry.terraform.io", Namespace: "acme", Type: "widget"}
}

func TestAcquire_HappyPath(t *testing.T) {
	key := testSigningKey(t)
	filename := "terraform-provider-widget_v1.0.0_x5"
	content := []byte("#!/bin/sh\necho fake provider binary\n")
	archive := buildProviderZip(t, filename, content)
	shasums := shasumsLine(sha256HexOf(archive), filename)
	sig := detachSign(t, key, shasums)

	reg := &testRegistry{archive: archive, shasums: shasums, signature: sig, filename: filename}
	srv := reg.startWithKeys(t, []registryGPGPublicKey{{KeyID: "test", ASCIIArmor: armoredPublicKey(t, key)}})

	cacheRoot := t.TempDir()
	result, err := Acquire(context.Background(), testSource(), "1.0.0",
		WithHTTPClient(srv.Client()), WithRegistryAPIBase(srv.URL+"/v1/providers"),
		WithCacheRoot(cacheRoot), WithPlatform("linux", "amd64"))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if result.FromMirror || result.FromCache {
		t.Fatalf("expected a fresh network acquisition, got %+v", result)
	}
	if result.SHA256 != sha256HexOf(content) {
		t.Fatalf("SHA256 = %s, want sha256 of the extracted binary %s", result.SHA256, sha256HexOf(content))
	}
	got, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read acquired binary: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("extracted binary content mismatch")
	}
}

// TestAcquire_ArchiveWithExtraFiles covers a real shape found while
// verifying this against the actual registry: OpenTofu's mirrored release
// archive for terraform-provider-aws ships CHANGELOG.md/LICENSE/README.md
// alongside the binary, not just the one file some other providers' zips
// contain. extractSingleFile must pick the "terraform-provider-*" entry,
// not error out or grab the wrong file.
func TestAcquire_ArchiveWithExtraFiles(t *testing.T) {
	key := testSigningKey(t)
	filename := "terraform-provider-widget"
	content := []byte("the real provider binary")
	archive := buildProviderZipWithExtras(t, filename, content, map[string][]byte{
		"CHANGELOG.md": []byte("# Changelog\n"),
		"LICENSE":      []byte("MPL-2.0\n"),
		"README.md":    []byte("# widget provider\n"),
	})
	shasums := shasumsLine(sha256HexOf(archive), filename)
	sig := detachSign(t, key, shasums)

	reg := &testRegistry{archive: archive, shasums: shasums, signature: sig, filename: filename}
	srv := reg.startWithKeys(t, []registryGPGPublicKey{{KeyID: "test", ASCIIArmor: armoredPublicKey(t, key)}})

	result, err := Acquire(context.Background(), testSource(), "1.0.0",
		WithHTTPClient(srv.Client()), WithRegistryAPIBase(srv.URL+"/v1/providers"),
		WithCacheRoot(t.TempDir()), WithPlatform("linux", "amd64"))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if filepath.Base(result.Path) != filename {
		t.Fatalf("extracted %q, want %q", filepath.Base(result.Path), filename)
	}
	got, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read acquired binary: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("extracted the wrong file's content")
	}
}

func TestAcquire_CorruptedDownload(t *testing.T) {
	key := testSigningKey(t)
	filename := "terraform-provider-widget_v1.0.0_x5"
	content := []byte("real content")
	archive := buildProviderZip(t, filename, content)
	shasums := shasumsLine(sha256HexOf(archive), filename)
	sig := detachSign(t, key, shasums)

	// The archive actually served is truncated relative to what SHA256SUMS
	// (correctly, validly signed) describes -- simulating a connection that
	// dropped partway through the download.
	corrupted := archive[:len(archive)/2]

	reg := &testRegistry{archive: corrupted, shasums: shasums, signature: sig, filename: filename}
	srv := reg.startWithKeys(t, []registryGPGPublicKey{{KeyID: "test", ASCIIArmor: armoredPublicKey(t, key)}})

	_, err := Acquire(context.Background(), testSource(), "1.0.0",
		WithHTTPClient(srv.Client()), WithRegistryAPIBase(srv.URL+"/v1/providers"),
		WithCacheRoot(t.TempDir()), WithPlatform("linux", "amd64"))
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got %v, want ErrChecksumMismatch", err)
	}
}

func TestAcquire_BadChecksum(t *testing.T) {
	key := testSigningKey(t)
	filename := "terraform-provider-widget_v1.0.0_x5"
	archive := buildProviderZip(t, filename, []byte("real content"))
	// SHA256SUMS lists a checksum that doesn't match the archive at all
	// (still validly signed -- the signature can't save a wrong checksum).
	shasums := shasumsLine("0000000000000000000000000000000000000000000000000000000000000000", filename)
	sig := detachSign(t, key, shasums)

	reg := &testRegistry{archive: archive, shasums: shasums, signature: sig, filename: filename}
	srv := reg.startWithKeys(t, []registryGPGPublicKey{{KeyID: "test", ASCIIArmor: armoredPublicKey(t, key)}})

	_, err := Acquire(context.Background(), testSource(), "1.0.0",
		WithHTTPClient(srv.Client()), WithRegistryAPIBase(srv.URL+"/v1/providers"),
		WithCacheRoot(t.TempDir()), WithPlatform("linux", "amd64"))
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got %v, want ErrChecksumMismatch", err)
	}
}

func TestAcquire_BadSignature(t *testing.T) {
	signingKey := testSigningKey(t)
	otherKey := testSigningKey(t) // signs with a DIFFERENT key than the one advertised

	filename := "terraform-provider-widget_v1.0.0_x5"
	archive := buildProviderZip(t, filename, []byte("real content"))
	shasums := shasumsLine(sha256HexOf(archive), filename)
	sig := detachSign(t, otherKey, shasums)

	reg := &testRegistry{archive: archive, shasums: shasums, signature: sig, filename: filename}
	// Advertises signingKey's public key, but the signature was made with
	// otherKey -- must not verify.
	srv := reg.startWithKeys(t, []registryGPGPublicKey{{KeyID: "test", ASCIIArmor: armoredPublicKey(t, signingKey)}})

	_, err := Acquire(context.Background(), testSource(), "1.0.0",
		WithHTTPClient(srv.Client()), WithRegistryAPIBase(srv.URL+"/v1/providers"),
		WithCacheRoot(t.TempDir()), WithPlatform("linux", "amd64"))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("got %v, want ErrBadSignature", err)
	}
}

func TestAcquire_BadSignature_CorruptedBytes(t *testing.T) {
	key := testSigningKey(t)
	filename := "terraform-provider-widget_v1.0.0_x5"
	archive := buildProviderZip(t, filename, []byte("real content"))
	shasums := shasumsLine(sha256HexOf(archive), filename)
	sig := detachSign(t, key, shasums)
	sig[len(sig)-1] ^= 0xFF // flip a byte -- corrupt but still a plausible-length blob

	reg := &testRegistry{archive: archive, shasums: shasums, signature: sig, filename: filename}
	srv := reg.startWithKeys(t, []registryGPGPublicKey{{KeyID: "test", ASCIIArmor: armoredPublicKey(t, key)}})

	_, err := Acquire(context.Background(), testSource(), "1.0.0",
		WithHTTPClient(srv.Client()), WithRegistryAPIBase(srv.URL+"/v1/providers"),
		WithCacheRoot(t.TempDir()), WithPlatform("linux", "amd64"))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("got %v, want ErrBadSignature", err)
	}
}

func TestAcquire_MissingPlatform(t *testing.T) {
	reg := &testRegistry{metadataStatus: http.StatusNotFound}
	srv := reg.startWithKeys(t, nil)

	_, err := Acquire(context.Background(), testSource(), "1.0.0",
		WithHTTPClient(srv.Client()), WithRegistryAPIBase(srv.URL+"/v1/providers"),
		WithCacheRoot(t.TempDir()), WithPlatform("plan9", "arm"))
	if !errors.Is(err, ErrPlatformNotFound) {
		t.Fatalf("got %v, want ErrPlatformNotFound", err)
	}
}

func TestAcquire_MirrorHit_NoNetworkCall(t *testing.T) {
	mirrorDir := t.TempDir()
	src := testSource()
	dir := filepath.Join(mirrorDir, src.Namespace, src.Type, "1.0.0", "linux_amd64")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("mirror binary content")
	if err := os.WriteFile(filepath.Join(dir, "terraform-provider-widget"), content, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(providerMirrorEnv, mirrorDir)

	// No test server at all -- if Acquire tries the network, this
	// unreachable base URL will make the test fail with a network error
	// rather than silently succeeding for the wrong reason.
	result, err := Acquire(context.Background(), src, "1.0.0",
		WithRegistryAPIBase("http://127.0.0.1:0/unreachable"),
		WithCacheRoot(t.TempDir()), WithPlatform("linux", "amd64"))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !result.FromMirror {
		t.Fatalf("expected FromMirror=true, got %+v", result)
	}
	if result.SHA256 != sha256HexOf(content) {
		t.Fatalf("SHA256 mismatch for mirror hit")
	}
}

func TestAcquire_MirrorMiss_FallsThroughToNetwork(t *testing.T) {
	// Mirror dir exists but has nothing for this provider -- must not error,
	// must fall through to (and succeed via) the network path.
	mirrorDir := t.TempDir()
	t.Setenv(providerMirrorEnv, mirrorDir)

	key := testSigningKey(t)
	filename := "terraform-provider-widget_v1.0.0_x5"
	content := []byte("network content")
	archive := buildProviderZip(t, filename, content)
	shasums := shasumsLine(sha256HexOf(archive), filename)
	sig := detachSign(t, key, shasums)

	reg := &testRegistry{archive: archive, shasums: shasums, signature: sig, filename: filename}
	srv := reg.startWithKeys(t, []registryGPGPublicKey{{KeyID: "test", ASCIIArmor: armoredPublicKey(t, key)}})

	result, err := Acquire(context.Background(), testSource(), "1.0.0",
		WithHTTPClient(srv.Client()), WithRegistryAPIBase(srv.URL+"/v1/providers"),
		WithCacheRoot(t.TempDir()), WithPlatform("linux", "amd64"))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if result.FromMirror {
		t.Fatal("expected a mirror miss to fall through, not report FromMirror")
	}
	if result.SHA256 != sha256HexOf(content) {
		t.Fatalf("SHA256 mismatch after mirror-miss fallthrough")
	}
}

func TestAcquire_CacheHit_NoSecondNetworkCall(t *testing.T) {
	key := testSigningKey(t)
	filename := "terraform-provider-widget_v1.0.0_x5"
	content := []byte("cached content")
	archive := buildProviderZip(t, filename, content)
	shasums := shasumsLine(sha256HexOf(archive), filename)
	sig := detachSign(t, key, shasums)

	reg := &testRegistry{archive: archive, shasums: shasums, signature: sig, filename: filename}
	srv := reg.startWithKeys(t, []registryGPGPublicKey{{KeyID: "test", ASCIIArmor: armoredPublicKey(t, key)}})

	cacheRoot := t.TempDir()
	first, err := Acquire(context.Background(), testSource(), "1.0.0",
		WithHTTPClient(srv.Client()), WithRegistryAPIBase(srv.URL+"/v1/providers"),
		WithCacheRoot(cacheRoot), WithPlatform("linux", "amd64"))
	if err != nil {
		t.Fatalf("Acquire (1st): %v", err)
	}
	if first.FromCache {
		t.Fatal("first call should not be a cache hit")
	}

	// Second call: point at an unreachable registry -- if this succeeds, it
	// can only be because the cache was actually used.
	second, err := Acquire(context.Background(), testSource(), "1.0.0",
		WithRegistryAPIBase("http://127.0.0.1:0/unreachable"),
		WithCacheRoot(cacheRoot), WithPlatform("linux", "amd64"))
	if err != nil {
		t.Fatalf("Acquire (2nd, should be cache hit): %v", err)
	}
	if !second.FromCache {
		t.Fatalf("expected FromCache=true, got %+v", second)
	}
	if second.Path != first.Path {
		t.Fatalf("cache hit path %q != original path %q", second.Path, first.Path)
	}
}

func TestParseSource(t *testing.T) {
	cases := []struct {
		in   string
		want Source
	}{
		{"hashicorp/aws", Source{Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "aws"}},
		{"registry.terraform.io/hashicorp/aws", Source{Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "aws"}},
	}
	for _, c := range cases {
		got, err := ParseSource(c.in)
		if err != nil {
			t.Fatalf("ParseSource(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseSource(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseSource_Invalid(t *testing.T) {
	_, err := ParseSource("not-a-valid-source/with/way/too/many/parts")
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("got %v, want ErrInvalidSource", err)
	}
}
