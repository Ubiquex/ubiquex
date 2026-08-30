package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// UBI-102: the pinnable distribution mechanism for a provider's own raw
// description corpus (AI-authored field descriptions that fill real
// gaps left by native vendor schema text) -- the replacement for the
// two independently-maintained copies (ubiquex's own
// sdk/providers/descriptions/<provider>.json, ubiquex-docs' own
// artifacts/<provider>/descriptions.json) whose drift caused a real
// 49,479-description gap (descriptions authored in one, never reaching
// the other).
//
// Deliberately a separate type from SchemaSource, not a reuse with a
// flag -- same "separate type, not a reuse" precedent SchemaSource's
// own doc comment already established relative to Source: a
// description corpus and a schema snapshot are distributed differently
// enough (fixed repo name here, one repo hosting every provider's own
// corpus, versus one repo per provider derived from Type there) that
// conflating them would make one or the other's own address shape lie
// about what it really resolves.
//
// Real, deliberate reuse, not duplicated: fetchRelease
// (githubrelease.go), expectedSHA256/sha256HexOf (verify.go), and
// httpGetBytes (registry.go) are already generic, SchemaSource-free
// helpers -- this file adds only what's genuinely new (the address
// shape, the cache layout, the "does this look like a real, extracted
// descriptions directory" check). extractTarGz (acquireschema.go) is
// reused as-is; it already takes plain (data []byte, destDir string),
// nothing SchemaSource-specific about it either.

// descriptionsRepo is the one real GitHub repo every provider's own raw
// description corpus is published from -- unlike SchemaSource's own
// per-provider "ubx-schema-<type>" derivation, there is exactly one
// repo here, because the corpus's own real home (UBI-102's own design
// decision) is ubiquex-docs itself: descriptions are authored as part
// of that repo's own docs-artifact workflow, and a tagged release
// there costs nothing new regardless of how many other pages live
// alongside it (a GitHub Release's own asset list is whatever this
// package's own publish workflow explicitly builds, never the whole
// repo).
const descriptionsRepo = "ubiquex-docs"

// descriptionsArchiveFilename/descriptionsChecksumsFilename mirror
// archiveFilename/checksumsFilename's own real shape (acquireschema.go)
// -- same two-asset convention, same real reason (one real download
// regardless of corpus size, a checksum covering the archive itself).
const (
	descriptionsArchiveFilename   = "snapshot.tar.gz"
	descriptionsChecksumsFilename = "SHA256SUMS"
)

// DescriptionSource identifies a provider's own raw description
// corpus by Hostname/Namespace/Type -- Type is the provider name
// (e.g. "datadog"), NOT used to derive the repo name (unlike
// SchemaSource.repo()): the repo is always descriptionsRepo, and Type
// instead feeds the release's own namespaced tag
// ("descriptions-<type>-v<version>"), since one repo hosts every
// provider's own corpus.
type DescriptionSource struct {
	Hostname  string
	Namespace string
	Type      string
}

func (s DescriptionSource) String() string {
	return s.Hostname + "/" + s.Namespace + "/" + descriptionsRepo + "/" + s.Type
}

func (s DescriptionSource) tag(version string) string {
	return "descriptions-" + s.Type + "-v" + version
}

// ParseDescriptionSource parses a config's own descriptions pin. Both
// "ubiquex/datadog" (shorthand, hostname defaults to defaultSchemaHost)
// and "github.com/ubiquex/datadog" (fully qualified) are accepted,
// mirroring ParseSchemaSource's own real two-part/three-part shape.
func ParseDescriptionSource(s string) (DescriptionSource, error) {
	src, err := ParseSchemaSource(s)
	if err != nil {
		return DescriptionSource{}, err
	}
	return DescriptionSource{Hostname: src.Hostname, Namespace: src.Namespace, Type: src.Type}, nil
}

// AcquireDescriptionsResult is AcquireSchemaResult's own real analog --
// Path is the local directory containing exactly one real, extracted
// <type>.json (the raw, qualifier-free, flat {key: {source, text}}
// corpus this provider's own docs and codegen paths both read).
type AcquireDescriptionsResult struct {
	Path       string
	FromMirror bool
	FromCache  bool
	SHA256     string
}

type acquireDescriptionsConfig struct {
	httpClient *http.Client
	apiBase    string
	cacheRoot  string
}

// AcquireDescriptionsOption mirrors AcquireSchemaOption's own real shape.
type AcquireDescriptionsOption func(*acquireDescriptionsConfig)

// descriptionsMirrorEnv mirrors schemaMirrorEnv's own real purpose --
// a local directory an operator already populated, trusted as-is, no
// download or checksum verification.
const descriptionsMirrorEnv = "UBX_DESCRIPTIONS_MIRROR"

// AcquireDescriptions resolves src@version to a local, verified
// directory containing <type>.json -- same real
// download-once/cache-forever/mirror-override discipline
// AcquireSchema already established, deliberately not shared code (see
// this file's own top doc comment) since the two address shapes
// genuinely differ.
func AcquireDescriptions(ctx context.Context, src DescriptionSource, version string, opts ...AcquireDescriptionsOption) (*AcquireDescriptionsResult, error) {
	cfg := acquireDescriptionsConfig{
		httpClient: http.DefaultClient,
		apiBase:    githubAPIBase,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.cacheRoot == "" {
		root, err := defaultDescriptionsCacheRoot()
		if err != nil {
			return nil, fmt.Errorf("acquire descriptions %s@%s: %w", src, version, err)
		}
		cfg.cacheRoot = root
	}

	if dir := lookupDescriptionsMirror(src, version); dir != "" {
		return &AcquireDescriptionsResult{Path: dir, FromMirror: true}, nil
	}

	dir := descriptionsCacheDir(cfg.cacheRoot, src, version)
	if isDescriptionsDir(dir, src.Type) {
		return &AcquireDescriptionsResult{Path: dir, FromCache: true}, nil
	}

	sum, err := downloadAndVerifyDescriptions(ctx, cfg, src, version, dir)
	if err != nil {
		return nil, fmt.Errorf("acquire descriptions %s@%s: %w", src, version, err)
	}
	return &AcquireDescriptionsResult{Path: dir, SHA256: sum}, nil
}

func isDescriptionsDir(dir, providerType string) bool {
	_, err := os.Stat(filepath.Join(dir, providerType+".json"))
	return err == nil
}

func downloadAndVerifyDescriptions(ctx context.Context, cfg acquireDescriptionsConfig, src DescriptionSource, version, destDir string) (digest string, err error) {
	rel, err := fetchRelease(ctx, cfg.httpClient, cfg.apiBase, src.Namespace, descriptionsRepo, src.tag(version))
	if err != nil {
		return "", err
	}

	archiveAsset, ok := rel.asset(descriptionsArchiveFilename)
	if !ok {
		return "", fmt.Errorf("%w: %s has no %q asset", ErrSchemaAssetMissing, descriptionsRepo, descriptionsArchiveFilename)
	}
	sumsAsset, ok := rel.asset(descriptionsChecksumsFilename)
	if !ok {
		return "", fmt.Errorf("%w: %s has no %q asset", ErrSchemaAssetMissing, descriptionsRepo, descriptionsChecksumsFilename)
	}

	sumsContent, err := httpGetBytes(ctx, cfg.httpClient, sumsAsset.BrowserDownloadURL)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", descriptionsChecksumsFilename, err)
	}
	want, err := expectedSHA256(sumsContent, descriptionsArchiveFilename)
	if err != nil {
		return "", err
	}

	archiveBytes, err := httpGetBytes(ctx, cfg.httpClient, archiveAsset.BrowserDownloadURL)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", descriptionsArchiveFilename, err)
	}
	got := sha256HexOf(archiveBytes)
	if got != want {
		return "", fmt.Errorf("%w: %s: SHA256SUMS says %s, downloaded archive is %s",
			ErrSchemaChecksumMismatch, descriptionsArchiveFilename, want, got)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	if err := extractTarGz(archiveBytes, destDir); err != nil {
		return "", fmt.Errorf("extract %s: %w", descriptionsArchiveFilename, err)
	}
	if !isDescriptionsDir(destDir, src.Type) {
		return "", fmt.Errorf("%w: %s extracted with no real %s.json at its root", ErrSchemaAssetMissing, descriptionsArchiveFilename, src.Type)
	}
	return got, nil
}

func defaultDescriptionsCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ubx", "descriptions"), nil
}

func descriptionsCacheDir(cacheRoot string, src DescriptionSource, version string) string {
	return filepath.Join(cacheRoot, src.Namespace, src.Type, version)
}

func lookupDescriptionsMirror(src DescriptionSource, version string) string {
	mirrorDir := os.Getenv(descriptionsMirrorEnv)
	if mirrorDir == "" {
		return ""
	}
	dir := filepath.Join(mirrorDir, src.Namespace, src.Type, version)
	if !isDescriptionsDir(dir, src.Type) {
		return ""
	}
	return dir
}
