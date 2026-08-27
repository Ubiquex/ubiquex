package provider

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// AcquireDynamicProviderBinaryResult is what AcquireDynamicProviderBinary
// found or produced -- the ubx-provider-dynamic-binary analog of
// AcquireResult (a real Terraform-registry provider binary) and
// AcquireSchemaResult (a real schema snapshot directory). UBI-194: before
// this existed, the ONLY way to run ubx-provider-dynamic at all was a
// local checkout (UBX_PROVIDER_DYNAMIC_REPO) -- every real
// [providers.<name>] pin already published (kubernetes/datadog/github/
// google/aws/azure) resolved a real schema snapshot but had nothing real
// to serve it with outside this project.
type AcquireDynamicProviderBinaryResult struct {
	// Path is the local filesystem path to the ready-to-run
	// ubx-provider-dynamic binary -- pass it straight to Launch, exactly
	// like AcquireResult.Path.
	Path string

	// FromMirror is true if Path came from UBX_DYNAMIC_PROVIDER_MIRROR, a
	// local file used as-is, unverified -- mirrors AcquireResult's own
	// real discipline.
	FromMirror bool

	// FromCache is true if Path came from ubx's own cache
	// (~/.ubx/dynamic-provider-bin/...): a hit is used directly, already
	// verified the first time it was written there.
	FromCache bool

	// SHA256 is the hex SHA-256 digest of the real, downloaded binary --
	// only ever populated for a fresh download, matching
	// AcquireResult.SHA256's own real, fresh-download-only convention.
	SHA256 string
}

type acquireDynamicProviderBinaryConfig struct {
	httpClient *http.Client
	apiBase    string
	cacheRoot  string
	goos       string
	goarch     string
}

// AcquireDynamicProviderBinaryOption configures AcquireDynamicProviderBinary
// -- mirrors AcquireOption/AcquireSchemaOption's own shape.
type AcquireDynamicProviderBinaryOption func(*acquireDynamicProviderBinaryConfig)

// WithDynamicProviderBinaryHTTPClient overrides the http.Client
// AcquireDynamicProviderBinary uses (default: http.DefaultClient).
func WithDynamicProviderBinaryHTTPClient(c *http.Client) AcquireDynamicProviderBinaryOption {
	return func(cfg *acquireDynamicProviderBinaryConfig) { cfg.httpClient = c }
}

// WithDynamicProviderBinaryAPIBase overrides the GitHub API base URL
// (default: githubAPIBase, api.github.com).
func WithDynamicProviderBinaryAPIBase(base string) AcquireDynamicProviderBinaryOption {
	return func(cfg *acquireDynamicProviderBinaryConfig) { cfg.apiBase = base }
}

// WithDynamicProviderBinaryCacheRoot overrides the verified-binary cache
// directory (default: ~/.ubx/dynamic-provider-bin).
func WithDynamicProviderBinaryCacheRoot(dir string) AcquireDynamicProviderBinaryOption {
	return func(cfg *acquireDynamicProviderBinaryConfig) { cfg.cacheRoot = dir }
}

// WithDynamicProviderBinaryPlatform overrides the target os/arch (default:
// runtime.GOOS/GOARCH).
func WithDynamicProviderBinaryPlatform(goos, goarch string) AcquireDynamicProviderBinaryOption {
	return func(cfg *acquireDynamicProviderBinaryConfig) { cfg.goos = goos; cfg.goarch = goarch }
}

// dynamicProviderBinaryMirrorEnv mirrors providerMirrorEnv/schemaMirrorEnv
// one level down -- a local directory to check before ever touching the
// network. A miss is not an error.
const dynamicProviderBinaryMirrorEnv = "UBX_DYNAMIC_PROVIDER_MIRROR"

// dynamicProviderBinaryOwner/dynamicProviderBinaryRepo are
// ubx-provider-dynamic's own real, fixed GitHub identity. Unlike a
// schema snapshot (one real repo per real provider) or a Terraform-
// registry binary (one real repo per real vendor), there is exactly ONE
// real ubx-provider-dynamic binary -- no per-caller namespace/type
// resolution is needed at all, unlike SchemaSource/Source.
const (
	dynamicProviderBinaryOwner = "Ubiquex"
	dynamicProviderBinaryRepo  = "ubx-provider-dynamic"
)

// dynamicProviderBinaryChecksumsFilename is the one real release asset
// every ubx-provider-dynamic release covers every real platform archive
// with -- one shared SHA256SUMS, not one per platform, matching the real
// OpenTofu-registry-style convention (Acquire, verify.go) rather than
// AcquireSchema's own one-archive-one-checksum shape, since a real
// ubx-provider-dynamic release genuinely ships more than one real
// platform archive.
const dynamicProviderBinaryChecksumsFilename = "SHA256SUMS"

var (
	// ErrDynamicProviderBinaryNotFound means no GitHub release matches
	// the requested version at all.
	ErrDynamicProviderBinaryNotFound = errors.New("ubx-provider-dynamic release not found")

	// ErrDynamicProviderBinaryAssetMissing means the release exists but
	// is missing the platform archive or SHA256SUMS.
	ErrDynamicProviderBinaryAssetMissing = errors.New("ubx-provider-dynamic release is missing a required asset")

	// ErrDynamicProviderBinaryChecksumMismatch means the downloaded
	// platform archive's own SHA-256 doesn't match its SHA256SUMS entry.
	ErrDynamicProviderBinaryChecksumMismatch = errors.New("ubx-provider-dynamic binary checksum mismatch")

	// ErrDynamicProviderBinaryArchiveUnsafe means the platform archive
	// contains an entry this package refuses to extract, or doesn't
	// contain exactly the one real binary file expected.
	ErrDynamicProviderBinaryArchiveUnsafe = errors.New("ubx-provider-dynamic archive contains an unsafe or unexpected entry")
)

// AcquireDynamicProviderBinary ensures a verified ubx-provider-dynamic
// binary for version exists locally and returns its path, ready to pass
// to Launch. version must be an explicit, fully-resolved semver string
// (e.g. "1.0.0") -- like Acquire and AcquireSchema, AcquireDynamicProviderBinary
// never does "latest" resolution.
//
// Resolution order (identical discipline to Acquire/AcquireSchema,
// mirror-first, explicit version only, verify once trust forever):
//  1. UBX_DYNAMIC_PROVIDER_MIRROR, if set: a hit is used directly,
//     unverified. A miss falls through -- not an error.
//  2. ubx's own cache (~/.ubx/dynamic-provider-bin/<version>/<os_arch>/):
//     a hit is used directly.
//  3. Otherwise: fetch the GitHub release tagged "v<version>" from
//     github.com/Ubiquex/ubx-provider-dynamic, download
//     ubx-provider-dynamic_<version>_<os>_<arch>.tar.gz and SHA256SUMS,
//     confirm the archive's own SHA-256 matches its SHA256SUMS entry,
//     extract the one real binary inside into the cache, and return its
//     path.
func AcquireDynamicProviderBinary(ctx context.Context, version string, opts ...AcquireDynamicProviderBinaryOption) (*AcquireDynamicProviderBinaryResult, error) {
	cfg := acquireDynamicProviderBinaryConfig{
		httpClient: http.DefaultClient,
		apiBase:    githubAPIBase,
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.cacheRoot == "" {
		root, err := defaultDynamicProviderBinaryCacheRoot()
		if err != nil {
			return nil, fmt.Errorf("acquire ubx-provider-dynamic@%s: %w", version, err)
		}
		cfg.cacheRoot = root
	}

	if path := lookupDynamicProviderBinaryMirror(version, cfg.goos, cfg.goarch); path != "" {
		sum, err := sha256File(path)
		if err != nil {
			return nil, fmt.Errorf("acquire ubx-provider-dynamic@%s: mirror: %w", version, err)
		}
		return &AcquireDynamicProviderBinaryResult{Path: path, FromMirror: true, SHA256: sum}, nil
	}

	dir := dynamicProviderBinaryPlatformDir(cfg.cacheRoot, version, cfg.goos, cfg.goarch)
	if path := findSingleFile(dir); path != "" {
		sum, err := sha256File(path)
		if err != nil {
			return nil, fmt.Errorf("acquire ubx-provider-dynamic@%s: cache: %w", version, err)
		}
		return &AcquireDynamicProviderBinaryResult{Path: path, FromCache: true, SHA256: sum}, nil
	}

	path, sum, err := downloadVerifyAndExtractDynamicProviderBinary(ctx, cfg, version, dir)
	if err != nil {
		return nil, fmt.Errorf("acquire ubx-provider-dynamic@%s: %w", version, err)
	}
	return &AcquireDynamicProviderBinaryResult{Path: path, SHA256: sum}, nil
}

func downloadVerifyAndExtractDynamicProviderBinary(ctx context.Context, cfg acquireDynamicProviderBinaryConfig, version, destDir string) (path, digest string, err error) {
	tag := "v" + version
	rel, err := fetchRelease(ctx, cfg.httpClient, cfg.apiBase, dynamicProviderBinaryOwner, dynamicProviderBinaryRepo, tag)
	if err != nil {
		if errors.Is(err, ErrSchemaNotFound) {
			return "", "", fmt.Errorf("%w: %s/%s tag %s", ErrDynamicProviderBinaryNotFound, dynamicProviderBinaryOwner, dynamicProviderBinaryRepo, tag)
		}
		return "", "", err
	}

	archiveFilename := dynamicProviderBinaryArchiveFilename(version, cfg.goos, cfg.goarch)
	archiveAsset, ok := rel.asset(archiveFilename)
	if !ok {
		return "", "", fmt.Errorf("%w: %s has no %q asset", ErrDynamicProviderBinaryAssetMissing, dynamicProviderBinaryRepo, archiveFilename)
	}
	sumsAsset, ok := rel.asset(dynamicProviderBinaryChecksumsFilename)
	if !ok {
		return "", "", fmt.Errorf("%w: %s has no %q asset", ErrDynamicProviderBinaryAssetMissing, dynamicProviderBinaryRepo, dynamicProviderBinaryChecksumsFilename)
	}

	sumsContent, err := httpGetBytes(ctx, cfg.httpClient, sumsAsset.BrowserDownloadURL)
	if err != nil {
		return "", "", fmt.Errorf("download %s: %w", dynamicProviderBinaryChecksumsFilename, err)
	}
	want, err := expectedSHA256(sumsContent, archiveFilename)
	if err != nil {
		return "", "", err
	}

	archiveBytes, err := httpGetBytes(ctx, cfg.httpClient, archiveAsset.BrowserDownloadURL)
	if err != nil {
		return "", "", fmt.Errorf("download %s: %w", archiveFilename, err)
	}
	got := sha256HexOf(archiveBytes)
	if got != want {
		return "", "", fmt.Errorf("%w: %s: SHA256SUMS says %s, downloaded archive is %s",
			ErrDynamicProviderBinaryChecksumMismatch, archiveFilename, want, got)
	}

	binPath, err := extractSingleFileFromTarGz(archiveBytes, destDir)
	if err != nil {
		return "", "", fmt.Errorf("extract %s: %w", archiveFilename, err)
	}
	sum, err := sha256File(binPath)
	if err != nil {
		return "", "", err
	}
	return binPath, sum, nil
}

// dynamicProviderBinaryArchiveFilename is publish.yml's own real, fixed
// per-platform naming convention (ubx-provider-dynamic repo) -- one
// archive per real os/arch combination, unlike AcquireSchema's own
// single, platform-independent archiveFilename.
func dynamicProviderBinaryArchiveFilename(version, goos, goarch string) string {
	return fmt.Sprintf("ubx-provider-dynamic_%s_%s_%s.tar.gz", version, goos, goarch)
}

// dynamicProviderBinaryFilename is the one real file every extracted
// platform archive contains -- ubx-provider-dynamic on every real
// platform (no ".exe": windows isn't a real, published platform this
// binary targets today, matching publish.yml's own real build matrix).
const dynamicProviderBinaryFilename = "ubx-provider-dynamic"

// extractSingleFileFromTarGz writes the one real file data (a gzip-
// compressed tar archive, publish.yml's own real per-platform shape) is
// expected to contain into destDir/dynamicProviderBinaryFilename, with
// real, executable (0o755) permissions -- unlike AcquireSchema's own
// extractTarGz (a whole real directory tree of plain JSON, 0o644, no
// execute bit needed), this archive's own one real entry IS the binary
// ubx-provider-dynamic itself. Refuses loudly, ErrDynamicProviderBinaryArchiveUnsafe,
// on anything else: more than one real entry, an absolute path, a ".."
// path segment, a symlink, or a non-regular-file entry.
func extractSingleFileFromTarGz(data []byte, destDir string) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var found bool
	var out []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar entry: %w", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}

		cleaned := filepath.Clean(hdr.Name)
		if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: entry %q escapes the archive root", ErrDynamicProviderBinaryArchiveUnsafe, hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("%w: entry %q has unsupported type %v", ErrDynamicProviderBinaryArchiveUnsafe, hdr.Name, hdr.Typeflag)
		}
		if found {
			return "", fmt.Errorf("%w: more than one real file entry, expected exactly one (the binary itself)", ErrDynamicProviderBinaryArchiveUnsafe)
		}
		found = true

		buf, err := io.ReadAll(tr)
		if err != nil {
			return "", fmt.Errorf("read entry %q: %w", hdr.Name, err)
		}
		out = buf
	}
	if !found {
		return "", fmt.Errorf("%w: archive has no real file entry at all", ErrDynamicProviderBinaryArchiveUnsafe)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	binPath := filepath.Join(destDir, dynamicProviderBinaryFilename)
	if err := os.WriteFile(binPath, out, 0o755); err != nil {
		return "", err
	}
	return binPath, nil
}

// defaultDynamicProviderBinaryCacheRoot is ~/.ubx/dynamic-provider-bin --
// a sibling of Acquire's own ~/.ubx/providers and AcquireSchema's own
// ~/.ubx/schemas, distinct from both: this caches ubx's OWN binary, not
// a real Terraform-registry provider or a real schema snapshot.
func defaultDynamicProviderBinaryCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ubx", "dynamic-provider-bin"), nil
}

// dynamicProviderBinaryPlatformDir is <root>/<version>/<os_arch>/ -- no
// namespace/type dimension, unlike platformDir (cache.go): there is
// exactly one real ubx-provider-dynamic identity, so nothing to
// disambiguate.
func dynamicProviderBinaryPlatformDir(root, version, goos, goarch string) string {
	return filepath.Join(root, version, goos+"_"+goarch)
}

// lookupDynamicProviderBinaryMirror mirrors lookupMirror/lookupSchemaMirror
// one level down.
func lookupDynamicProviderBinaryMirror(version, goos, goarch string) string {
	mirrorDir := os.Getenv(dynamicProviderBinaryMirrorEnv)
	if mirrorDir == "" {
		return ""
	}
	return findSingleFile(dynamicProviderBinaryPlatformDir(mirrorDir, version, goos, goarch))
}
