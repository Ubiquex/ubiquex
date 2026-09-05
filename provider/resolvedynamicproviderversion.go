package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// snapshotManifestFields is the minimal, real subset of manifest.json
// (ubx-provider-dynamic's own internal/snapshot package -- SaveSplit's
// own real, committed shape) this package ever needs to read directly.
// ubiquex deliberately never imports ubx-provider-dynamic's own Go
// packages (a separate module, launched only as a real subprocess over
// tfplugin) -- this is a small, independent, intentionally-minimal
// decode of the two real fields UBI-194's own version resolution needs,
// not a shared type kept in lockstep with that other repo's own struct.
type snapshotManifestFields struct {
	SchemaFormat int    `json:"schema_format"`
	Provider     string `json:"provider"`
	Version      string `json:"version"`

	// GeneratedByBinaryVersion is the post-UBI-249 name for what used to
	// be min_binary_version. The old name promised a floor the field has
	// never computed: it records which ubx-provider-dynamic binary CUT
	// the snapshot, which is a version known to work, not the lowest one
	// that would. The rename changed nothing about resolution.
	GeneratedByBinaryVersion string `json:"generated_by_binary_version"`

	// LegacyMinBinaryVersion is the pre-rename spelling, still read
	// because every ubx-schema-<provider> snapshot published before the
	// rename carries only this key -- confirmed against all eight real
	// published manifests, not assumed. Dropping this read would make
	// each of them resolve as absent and silently fall through to the
	// SchemaFormat bootstrap table below, downgrading cloudflare's real
	// 1.0.10 to 1.0.0 while logging a "published before UBI-194" line
	// that is not true of them.
	LegacyMinBinaryVersion string `json:"min_binary_version"`
}

// generatedByBinaryVersion prefers the current name and falls back to
// the pre-rename one, so an already-published snapshot and a freshly
// cut one both resolve identically.
func (m snapshotManifestFields) generatedByBinaryVersion() string {
	if m.GeneratedByBinaryVersion != "" {
		return m.GeneratedByBinaryVersion
	}
	return m.LegacyMinBinaryVersion
}

func readSnapshotManifest(schemaDir string) (snapshotManifestFields, error) {
	data, err := os.ReadFile(filepath.Join(schemaDir, manifestFilename))
	if err != nil {
		return snapshotManifestFields{}, err
	}
	var m snapshotManifestFields
	if err := json.Unmarshal(data, &m); err != nil {
		return snapshotManifestFields{}, fmt.Errorf("parse %s: %w", manifestFilename, err)
	}
	return m, nil
}

// dynamicProviderBinaryBootstrapVersions is UBI-194's own real,
// EXPLICIT, TEMPORARY bootstrap fallback -- used ONLY when a real
// snapshot's own manifest.json has no real GeneratedByBinaryVersion at all
// (every one of the six real provider snapshots published before that
// field existed: kubernetes, datadog, github, google, aws, azure). This
// is deliberately NOT the primary resolution mechanism -- that's the
// snapshot's own exact, generation-time-stamped GeneratedByBinaryVersion
// (internal/snapshot's own doc comment in ubx-provider-dynamic has the
// full real reasoning: a hand-maintained table keyed by schema_format
// cannot distinguish a pre-fix from a post-fix snapshot declaring the
// identical schema_format, the exact real gap AWS's own mixed-source
// case exposed). This map exists ONLY to give the six already-published
// releases something to resolve against until each one's own next real
// regeneration stamps a real GeneratedByBinaryVersion instead -- every real
// hash-watch.yml in this org already builds ubx-provider-dynamic fresh
// on every real run, so this self-heals per provider automatically, not
// something anyone needs to remember to clean up as a separate task.
//
// A provider's own real entry should be REMOVED from this map, not
// bumped, once that provider's own real release carries a real
// GeneratedByBinaryVersion -- keeping a stale entry around after that point
// would silently reintroduce the identical failure mode this whole
// design exists to avoid (an entry here always wins over an absent
// field, never over a REAL, present one, so a stale entry is inert
// once the real field exists -- but an inert, unremoved entry is still
// a real, silent trap for the next person who doesn't know it's dead).
var dynamicProviderBinaryBootstrapVersions = map[int]string{
	3: "1.0.0",
}

// ResolveDynamicProviderBinaryVersion decides which real
// ubx-provider-dynamic version to acquire for a real, already-acquired
// schema snapshot at schemaDir (an AcquireSchemaResult.Path). Reads the
// snapshot's own real GeneratedByBinaryVersion first (falling back to
// the pre-rename min_binary_version key) -- UBI-194's own real,
// primary, exact mechanism, needing no table at all. Falls back to
// dynamicProviderBinaryBootstrapVersions, keyed by the snapshot's own
// real SchemaFormat, ONLY when that field is genuinely absent (a real,
// explicit, LOGGED bootstrap case, never a silent, permanent second
// resolution mode -- the identical shape UBI-182 Stage E's own
// [providers.<name>] dual-meaning collapse just eliminated one layer
// up, deliberately not repeated here). The log line names the real
// provider and version every time the fallback fires, so which of the
// six real releases still need to regenerate past it stays visible,
// not something only discoverable by reading this file's own source.
func ResolveDynamicProviderBinaryVersion(schemaDir string) (string, error) {
	man, err := readSnapshotManifest(schemaDir)
	if err != nil {
		return "", fmt.Errorf("read snapshot manifest at %s: %w", schemaDir, err)
	}
	if v := man.generatedByBinaryVersion(); v != "" {
		return v, nil
	}

	fallback, ok := dynamicProviderBinaryBootstrapVersions[man.SchemaFormat]
	if !ok {
		return "", fmt.Errorf("%w: %s@%s (schema_format %d) has no real generated_by_binary_version (nor the pre-rename min_binary_version) and no bootstrap fallback is registered for that schema_format -- regenerate and republish this snapshot with a real ubx-provider-dynamic release",
			ErrDynamicProviderBinaryAssetMissing, man.Provider, man.Version, man.SchemaFormat)
	}
	fmt.Fprintf(os.Stderr, "ubx: %s@%s has no real generated_by_binary_version or min_binary_version (published before UBI-194) -- falling back to bootstrap ubx-provider-dynamic version %s for schema_format %d; this fallback is removed automatically once %s regenerates and republishes past it\n",
		man.Provider, man.Version, fallback, man.SchemaFormat, man.Provider)
	return fallback, nil
}
