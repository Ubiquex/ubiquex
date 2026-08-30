// UBI-102: resolves a [dynamic_providers.<name>.descriptions] pin
// (source+version, the identical pinned shape schemaProvenanceFields'
// own pinnedSchemaFields already established for schema sources) into
// an effective --descriptions-dir, via provider.AcquireDescriptions --
// the replacement for a provider's own local, checked-in
// sdk/providers/descriptions/<name>.json file, which no longer needs
// to exist once every real provider carries this pin.
//
// Deliberately reshapes the pinned corpus's own real flat shape
// ({key: {source, text}}, resource.field.path or data_resource.field.path
// keys -- ubiquex-docs' own established shape, the richer of the two
// pre-migration copies, kept as the corpus's own real published format)
// into loadCheckedInDescriptions' own existing nested shape
// ({resource: {field.path: text}}) rather than changing that function
// at all -- every other real caller of it is unaffected, and this is
// the smaller, lower-risk change of the two.
//
// Only resource-keyed entries (no "data_" prefix) are included --
// matching exactly what a provider's own pre-migration
// sdk/providers/descriptions/<name>.json ever held (confirmed live,
// UBI-102's own datadog pilot: 0 data-source-keyed entries in that
// file, 0 real "(AI-inferred)" markers in any published data-source
// binding). Including data-source entries here would be a real,
// unintended behavior CHANGE (codegen suddenly enriching data sources
// it never enriched before), not a relocation -- exactly what the
// pilot's own byte-identical check exists to catch.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ubiquex/ubiquex/provider"
)

// descriptionsPinFields mirrors pinnedSchemaFields' own real shape
// exactly, one level down: params["descriptions"] is itself a
// map[string]any (a nested TOML table,
// [dynamic_providers.<name>.descriptions]) carrying source+version.
// Absent entirely is the common, honest case (a provider not yet
// migrated off its own local checked-in file) -- ok=false, no error.
func descriptionsPinFields(params map[string]any) (source, version string, ok bool, err error) {
	raw, has := params["descriptions"]
	if !has {
		return "", "", false, nil
	}
	sub, isMap := raw.(map[string]any)
	if !isMap {
		return "", "", false, fmt.Errorf("\"descriptions\" must be a table, got %T", raw)
	}
	rawSource, hasSource := sub["source"]
	if !hasSource {
		return "", "", false, fmt.Errorf("\"descriptions\" declares no \"source\"")
	}
	source, isString := rawSource.(string)
	if !isString {
		return "", "", false, fmt.Errorf("\"descriptions.source\" must be a string, got %T", rawSource)
	}
	rawVersion, hasVersion := sub["version"]
	if !hasVersion {
		return "", "", false, fmt.Errorf("%q declares \"descriptions.source\" but no \"descriptions.version\"", source)
	}
	version, isString = rawVersion.(string)
	if !isString {
		return "", "", false, fmt.Errorf("\"descriptions.version\" must be a string, got %T", rawVersion)
	}
	return source, version, true, nil
}

// rawDescriptionsEntry is the pinned corpus's own real per-key shape.
type rawDescriptionsEntry struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}

// resolveDescriptionsDir returns fallbackDir unchanged when providerType
// has no [dynamic_providers.<name>.descriptions] pin -- the honest,
// common state for any provider not yet migrated. When a pin exists,
// acquires it, reshapes it into loadCheckedInDescriptions' own expected
// nested file, writes it to a fresh temp directory, and returns that
// directory instead.
func resolveDescriptionsDir(ctx context.Context, params map[string]any, providerType, fallbackDir string) (string, error) {
	rawSource, version, ok, err := descriptionsPinFields(params)
	if err != nil {
		return "", fmt.Errorf("dynamic provider %q: descriptions provenance: %w", providerType, err)
	}
	if !ok {
		return fallbackDir, nil
	}

	src, err := provider.ParseDescriptionSource(rawSource)
	if err != nil {
		return "", fmt.Errorf("dynamic provider %q: descriptions source %q: %w", providerType, rawSource, err)
	}
	// providerType (e.g. "datadog") is the real key into the pinned
	// corpus's own release tag (descriptions-<providerType>-v<version>)
	// -- ParseDescriptionSource's own Type field (parsed from rawSource,
	// e.g. "datadog" out of "ubiquex/datadog") must agree; a mismatch
	// here means the config entry names the wrong pin for its own
	// table, a real config error, not something to silently paper over.
	if src.Type != providerType {
		return "", fmt.Errorf("dynamic provider %q: descriptions source %q names provider %q, not %q", providerType, rawSource, src.Type, providerType)
	}

	res, err := provider.AcquireDescriptions(ctx, src, version)
	if err != nil {
		return "", fmt.Errorf("dynamic provider %q: acquire descriptions %s@%s: %w", providerType, rawSource, version, err)
	}

	flatPath := filepath.Join(res.Path, providerType+".json")
	flatBytes, err := os.ReadFile(flatPath)
	if err != nil {
		return "", fmt.Errorf("dynamic provider %q: read acquired descriptions: %w", providerType, err)
	}
	var flat map[string]rawDescriptionsEntry
	if err := json.Unmarshal(flatBytes, &flat); err != nil {
		return "", fmt.Errorf("dynamic provider %q: parse acquired descriptions: %w", providerType, err)
	}

	nested := make(map[string]map[string]string)
	for key, entry := range flat {
		if strings.HasPrefix(key, "data_") {
			continue
		}
		resource, relPath, found := strings.Cut(key, ".")
		if !found {
			continue
		}
		if nested[resource] == nil {
			nested[resource] = make(map[string]string)
		}
		nested[resource][relPath] = entry.Text
	}

	dir, err := os.MkdirTemp("", "ubx-sdk-gen-descriptions-"+providerType)
	if err != nil {
		return "", fmt.Errorf("dynamic provider %q: %w", providerType, err)
	}
	nestedBytes, err := json.Marshal(nested)
	if err != nil {
		return "", fmt.Errorf("dynamic provider %q: %w", providerType, err)
	}
	if err := os.WriteFile(filepath.Join(dir, providerType+".json"), nestedBytes, 0o644); err != nil {
		return "", fmt.Errorf("dynamic provider %q: %w", providerType, err)
	}
	return dir, nil
}
