package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// genericTree is the one shared shape every format's own parser produces
// (UBI-32 Arc A, docs/architecture.md — "Config: cascading, per-key,
// child overrides parent"): nested tables are nested genericTrees,
// recursively, all the way down. The cascade merge and provenance
// tracking below operate ONLY on this shape -- neither has ever heard of
// TOML, HCL, or YAML specifically, which is what makes the same merge
// logic correct across a cascade chain mixing all three.
type genericTree = map[string]any

// Provenance maps a dotted config key path (e.g. "stack",
// `provider.source`, `providers."hashicorp/aws"`,
// `provider_configs."hashicorp/aws".region`) to the absolute path of the
// file that supplied that key's effective, post-cascade value.
type Provenance map[string]string

// ResolvedConfig is LoadConfig's richer sibling: the merged Config,
// which files contributed (nearest first), and -- per key -- both the
// effective value and its provenance. Most callers only need LoadConfig;
// the provenance view (`ubx config`) needs to explain itself, so it
// needs this instead.
type ResolvedConfig struct {
	Config     *Config
	Provenance Provenance
	// Values holds the same keys as Provenance, each mapped to its own
	// effective (post-cascade) leaf value -- kept alongside Provenance
	// rather than requiring a caller to re-derive "what value lives at
	// this path" by parsing the path back apart.
	Values map[string]any
	// Files lists every file that contributed to the merge, nearest
	// first -- the same order discoverCascadeFiles returns.
	Files []string
}

// configFileCandidates is the per-directory discovery order (UBI-32 Arc
// A): first found wins for that directory; formats never merge within
// one directory. `config` (no extension) is UBI-19's original legacy
// name, kept in its original priority position forever.
var configFileCandidates = []string{"config.hcl", "config.toml", "config", "config.yaml"}

// LoadConfigResolved discovers the full .ubx/config cascade -- every
// `.ubx/config*` from configSearchStartDir() up to the filesystem root,
// one file per directory chosen by configFileCandidates' own discovery
// order -- parses each into a genericTree, folds them root-to-nearest
// (so a nearer directory's own value for any given key always wins,
// recorded in Provenance), and decodes the single merged tree into
// *Config. Returns a zero Config (not an error) if no config file exists
// anywhere in the walk.
func LoadConfigResolved(warnOut io.Writer) (*ResolvedConfig, error) {
	dir, err := configSearchStartDir()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	files := discoverCascadeFiles(dir)
	if len(files) == 0 {
		return &ResolvedConfig{Config: &Config{}, Provenance: Provenance{}, Values: map[string]any{}}, nil
	}

	merged := genericTree{}
	prov := Provenance{}
	values := map[string]any{}
	// Fold root-to-nearest (files is nearest-first) so a nearer
	// directory's own value always overwrites a farther one's, per key.
	for i := len(files) - 1; i >= 0; i-- {
		file := files[i]
		layer, err := parseGenericFile(file)
		if err != nil {
			return nil, fmt.Errorf("load config: parse %s: %w", file, err)
		}
		warnUnknownKeys(layer, file, warnOut)
		merged = mergeGeneric(merged, layer, "", file, prov, values)
	}

	cfg, err := decodeGenericIntoConfig(merged)
	if err != nil {
		return nil, fmt.Errorf("load config: decode merged config: %w", err)
	}
	return &ResolvedConfig{Config: cfg, Provenance: prov, Values: values, Files: files}, nil
}

// discoverCascadeFiles walks from startDir upward through every parent
// directory to the filesystem root, returning one file per directory
// that has one at all (configFileCandidates' own discovery order),
// nearest directory first.
func discoverCascadeFiles(startDir string) []string {
	var files []string
	dir := startDir
	for {
		if f := pickConfigFile(dir); f != "" {
			files = append(files, f)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return files
		}
		dir = parent
	}
}

// pickConfigFile returns the one config file dir's own `.ubx/` holds,
// per configFileCandidates' discovery order, or "" if it holds none.
// A directory holding more than one candidate (e.g. a leftover from
// switching formats) never merges them -- the first found wins outright,
// the rest are silently unused.
func pickConfigFile(dir string) string {
	for _, name := range configFileCandidates {
		candidate := filepath.Join(dir, ".ubx", name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// parseGenericFile dispatches to the right format parser by extension.
// The legacy extensionless `config` name is TOML, matching UBI-19's
// original format.
func parseGenericFile(path string) (genericTree, error) {
	switch filepath.Ext(path) {
	case ".hcl":
		return parseHCLGeneric(path)
	case ".yaml", ".yml":
		return parseYAMLGeneric(path)
	default: // ".toml", "" (legacy `config`)
		return parseTOMLGeneric(path)
	}
}

// decodeGenericIntoConfig is the one shared decode step for all three
// formats: JSON-marshal the merged generic tree, then JSON-unmarshal it
// into Config (whose struct tags now carry matching `json` names
// alongside the pre-existing `toml` ones -- see config.go). Chosen over
// three separate format-specific struct decoders specifically so the
// cascade/merge logic upstream never has to know or care which format
// produced any given layer.
func decodeGenericIntoConfig(tree genericTree) (*Config, error) {
	b, err := json.Marshal(tree)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// mergeGeneric folds src (a nearer cascade layer) onto dst (everything
// farther away, already merged), recording provenance per leaf key as
// file. Per docs/architecture.md's "tables merge key-wise": when both
// sides hold a genericTree at the same key, merge recurses into it
// rather than one whole table replacing the other -- a nearer directory
// overriding just one key of a table leaves every sibling key a farther
// directory supplied untouched. When a key isn't a table on both sides
// (a farther layer's table entirely replaced by a nearer layer's plain
// scalar, or vice versa), the nearer layer's own value simply wins
// outright for that whole subtree, same as any other leaf -- a real,
// named gap (docs/config-cascade-adversarial.md's own "what this table
// doesn't yet cover") is that a farther layer's now-orphaned nested
// provenance entries for that subtree are left stale in prov rather than
// swept; harmless (they're keys that no longer appear in the merged
// Config at all, so nothing ever looks them up), but not actively
// cleaned up.
func mergeGeneric(dst, src genericTree, base, file string, prov Provenance, values map[string]any) genericTree {
	if dst == nil {
		dst = genericTree{}
	}
	for k, sv := range src {
		key := joinProvenancePath(base, k)
		if smap, ok := sv.(genericTree); ok {
			dmap, _ := dst[k].(genericTree)
			dst[k] = mergeGeneric(dmap, smap, key, file, prov, values)
			continue
		}
		dst[k] = sv
		prov[key] = file
		values[key] = sv
	}
	return dst
}

// identRe matches a bare identifier -- a map key that never needs
// quoting in a rendered provenance path.
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// joinProvenancePath extends base with key, quoting key if it isn't a
// bare identifier (a provider source string like "hashicorp/aws" never
// is). This is a new convention for this surface specifically -- neither
// core/state.go's dotSet nor tfwrite's own dot-path splitting has ever
// needed to quote a segment, since a resource attribute name is always a
// bare identifier; a provider source string is not.
func joinProvenancePath(base, key string) string {
	seg := key
	if !identRe.MatchString(key) {
		seg = strconv.Quote(key)
	}
	if base == "" {
		return seg
	}
	return base + "." + seg
}

// knownTopLevelKeys/knownProviderKeys/knownK8sAuditKeys are the fixed,
// known shape of config -- everything else in warnUnknownKeys' walk is
// freeform by design (provider_config/providers/provider_configs, whose
// keys are provider-defined, not ubx-defined) and never checked below
// its own top-level table name.
var knownTopLevelKeys = map[string]bool{
	"stack": true, "github_repo": true, "tf_dir": true,
	"provider": true, "provider_config": true,
	"providers": true, "provider_configs": true,
	"k8s_audit": true, "ledger": true,
}
var knownProviderKeys = map[string]bool{"path": true, "source": true, "version": true}
var knownK8sAuditKeys = map[string]bool{"cluster": true, "region": true, "log_group": true}
var knownLedgerKeys = map[string]bool{"store": true}

// warnUnknownKeys checks one already-parsed layer against config's known
// shape, warning (never failing) for anything it doesn't recognize --
// UBI-19's own "unknown keys warn, they don't fail" rule, now
// implemented once against a generic tree's own shape instead of relying
// on a format-specific "what didn't decode" API (BurntSushi's
// `MetaData.Undecoded()` stops applying the moment parsing targets a
// generic map rather than the Config struct directly -- confirmed
// empirically, not assumed, before this was written: decoding a real
// TOML fixture into map[string]interface{} reports EVERY key as
// "undecoded," since no struct field ever consumes any of them).
// Warnings are emitted per-layer, at parse time, so they still name the
// exact file a typo came from -- never blurred by a later merge.
func warnUnknownKeys(tree genericTree, file string, warnOut io.Writer) {
	for _, k := range sortedKeys(tree) {
		if !knownTopLevelKeys[k] {
			fmt.Fprintf(warnOut, "warning: %s: unknown config key %q (ignored)\n", file, k)
			continue
		}
		switch k {
		case "provider":
			warnUnknownSubKeys(tree[k], file, "provider", knownProviderKeys, warnOut)
		case "k8s_audit":
			warnUnknownSubKeys(tree[k], file, "k8s_audit", knownK8sAuditKeys, warnOut)
		case "ledger":
			warnUnknownSubKeys(tree[k], file, "ledger", knownLedgerKeys, warnOut)
		}
	}
}

func warnUnknownSubKeys(v any, file, table string, known map[string]bool, warnOut io.Writer) {
	m, ok := v.(genericTree)
	if !ok {
		return
	}
	for _, k := range sortedKeys(m) {
		if !known[k] {
			fmt.Fprintf(warnOut, "warning: %s: unknown config key %q (ignored)\n", file, table+"."+k)
		}
	}
}

func sortedKeys(m genericTree) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// renderProvenance formats rc as a stable, sorted "key = value  <- file"
// report -- every effective value and which file supplied it, per
// docs/architecture.md's own "provenance surface" requirement -- shared
// by the `ubx config` provenance view and its own hermetic tests.
func renderProvenance(rc *ResolvedConfig) string {
	keys := make([]string, 0, len(rc.Provenance))
	for k := range rc.Provenance {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %v\t<- %s\n", k, rc.Values[k], rc.Provenance[k])
	}
	return b.String()
}
