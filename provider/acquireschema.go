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
	"strings"
)

// AcquireSchemaResult is what AcquireSchema found or produced -- the
// schema-snapshot analog of AcquireResult.
type AcquireSchemaResult struct {
	// Path is the local filesystem path to the ready-to-load snapshot
	// DIRECTORY -- pass it to internal/snapshot.LoadSplit
	// (ubx-provider-dynamic's own package; AcquireSchema deliberately
	// never parses the snapshot itself, see this file's own doc comment
	// below). UBI-182 group-container change: a real group snapshot is
	// no longer one file -- manifest.json plus one members/<name>.json
	// per real member, committed as separate files in the real
	// ubx-schema-<type> repo so a reviewer sees which members actually
	// changed on a version bump, not one undifferentiated blob. Path is
	// always a directory now, never a single file.
	Path string

	// FromMirror is true if Path came from UBX_SCHEMA_MIRROR, used as-is
	// with no download or checksum verification -- mirrors
	// AcquireResult.FromMirror's own discipline exactly: a local
	// directory the operator put there is trusted differently than a
	// network download.
	FromMirror bool

	// FromCache is true if Path came from a previously-verified entry in
	// ubx's own cache (~/.ubx/schemas/...). Once verified, always
	// verified: AcquireSchema doesn't re-download or re-verify a cache
	// hit.
	FromCache bool

	// SHA256 is the hex SHA-256 digest of the real, downloaded archive
	// asset (snapshot.tar.gz) -- only ever populated for a fresh
	// download, real and checked, never a mirror or cache hit. A
	// directory of already-extracted files has no one meaningful digest
	// to report for those two cases (empty, not fabricated) -- the real
	// verification already happened, once, the first time this exact
	// version was downloaded; FromCache/FromMirror's own real point is
	// that re-verifying on every subsequent hit is unnecessary.
	SHA256 string
}

type acquireSchemaConfig struct {
	httpClient *http.Client
	apiBase    string
	cacheRoot  string
}

// AcquireSchemaOption configures AcquireSchema. Mirrors AcquireOption's own
// shape: the With* functions below exist for tests (a local httptest
// server and a scratch cache dir) -- real callers don't need any of them.
type AcquireSchemaOption func(*acquireSchemaConfig)

// WithSchemaHTTPClient overrides the http.Client AcquireSchema uses
// (default: http.DefaultClient).
func WithSchemaHTTPClient(c *http.Client) AcquireSchemaOption {
	return func(cfg *acquireSchemaConfig) { cfg.httpClient = c }
}

// WithSchemaAPIBase overrides the GitHub API base URL (default:
// githubAPIBase, api.github.com).
func WithSchemaAPIBase(base string) AcquireSchemaOption {
	return func(cfg *acquireSchemaConfig) { cfg.apiBase = base }
}

// WithSchemaCacheRoot overrides the verified-snapshot cache directory
// (default: ~/.ubx/schemas).
func WithSchemaCacheRoot(dir string) AcquireSchemaOption {
	return func(cfg *acquireSchemaConfig) { cfg.cacheRoot = dir }
}

// schemaMirrorEnv is UBX_SCHEMA_MIRROR's own real name -- mirrors
// providerMirrorEnv (cache.go) exactly, one level up: a local directory to
// check for an already-generated snapshot before ever touching the
// network. A miss is not an error; AcquireSchema falls through to the
// normal cache-then-network path.
const schemaMirrorEnv = "UBX_SCHEMA_MIRROR"

// archiveFilename is the one real release asset every schema-snapshot
// repo's own publish.yml is expected to produce -- fixed, not derived,
// so a release's own asset name and this package's own download logic
// always agree. UBI-182 group-container change: the real, committed
// artifact in the repo is manifest.json plus one members/<name>.json per
// real member (separate files, so a reviewer sees a real diff on every
// version bump, per this design's own reason for committing at all) --
// this archive is how those already-committed files travel as ONE real
// download at release time, not a second, different representation of
// the content.
const archiveFilename = "snapshot.tar.gz"

// checksumsFilename is the release asset carrying archiveFilename's own
// SHA-256 digest, in the identical "<hex digest>  <filename>" per-line
// format sha256sum itself produces -- the SAME real format and the SAME
// real parser (expectedSHA256, verify.go) the OpenTofu registry path
// already uses for SHA256SUMS, reused here verbatim because the file
// shape, not its signer, is what that parser depends on.
const checksumsFilename = "SHA256SUMS"

// manifestFilename is the one real file a valid extracted (or mirrored)
// schema directory is always expected to hold -- what lookupSchemaMirror
// and AcquireSchema's own cache-hit check both look for to decide
// "usable" versus "empty/malformed," the directory analog of the old
// single-file findSingleFile check.
const manifestFilename = "manifest.json"

var (
	// ErrSchemaNotFound means no GitHub release matches the requested
	// provider@version at all (no tag, not a missing asset within an
	// existing tag -- see ErrSchemaAssetMissing for that).
	ErrSchemaNotFound = errors.New("schema snapshot release not found")

	// ErrSchemaAssetMissing means the release exists but is missing
	// snapshot.tar.gz or SHA256SUMS -- a malformed/incomplete publish,
	// not a version that doesn't exist.
	ErrSchemaAssetMissing = errors.New("schema snapshot release is missing a required asset")

	// ErrSchemaChecksumMismatch means the downloaded snapshot.tar.gz's
	// own SHA-256 doesn't match its SHA256SUMS entry -- a corrupted or
	// tampered-in-transit download. See AcquireSchema's own doc comment
	// for exactly what this check does and does not guarantee.
	ErrSchemaChecksumMismatch = errors.New("schema snapshot checksum mismatch")

	// ErrSchemaArchiveUnsafe means snapshot.tar.gz contains an entry
	// this package refuses to extract (an absolute path, a ".." path
	// segment, or a symlink) -- a real, defensive refusal against a
	// malicious or corrupted archive attempting to write outside its own
	// real cache directory, never silently sanitized/renamed.
	ErrSchemaArchiveUnsafe = errors.New("schema snapshot archive contains an unsafe entry")
)

// AcquireSchema ensures a verified schema snapshot for src@version exists
// locally and returns the path to its real, extracted DIRECTORY, ready to
// pass to ubx-provider-dynamic's own internal/snapshot.LoadSplit.
// version must be an explicit, fully-resolved semver string (e.g.
// "1.2.0") -- like Acquire, AcquireSchema never does "latest" resolution:
// a config's pinned version is part of what was reviewed when a proposal
// was accepted, and reproducing a past build means resolving the exact
// same bytes, not whatever a provider happens to publish next.
//
// Resolution order (identical discipline to Acquire, one level up the
// stack -- mirror-first, explicit version only, verify once trust
// forever):
//  1. UBX_SCHEMA_MIRROR, if set: a hit is used directly, unverified (a
//     real directory containing manifest.json). A miss falls through to
//     the next step -- it is not an error.
//  2. ubx's own cache (~/.ubx/schemas/<namespace>/<type>/<version>/): a
//     hit is used directly; it was already verified the first time it was
//     written there.
//  3. Otherwise: fetch the GitHub release tagged "v<version>" from
//     github.com/<namespace>/ubx-schema-<type>, download snapshot.tar.gz
//     and SHA256SUMS, confirm the archive's own SHA-256 matches its
//     SHA256SUMS entry, extract it into the cache, and return the cache
//     directory's own path.
//
// AcquireSchema deliberately never parses or format-checks the snapshot
// it downloads -- that's internal/snapshot.LoadSplit's own real job
// (schema_format range checking included), run by whatever process
// actually loads the directory for serving. AcquireSchema's own contract
// ends at "these files are on disk and the archive they came from matched
// what the release published," exactly where Acquire's own contract ends
// at "this binary is on disk and its checksum matches what the registry
// published."
//
// # What SHA256-against-a-manifest actually guarantees, and what it doesn't
//
// The OpenTofu registry path (Acquire, verify.go) verifies SHA256SUMS
// itself against a GPG signature made with a key the provider's own
// publisher controls, independent of registry.opentofu.org's own hosting
// -- a compromised or malicious registry operator cannot forge a passing
// signature without that separately-held private key. GitHub Releases has
// no equivalent: there is no publisher-held signing key in this design,
// and SHA256SUMS here is just another release asset, served by the same
// GitHub API as snapshot.tar.gz itself, over the same TLS connection,
// with no independent authority attesting to it.
//
// What checking snapshot.tar.gz against SHA256SUMS DOES guarantee:
//   - Transport integrity -- a truncated, corrupted, or bit-flipped
//     download is caught before it's extracted or trusted forever.
//   - Two-asset consistency -- if a CDN edge or mirror ever served a stale
//     or partially-updated copy of one asset but not the other, the
//     mismatch is caught rather than silently cached.
//
// What it does NOT guarantee, unlike the GPG-signed path:
//   - Publisher authenticity independent of GitHub itself. Anyone with
//     push access to the ubx-schema-<type> repo (or anyone who compromises
//     that access) can publish a malicious archive alongside a SHA256SUMS
//     that matches it -- the checksum check passes because it only proves
//     the two assets are internally consistent with each other, not that
//     either one is legitimate.
//   - Protection against a compromised GitHub account or a
//     supply-chain-compromised CI job that publishes the release in the
//     first place.
//
// In short: this is a corruption/tamper-in-transit guard, not a
// publisher-authenticity guard. The real trust boundary for schema
// snapshots is "whoever has push access to github.com/<namespace>/
// ubx-schema-<type>," identical to the trust boundary every GitHub-hosted
// SDK bindings repo (ubx-sdk-*) already operates under with no GPG
// signing either -- this is not a regression relative to that existing,
// accepted pattern, but it is a real, lesser guarantee than the
// GPG-signed OpenTofu provider path, and should not be described as
// equivalent to it.
func AcquireSchema(ctx context.Context, src SchemaSource, version string, opts ...AcquireSchemaOption) (*AcquireSchemaResult, error) {
	cfg := acquireSchemaConfig{
		httpClient: http.DefaultClient,
		apiBase:    githubAPIBase,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.cacheRoot == "" {
		root, err := defaultSchemaCacheRoot()
		if err != nil {
			return nil, fmt.Errorf("acquire schema %s@%s: %w", src, version, err)
		}
		cfg.cacheRoot = root
	}

	if dir := lookupSchemaMirror(src, version); dir != "" {
		return &AcquireSchemaResult{Path: dir, FromMirror: true}, nil
	}

	dir := schemaCacheDir(cfg.cacheRoot, src, version)
	if isSchemaDir(dir) {
		return &AcquireSchemaResult{Path: dir, FromCache: true}, nil
	}

	sum, err := downloadAndVerifySchema(ctx, cfg, src, version, dir)
	if err != nil {
		return nil, fmt.Errorf("acquire schema %s@%s: %w", src, version, err)
	}
	return &AcquireSchemaResult{Path: dir, SHA256: sum}, nil
}

// isSchemaDir reports whether dir looks like a real, usable extracted (or
// mirrored) schema directory -- the directory analog of the old
// findSingleFile's single-file check, deliberately conservative:
// manifest.json's real presence is the one thing both a fresh extraction
// and a hand-populated mirror entry are expected to share.
func isSchemaDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, manifestFilename))
	return err == nil
}

// downloadAndVerifySchema fetches src@version's real GitHub Release,
// downloads snapshot.tar.gz and SHA256SUMS, confirms the archive's own
// SHA-256 matches its SHA256SUMS entry, and extracts it into destDir --
// returning the archive's own real digest (AcquireSchemaResult.SHA256's
// own real, fresh-download-only value).
func downloadAndVerifySchema(ctx context.Context, cfg acquireSchemaConfig, src SchemaSource, version, destDir string) (digest string, err error) {
	rel, err := fetchRelease(ctx, cfg.httpClient, cfg.apiBase, src.Namespace, src.repo(), "v"+version)
	if err != nil {
		return "", err
	}

	archiveAsset, ok := rel.asset(archiveFilename)
	if !ok {
		return "", fmt.Errorf("%w: %s has no %q asset", ErrSchemaAssetMissing, src.repo(), archiveFilename)
	}
	sumsAsset, ok := rel.asset(checksumsFilename)
	if !ok {
		return "", fmt.Errorf("%w: %s has no %q asset", ErrSchemaAssetMissing, src.repo(), checksumsFilename)
	}

	sumsContent, err := httpGetBytes(ctx, cfg.httpClient, sumsAsset.BrowserDownloadURL)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", checksumsFilename, err)
	}
	want, err := expectedSHA256(sumsContent, archiveFilename)
	if err != nil {
		return "", err
	}

	archiveBytes, err := httpGetBytes(ctx, cfg.httpClient, archiveAsset.BrowserDownloadURL)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", archiveFilename, err)
	}
	got := sha256HexOf(archiveBytes)
	if got != want {
		return "", fmt.Errorf("%w: %s: SHA256SUMS says %s, downloaded archive is %s",
			ErrSchemaChecksumMismatch, archiveFilename, want, got)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	if err := extractTarGz(archiveBytes, destDir); err != nil {
		return "", fmt.Errorf("extract %s: %w", archiveFilename, err)
	}
	if !isSchemaDir(destDir) {
		return "", fmt.Errorf("%w: %s extracted with no real %s at its root", ErrSchemaAssetMissing, archiveFilename, manifestFilename)
	}
	return got, nil
}

// extractTarGz writes data (a real gzip-compressed tar archive) into
// destDir, refusing loudly -- ErrSchemaArchiveUnsafe, never a silent
// sanitize/rename -- on any entry this package won't safely extract: an
// absolute path, a path containing a ".." segment (the standard
// zip-slip/tar-slip escape a malicious or corrupted archive could use to
// write outside destDir), or a symlink (no real snapshot content needs
// one, and a symlink target is one more real way to escape destDir).
// Directory entries are created as needed; regular files are written
// with real, fixed 0o644 permissions regardless of what the archive
// itself claims -- this package's own artifact, this package's own
// permission policy.
func extractTarGz(data []byte, destDir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		cleaned := filepath.Clean(hdr.Name)
		if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: entry %q escapes the archive root", ErrSchemaArchiveUnsafe, hdr.Name)
		}
		target := filepath.Join(destDir, cleaned)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: entry %q has unsupported type %v (only regular files and directories are real snapshot content)", ErrSchemaArchiveUnsafe, hdr.Name, hdr.Typeflag)
		}
	}
}

// defaultSchemaCacheRoot is ~/.ubx/schemas -- the on-disk cache for
// verified schema snapshots, a sibling of Acquire's own
// ~/.ubx/providers.
func defaultSchemaCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ubx", "schemas"), nil
}

// schemaCacheDir is <cacheRoot>/<namespace>/<type>/<version>/ -- no
// os_arch dimension, unlike cacheDir: a schema snapshot's own RawSpec is
// platform-independent (internal/snapshot's own doc comment covers why),
// so one cached copy per version serves every platform.
func schemaCacheDir(cacheRoot string, src SchemaSource, version string) string {
	return filepath.Join(cacheRoot, src.Namespace, src.Type, version)
}

// lookupSchemaMirror returns the path to a real, usable schema DIRECTORY
// already present in the local mirror directory named by
// UBX_SCHEMA_MIRROR, or "" if the env var isn't set or nothing usable is
// there (isSchemaDir's own manifest.json check, the directory analog of
// the old single-file findSingleFile lookup this replaced).
func lookupSchemaMirror(src SchemaSource, version string) string {
	mirrorDir := os.Getenv(schemaMirrorEnv)
	if mirrorDir == "" {
		return ""
	}
	dir := filepath.Join(mirrorDir, src.Namespace, src.Type, version)
	if !isSchemaDir(dir) {
		return ""
	}
	return dir
}
