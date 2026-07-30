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
	// first -- the same order the cascade walk visited them in. Does NOT
	// include UserGlobalFile, which sits outside the cascade proper.
	Files []string
	// CeilingReason/CeilingPath explain where and why the upward walk
	// stopped (UBI-32 Arc B addendum, docs/architecture.md -- "Cascade
	// ceiling"): one of ceilingRootMarker/ceilingRepoBoundary/
	// ceilingHome/ceilingFilesystemRoot, and the file (root marker) or
	// directory (every other reason) responsible.
	CeilingReason ceilingReason
	CeilingPath   string
	// UserGlobalFile is the ~/.ubx/config* file consulted for
	// personal-preference keys, if any was found -- "" if none. Never
	// part of Files: user-global config sits outside the cascade walk
	// entirely (docs/architecture.md -- "User-global ~/.ubx/config").
	UserGlobalFile string
}

// ceilingReason names why the cascade's upward walk stopped where it
// did (UBI-32 Arc B addendum). Checked in this priority order at every
// directory the walk visits, on top of that directory's own
// configFileCandidates discovery.
type ceilingReason string

const (
	ceilingRootMarker     ceilingReason = "root marker"
	ceilingRepoBoundary   ceilingReason = "repo boundary"
	ceilingHome           ceilingReason = "$HOME"
	ceilingFilesystemRoot ceilingReason = "filesystem root"
)

// configFileCandidates is the per-directory discovery order (UBI-32 Arc
// A): first found wins for that directory; formats never merge within
// one directory. `config` (no extension) is UBI-19's original legacy
// name, kept in its original priority position forever.
var configFileCandidates = []string{"config.hcl", "config.toml", "config", "config.yaml"}

// LoadConfigResolved discovers the full .ubx/config cascade -- every
// `.ubx/config*` from configSearchStartDir() upward, stopping at
// whichever cascade ceiling fires first (docs/architecture.md --
// "Cascade ceiling": a `root = true` file, else the git repo boundary,
// else $HOME or the filesystem root) -- parses each into a genericTree,
// folds them root-to-nearest (so a nearer directory's own value for any
// given key always wins, recorded in Provenance), folds in
// ~/.ubx/config's own allowlisted personal-preference keys as the
// lowest-priority layer of all, and decodes the single merged tree into
// *Config. Returns a zero Config (not an error) if no config file exists
// anywhere in the walk.
func LoadConfigResolved(warnOut io.Writer) (*ResolvedConfig, error) {
	dir, err := configSearchStartDir()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return loadConfigResolvedFromDir(dir, warnOut)
}

// loadConfigResolvedFromDir is LoadConfigResolved's own cascade-walk-plus-
// merge logic, parameterized on the starting directory instead of always
// starting from configSearchStartDir() -- `ubx promote --to <target-dir>`
// (UBI-55) is the first caller that genuinely needs a SECOND config,
// scoped to a directory that isn't the process's own cwd (the target
// environment's own directory, distinct from the source proposal's
// ledger dir): every other command opens exactly one ledger/config, at
// cwd, so this seam never needed to exist before. Behavior for every
// existing caller is unchanged -- LoadConfigResolved is now a one-line
// wrapper around this with dir = configSearchStartDir().
func loadConfigResolvedFromDir(dir string, warnOut io.Writer) (*ResolvedConfig, error) {
	walk, err := walkCascade(dir, warnOut)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	userGlobalTree, userGlobalFile, err := loadUserGlobalLayer(walk.layers)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	merged := genericTree{}
	prov := Provenance{}
	values := map[string]any{}
	if userGlobalTree != nil {
		merged = mergeGeneric(merged, userGlobalTree, "", userGlobalFile, prov, values)
	}
	// Fold root-to-nearest (walk.layers is nearest-first) so a nearer
	// directory's own value always overwrites a farther one's, per key --
	// and always overwrites whatever user-global just supplied, too.
	for i := len(walk.layers) - 1; i >= 0; i-- {
		layer := walk.layers[i]
		merged = mergeGeneric(merged, layer.tree, "", layer.file, prov, values)
	}

	cfg, err := decodeGenericIntoConfig(merged)
	if err != nil {
		return nil, fmt.Errorf("load config: decode merged config: %w", err)
	}
	files := make([]string, len(walk.layers))
	for i, l := range walk.layers {
		files[i] = l.file
	}
	return &ResolvedConfig{
		Config: cfg, Provenance: prov, Values: values, Files: files,
		CeilingReason: walk.ceilingReason, CeilingPath: walk.ceilingPath,
		UserGlobalFile: userGlobalFile,
	}, nil
}

// cascadeLayer is one directory's own contribution to the walk: the file
// discovered there (via configFileCandidates) and its already-parsed
// generic tree -- parsed inline, during the walk, rather than in a
// second pass, because deciding whether to keep walking upward at all
// (the `root = true` check) depends on this layer's own parsed content.
type cascadeLayer struct {
	file string
	tree genericTree
}

// cascadeWalk is walkCascade's own result: every layer found, nearest
// first, plus which rule stopped the walk and where.
type cascadeWalk struct {
	layers        []cascadeLayer
	ceilingReason ceilingReason
	ceilingPath   string
}

// userHomeDir resolves the current user's home directory -- a package
// var, not a bare os.UserHomeDir() call, so tests can point it at an
// isolated scratch directory instead (same convention as cli's own
// configSearchStartDir): without this seam, every test in this package
// would depend on whatever the real host machine's $HOME happens to
// contain, exactly the ambient-state leak `go test ./...` staying
// hermetic is supposed to rule out. Defaulted to a safe, nonexistent
// scratch path by this package's own TestMain; individual tests that
// specifically exercise $HOME-ceiling or user-global-config behavior
// override it further, per test.
var userHomeDir = os.UserHomeDir

// walkCascade walks from startDir upward, parsing one file per directory
// (configFileCandidates' own discovery order) until a cascade ceiling
// rule fires (docs/architecture.md -- "Cascade ceiling"), checked in
// this order at every directory visited:
//
//  1. root = true in that directory's own config -- an inclusive stop,
//     that layer's own other keys still apply.
//  2. That directory itself contains .git (a directory or a file --
//     either is sufficient signal; contents are never read) -- the
//     implicit repo boundary, also inclusive.
//  3. That directory IS $HOME, or is the filesystem root (parent == dir)
//     -- the fallback ceiling for a walk that never finds rules 1 or 2,
//     also inclusive.
func walkCascade(startDir string, warnOut io.Writer) (cascadeWalk, error) {
	var layers []cascadeLayer
	home, homeErr := userHomeDir()
	dir := startDir
	for {
		if file := pickConfigFile(dir); file != "" {
			tree, err := parseGenericFile(file)
			if err != nil {
				return cascadeWalk{}, fmt.Errorf("parse %s: %w", file, err)
			}
			warnUnknownKeys(tree, file, warnOut)
			layers = append(layers, cascadeLayer{file: file, tree: tree})

			isRoot, err := treeDeclaresRoot(tree)
			if err != nil {
				return cascadeWalk{}, fmt.Errorf("%s: %w", file, err)
			}
			if isRoot {
				return cascadeWalk{layers: layers, ceilingReason: ceilingRootMarker, ceilingPath: file}, nil
			}
		}

		if hasGitBoundary(dir) {
			return cascadeWalk{layers: layers, ceilingReason: ceilingRepoBoundary, ceilingPath: dir}, nil
		}
		if homeErr == nil && home != "" && samePath(dir, home) {
			return cascadeWalk{layers: layers, ceilingReason: ceilingHome, ceilingPath: dir}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cascadeWalk{layers: layers, ceilingReason: ceilingFilesystemRoot, ceilingPath: dir}, nil
		}
		dir = parent
	}
}

// treeDeclaresRoot reports whether tree's own top-level "root" key is
// the literal boolean true. A "root" key present but not a bool at all
// is a hard error -- the same "ambiguity rejected loudly, never guessed"
// standard this surface already holds YAML strict mode to.
func treeDeclaresRoot(tree genericTree) (bool, error) {
	v, ok := tree["root"]
	if !ok {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("root must be a literal boolean (true/false), got %T", v)
	}
	return b, nil
}

// hasGitBoundary reports whether dir itself contains a `.git` entry --
// a directory for an ordinary checkout, a file for a worktree or
// submodule (a "gitdir: ..." pointer). Either is sufficient signal;
// contents are never read.
func hasGitBoundary(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// samePath reports whether a and b name the same directory, after
// cleaning both -- no symlink resolution, matching the level of rigor
// the rest of this cascade already applies to path comparisons.
func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
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
// core/state.go's dotSet nor writeback's own dot-path splitting has ever
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
	"k8s_audit": true, "ledger": true, "root": true,
	"intent": true,
}
var knownProviderKeys = map[string]bool{"path": true, "source": true, "version": true}
var knownK8sAuditKeys = map[string]bool{"cluster": true, "region": true, "log_group": true}
var knownLedgerKeys = map[string]bool{"store": true, "external": true}

// knownIntentKeys is IntentConfig's own known shape (UBI-41,
// docs/intent-provider.md's own "Unknown-key checking, extended" note --
// this is that extension, built). key_ref/vertex are themselves nested
// objects (env; project/location) but, matching every other table here,
// only checked one level deep -- no existing table's own unknown-key
// check recurses further than its own immediate sub-keys either.
var knownIntentKeys = map[string]bool{
	"adapter": true, "model": true, "key_ref": true,
	"auth": true, "vertex": true,
}

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
		case "intent":
			warnUnknownSubKeys(tree[k], file, "intent", knownIntentKeys, warnOut)
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
	if rc.CeilingReason != "" {
		fmt.Fprintf(&b, "cascade stopped at: %s (%s)\n", rc.CeilingReason, rc.CeilingPath)
	}
	if rc.UserGlobalFile != "" {
		fmt.Fprintf(&b, "user-global config consulted: %s\n", rc.UserGlobalFile)
	}
	return b.String()
}
