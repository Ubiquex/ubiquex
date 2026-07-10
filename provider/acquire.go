package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// AcquireResult is what Acquire found or produced.
type AcquireResult struct {
	// Path is the local filesystem path to the ready-to-run provider
	// binary — pass it straight to Launch.
	Path string

	// FromMirror is true if Path came from UBX_PROVIDER_MIRROR. A mirror
	// hit is used as-is, with no download or signature verification — see
	// docs/architecture.md: a local file the operator put there is
	// trusted differently than a network download.
	FromMirror bool

	// FromCache is true if Path came from a previously-verified entry in
	// ubx's own cache (~/.ubx/providers/...). Once verified, always
	// verified: Acquire doesn't re-download or re-verify a cache hit.
	FromCache bool

	// SHA256 is the hex SHA-256 digest of the file at Path — always
	// computed, even for a mirror hit, so callers always have something
	// concrete to attribute a read to (see core.ScanRequest.ProviderChecksum).
	SHA256 string
}

type acquireConfig struct {
	httpClient *http.Client
	apiBase    string
	cacheRoot  string
	goos       string
	goarch     string
}

// AcquireOption configures Acquire. The With* functions below exist for
// tests (pointing at a local httptest server and a scratch cache/platform)
// — real callers don't need any of them.
type AcquireOption func(*acquireConfig)

// WithHTTPClient overrides the http.Client Acquire uses (default:
// http.DefaultClient).
func WithHTTPClient(c *http.Client) AcquireOption {
	return func(cfg *acquireConfig) { cfg.httpClient = c }
}

// WithRegistryAPIBase overrides the registry API base URL (default:
// registryAPIBase, registry.opentofu.org).
func WithRegistryAPIBase(base string) AcquireOption {
	return func(cfg *acquireConfig) { cfg.apiBase = base }
}

// WithCacheRoot overrides the verified-binary cache directory (default:
// ~/.ubx/providers).
func WithCacheRoot(dir string) AcquireOption {
	return func(cfg *acquireConfig) { cfg.cacheRoot = dir }
}

// WithPlatform overrides the target os/arch (default: runtime.GOOS/GOARCH
// — the platform ubx itself is running on, which is what almost every real
// caller wants, since it's what will actually exec the binary).
func WithPlatform(goos, goarch string) AcquireOption {
	return func(cfg *acquireConfig) { cfg.goos = goos; cfg.goarch = goarch }
}

// Acquire ensures a verified provider binary for src@version exists
// locally and returns its path, ready to pass to Launch. version must be
// an explicit, fully-resolved version (e.g. "6.54.0") — Acquire never does
// "latest" resolution; docs/architecture.md explains why (reproducibility:
// a proposal's provider version is part of what was reviewed).
//
// Resolution order:
//  1. UBX_PROVIDER_MIRROR, if set: a hit is used directly, unverified (see
//     AcquireResult.FromMirror). A miss falls through to the next step —
//     it is not an error.
//  2. ubx's own cache (~/.ubx/providers/<hostname>/<namespace>/<type>/
//     <version>/<os_arch>/): a hit is used directly (see
//     AcquireResult.FromCache); it was already verified the first time it
//     was written there.
//  3. Otherwise: query the registry, download the provider archive and its
//     SHA256SUMS + detached signature, verify the signature against a
//     registry-served signing key, confirm the archive's SHA-256 matches
//     the signed SHA256SUMS entry, extract the one binary inside into the
//     cache, and return its path.
func Acquire(ctx context.Context, src Source, version string, opts ...AcquireOption) (*AcquireResult, error) {
	cfg := acquireConfig{
		httpClient: http.DefaultClient,
		apiBase:    registryAPIBase,
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.cacheRoot == "" {
		root, err := defaultCacheRoot()
		if err != nil {
			return nil, fmt.Errorf("acquire %s@%s: %w", src, version, err)
		}
		cfg.cacheRoot = root
	}

	if path := lookupMirror(src, version, cfg.goos, cfg.goarch); path != "" {
		sum, err := sha256File(path)
		if err != nil {
			return nil, fmt.Errorf("acquire %s@%s: mirror: %w", src, version, err)
		}
		return &AcquireResult{Path: path, FromMirror: true, SHA256: sum}, nil
	}

	dir := cacheDir(cfg.cacheRoot, src, version, cfg.goos, cfg.goarch)
	if path := findSingleFile(dir); path != "" {
		sum, err := sha256File(path)
		if err != nil {
			return nil, fmt.Errorf("acquire %s@%s: cache: %w", src, version, err)
		}
		return &AcquireResult{Path: path, FromCache: true, SHA256: sum}, nil
	}

	path, sum, err := downloadVerifyAndExtract(ctx, cfg, src, version, dir)
	if err != nil {
		return nil, fmt.Errorf("acquire %s@%s: %w", src, version, err)
	}
	return &AcquireResult{Path: path, SHA256: sum}, nil
}

func downloadVerifyAndExtract(ctx context.Context, cfg acquireConfig, src Source, version, destDir string) (path, digest string, err error) {
	meta, err := fetchPackageMetadata(ctx, cfg.httpClient, cfg.apiBase, src, version, cfg.goos, cfg.goarch)
	if err != nil {
		return "", "", err
	}

	shasumsContent, err := httpGetBytes(ctx, cfg.httpClient, meta.ShasumsURL)
	if err != nil {
		return "", "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	sigBytes, err := httpGetBytes(ctx, cfg.httpClient, meta.ShasumsSignatureURL)
	if err != nil {
		return "", "", fmt.Errorf("download SHA256SUMS signature: %w", err)
	}
	if err := verifyChecksumsSignature(shasumsContent, sigBytes, meta.SigningKeys.GPGPublicKeys); err != nil {
		return "", "", err
	}

	want, err := expectedSHA256(shasumsContent, meta.Filename)
	if err != nil {
		return "", "", err
	}

	archive, err := httpGetBytes(ctx, cfg.httpClient, meta.DownloadURL)
	if err != nil {
		return "", "", fmt.Errorf("download provider archive: %w", err)
	}
	got := sha256HexOf(archive)
	if got != want {
		return "", "", fmt.Errorf("%w: %s: SHA256SUMS says %s, downloaded archive is %s",
			ErrChecksumMismatch, meta.Filename, want, got)
	}

	binPath, err := extractSingleFile(archive, destDir)
	if err != nil {
		return "", "", fmt.Errorf("extract provider archive: %w", err)
	}
	sum, err := sha256File(binPath)
	if err != nil {
		return "", "", err
	}
	return binPath, sum, nil
}

// providerBinaryPrefix is the naming convention every Terraform-ecosystem
// provider binary follows (Terraform's own provider discovery requires it:
// "terraform-provider-<type>" or "terraform-provider-<type>_v<version>...").
// Verified empirically to matter: OpenTofu's mirrored release archives
// aren't always the one-file-only zips HashiCorp's original releases are —
// e.g. terraform-provider-aws's OpenTofu-mirrored zip also ships
// CHANGELOG.md/LICENSE/README.md alongside the binary.
const providerBinaryPrefix = "terraform-provider-"

// extractSingleFile unzips archive (in memory) into destDir and returns the
// path to the extracted provider binary. It picks the entry whose base name
// has the providerBinaryPrefix convention; if that's ambiguous (zero or
// more than one match) it falls back to "the archive has exactly one file
// total" for any oddly-packaged release that doesn't follow the naming
// convention. Anything else is a malformed/unrecognized archive shape, not
// something to guess at. filepath.Base on the entry name defends against
// zip-slip path traversal regardless of what a (by this point already
// signature-verified) archive's entry name claims.
func extractSingleFile(archive []byte, destDir string) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return "", fmt.Errorf("not a valid zip archive: %w", err)
	}

	var files, named []*zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		files = append(files, f)
		if strings.HasPrefix(filepath.Base(f.Name), providerBinaryPrefix) {
			named = append(named, f)
		}
	}

	var target *zip.File
	switch {
	case len(named) == 1:
		target = named[0]
	case len(files) == 1:
		target = files[0]
	default:
		return "", fmt.Errorf("could not identify the provider binary among %d file(s) in the archive "+
			"(expected exactly one named %q, or exactly one file total)", len(files), providerBinaryPrefix+"*")
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(destDir, filepath.Base(target.Name))

	rc, err := target.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return "", err
	}
	return path, nil
}

func sha256File(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256HexOf(b), nil
}
