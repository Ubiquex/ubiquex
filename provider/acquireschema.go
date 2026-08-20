package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// AcquireSchemaResult is what AcquireSchema found or produced -- the
// schema-snapshot analog of AcquireResult.
type AcquireSchemaResult struct {
	// Path is the local filesystem path to the ready-to-load snapshot
	// file -- pass it to internal/snapshot.Load (ubx-provider-dynamic's
	// own package; AcquireSchema deliberately never parses the snapshot
	// itself, see this file's own doc comment below).
	Path string

	// FromMirror is true if Path came from UBX_SCHEMA_MIRROR, used as-is
	// with no download or checksum verification -- mirrors
	// AcquireResult.FromMirror's own discipline exactly: a local file the
	// operator put there is trusted differently than a network download.
	FromMirror bool

	// FromCache is true if Path came from a previously-verified entry in
	// ubx's own cache (~/.ubx/schemas/...). Once verified, always
	// verified: AcquireSchema doesn't re-download or re-verify a cache
	// hit.
	FromCache bool

	// SHA256 is the hex SHA-256 digest of the file at Path -- always
	// computed, even for a mirror hit.
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

// snapshotFilename is the one real file every mirror/cache/release
// directory this package writes or reads is expected to hold -- fixed,
// not derived, so a release's own asset name and a cache hit's own
// filename always agree.
const snapshotFilename = "snapshot.json"

// checksumsFilename is the release asset carrying snapshot.json's own
// SHA-256 digest, in the identical "<hex digest>  <filename>" per-line
// format sha256sum itself produces -- the SAME real format and the SAME
// real parser (expectedSHA256, verify.go) the OpenTofu registry path
// already uses for SHA256SUMS, reused here verbatim because the file
// shape, not its signer, is what that parser depends on.
const checksumsFilename = "SHA256SUMS"

var (
	// ErrSchemaNotFound means no GitHub release matches the requested
	// provider@version at all (no tag, not a missing asset within an
	// existing tag -- see ErrSchemaAssetMissing for that).
	ErrSchemaNotFound = errors.New("schema snapshot release not found")

	// ErrSchemaAssetMissing means the release exists but is missing
	// snapshot.json or SHA256SUMS -- a malformed/incomplete publish, not
	// a version that doesn't exist.
	ErrSchemaAssetMissing = errors.New("schema snapshot release is missing a required asset")

	// ErrSchemaChecksumMismatch means the downloaded snapshot.json's own
	// SHA-256 doesn't match its SHA256SUMS entry -- a corrupted or
	// tampered-in-transit download. See AcquireSchema's own doc comment
	// for exactly what this check does and does not guarantee.
	ErrSchemaChecksumMismatch = errors.New("schema snapshot checksum mismatch")
)

// AcquireSchema ensures a verified schema snapshot for src@version exists
// locally and returns its path, ready to pass to
// ubx-provider-dynamic's own internal/snapshot.Load. version must be an
// explicit, fully-resolved semver string (e.g. "1.2.0") -- like Acquire,
// AcquireSchema never does "latest" resolution: a config's pinned version
// is part of what was reviewed when a proposal was accepted, and
// reproducing a past build means resolving the exact same bytes, not
// whatever a provider happens to publish next.
//
// Resolution order (identical discipline to Acquire, one level up the
// stack -- mirror-first, explicit version only, verify once trust
// forever):
//  1. UBX_SCHEMA_MIRROR, if set: a hit is used directly, unverified. A
//     miss falls through to the next step -- it is not an error.
//  2. ubx's own cache (~/.ubx/schemas/<namespace>/<type>/<version>/): a
//     hit is used directly; it was already verified the first time it was
//     written there.
//  3. Otherwise: fetch the GitHub release tagged "v<version>" from
//     github.com/<namespace>/ubx-schema-<type>, download snapshot.json and
//     SHA256SUMS, confirm the snapshot's own SHA-256 matches its
//     SHA256SUMS entry, write it into the cache, and return its path.
//
// AcquireSchema deliberately never parses or format-checks the snapshot
// it downloads -- that's internal/snapshot.Load's own real job (schema_format
// range checking included), run by whatever process actually loads the
// file for serving. AcquireSchema's own contract ends at "these bytes are
// on disk and their checksum matches what the release published," exactly
// where Acquire's own contract ends at "this binary is on disk and its
// checksum matches what the registry published."
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
// GitHub API as snapshot.json itself, over the same TLS connection, with
// no independent authority attesting to it.
//
// What checking snapshot.json against SHA256SUMS DOES guarantee:
//   - Transport integrity -- a truncated, corrupted, or bit-flipped
//     download is caught before it's written to the cache or trusted
//     forever.
//   - Two-asset consistency -- if a CDN edge or mirror ever served a stale
//     or partially-updated copy of one asset but not the other, the
//     mismatch is caught rather than silently cached.
//
// What it does NOT guarantee, unlike the GPG-signed path:
//   - Publisher authenticity independent of GitHub itself. Anyone with
//     push access to the ubx-schema-<type> repo (or anyone who compromises
//     that access) can publish a malicious snapshot.json alongside a
//     SHA256SUMS that matches it -- the checksum check passes because it
//     only proves the two files are internally consistent with each
//     other, not that either one is legitimate.
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

	if path := lookupSchemaMirror(src, version); path != "" {
		sum, err := sha256File(path)
		if err != nil {
			return nil, fmt.Errorf("acquire schema %s@%s: mirror: %w", src, version, err)
		}
		return &AcquireSchemaResult{Path: path, FromMirror: true, SHA256: sum}, nil
	}

	dir := schemaCacheDir(cfg.cacheRoot, src, version)
	if path := findSingleFile(dir); path != "" {
		sum, err := sha256File(path)
		if err != nil {
			return nil, fmt.Errorf("acquire schema %s@%s: cache: %w", src, version, err)
		}
		return &AcquireSchemaResult{Path: path, FromCache: true, SHA256: sum}, nil
	}

	path, sum, err := downloadAndVerifySchema(ctx, cfg, src, version, dir)
	if err != nil {
		return nil, fmt.Errorf("acquire schema %s@%s: %w", src, version, err)
	}
	return &AcquireSchemaResult{Path: path, SHA256: sum}, nil
}

func downloadAndVerifySchema(ctx context.Context, cfg acquireSchemaConfig, src SchemaSource, version, destDir string) (path, digest string, err error) {
	rel, err := fetchRelease(ctx, cfg.httpClient, cfg.apiBase, src.Namespace, src.repo(), "v"+version)
	if err != nil {
		return "", "", err
	}

	snapAsset, ok := rel.asset(snapshotFilename)
	if !ok {
		return "", "", fmt.Errorf("%w: %s has no %q asset", ErrSchemaAssetMissing, src.repo(), snapshotFilename)
	}
	sumsAsset, ok := rel.asset(checksumsFilename)
	if !ok {
		return "", "", fmt.Errorf("%w: %s has no %q asset", ErrSchemaAssetMissing, src.repo(), checksumsFilename)
	}

	sumsContent, err := httpGetBytes(ctx, cfg.httpClient, sumsAsset.BrowserDownloadURL)
	if err != nil {
		return "", "", fmt.Errorf("download %s: %w", checksumsFilename, err)
	}
	want, err := expectedSHA256(sumsContent, snapshotFilename)
	if err != nil {
		return "", "", err
	}

	snapBytes, err := httpGetBytes(ctx, cfg.httpClient, snapAsset.BrowserDownloadURL)
	if err != nil {
		return "", "", fmt.Errorf("download %s: %w", snapshotFilename, err)
	}
	got := sha256HexOf(snapBytes)
	if got != want {
		return "", "", fmt.Errorf("%w: %s: SHA256SUMS says %s, downloaded snapshot is %s",
			ErrSchemaChecksumMismatch, snapshotFilename, want, got)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", "", err
	}
	outPath := filepath.Join(destDir, snapshotFilename)
	if err := os.WriteFile(outPath, snapBytes, 0o644); err != nil {
		return "", "", err
	}
	return outPath, got, nil
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

// lookupSchemaMirror returns the path to a snapshot already present in
// the local mirror directory named by UBX_SCHEMA_MIRROR, or "" if the env
// var isn't set or nothing usable is there.
func lookupSchemaMirror(src SchemaSource, version string) string {
	mirrorDir := os.Getenv(schemaMirrorEnv)
	if mirrorDir == "" {
		return ""
	}
	return findSingleFile(filepath.Join(mirrorDir, src.Namespace, src.Type, version))
}
