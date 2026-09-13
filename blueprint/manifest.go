package blueprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/core"
)

// ManifestFileName is the well-known file `ubx blueprint package` writes
// into a built blueprint's own directory, and `ubx blueprint verify`
// reads back -- an ordinary file that travels with the directory through
// ANY distribution mechanism (a plain local copy, a git clone, or the
// tarball `package` also produces), never a side channel a distribution
// mechanism has to know about specially (docs/blueprint.md).
const ManifestFileName = "blueprint.lock.json"

// manifestSchemaVersion is bumped only if Manifest's own shape changes
// incompatibly -- independent of docs/schema.md's own Proposal
// schema_version; a blueprint package's lock file is a completely
// separate artifact with its own versioning.
const manifestSchemaVersion = 1

// Manifest is blueprint.lock.json's own decoded shape: every real file
// in a built blueprint's directory (relative path -> "sha256:<hex>" of
// its raw content, docs/schema.md's own established content-hash
// format), plus one overall ContentHash tying them all together --
// the same "hash everything except the hash field itself" pattern
// core.Hash already uses for a Proposal (core/canonical.go), applied
// here to a directory of files instead of one JSON document.
type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	Name          string            `json:"name"`
	Files         map[string]string `json:"files"`
	ContentHash   string            `json:"content_hash"`
}

// sha256Hex hashes b into "sha256:<hex>" -- the same format core/hash.go's
// own sha256Hex (Proposal ids) and intentprovider/sources.go's own
// sha256Hex (document/source hashes) already establish independently of
// each other; blueprint keeps its own tiny copy rather than exporting
// either of theirs across a package boundary, matching how those two
// packages already don't share one between themselves either.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// excludedDirs are the dependency directories a blueprint package never
// contains. The same list two other walkers in this codebase already
// use (cli/mcp_blueprint.go's own blueprint discovery,
// blueprint/callsite.go's own Python root walk), plus vendor.
//
// They are installed artifacts, not authored content. A blueprint's
// content hash is its identity, and hashing a dependency tree makes
// that identity change every time anyone reinstalls: a real TypeScript
// blueprint of three files packaged 13,922 of them, and `ubx blueprint
// verify` failed on the author's own working tree after an ordinary
// `npm install`.
//
// They are not used either, which is what makes this cost rather than
// tradeoff. Deno resolves a bare npm specifier from the node_modules it
// finds by walking up from the runner script, and tseval writes that
// runner into the CONSUMER's entry directory, so a blueprint's own
// node_modules is unreachable at evaluation time. goeval builds with
// GOFLAGS=-mod=mod, which ignores vendor/ outright.
//
// vendor is excluded on the same terms as the rest, though vendoring is
// a deliberate Go pattern rather than an install artifact. Nothing
// reads it during evaluation today, and the real objection to excluding
// it was silence rather than the exclusion, which the package receipt
// now answers by naming what it left out.
var excludedDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"venv":         true,
	"__pycache__":  true,
}

// skipEntry reports whether a directory entry should be excluded from a
// blueprint package's own file set: anything dot-prefixed (a `.git`
// directory a locally-authored blueprint happens to sit inside, or any
// future dotfile convention), so packaging/pulling never accidentally
// treats VCS internals or hidden tooling state as blueprint content,
// and any of excludedDirs above.
//
// The dot rule came first and covered dependencies only by accident.
// `.venv` was excluded because it begins with a dot, never because
// anything knew it was a virtualenv, and `venv` was not excluded at
// all. That distinction is not academic: pyeval's own venvSitePackages
// treats BOTH spellings as a real virtualenv, and the undotted one is
// what Python's own documentation uses (`python -m venv venv`), so a
// Python author following the standard instructions hit exactly the
// TypeScript problem. Python was never right here, only lucky about
// which spelling it happened to use.
//
// The rule also predates the situation. Under the Ubxfile model,
// package archived a BUILT directory of generated go/ts/py output,
// which has no dependency tree in it. Blueprints as code made package
// archive the author's own working tree, which is where node_modules
// lives, and the rule was never revisited.
func skipEntry(name string) bool {
	return strings.HasPrefix(name, ".") || excludedDirs[name]
}

// hashFiles walks dir recursively (skipping dot-prefixed entries and
// ManifestFileName itself -- a manifest can't hash its own not-yet-
// written content, and once written its bytes are metadata ABOUT the
// package, not package content) and returns relative-path ->
// "sha256:<hex>" for every real file found.
func hashFiles(dir string) (map[string]string, []string, error) {
	files := map[string]string{}
	// Which exclusions actually fired, so package can name them rather
	// than leaving an author to notice a file count they did not expect.
	// Recorded by name, deduplicated, sorted by the caller.
	excluded := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if skipEntry(d.Name()) {
			if d.IsDir() {
				if excludedDirs[d.Name()] {
					excluded[d.Name()] = true
				}
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == ManifestFileName {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		files[rel] = sha256Hex(raw)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(excluded))
	for n := range excluded {
		names = append(names, n)
	}
	sort.Strings(names)
	return files, names, nil
}

// buildManifest computes name's own content hash over dir's current
// files (hashFiles above) using core.CanonicalJSON -- core/canonical.go's
// own JCS-style canonicalization, the SAME approach core.Hash already
// uses for a Proposal's own hash, rather than hashing a tarball's raw
// bytes directly (which would make the hash sensitive to tar header
// metadata -- mtimes, uid/gid, entry order -- none of which is actual
// package content; CLAUDE.md's "determinism is a feature" rule means a
// content hash should track content, not incidental packaging noise).
func buildManifest(dir, name string) (*Manifest, error) {
	m, _, err := buildManifestReportingExclusions(dir, name)
	return m, err
}

// buildManifestReportingExclusions is buildManifest plus which
// dependency directories it actually skipped, which only `package`
// needs: every other caller is verifying or discovering, where the
// answer is the hash rather than what was left out of it.
func buildManifestReportingExclusions(dir, name string) (*Manifest, []string, error) {
	files, excluded, err := hashFiles(dir)
	if err != nil {
		return nil, nil, err
	}
	filesIface := make(map[string]interface{}, len(files))
	for k, v := range files {
		filesIface[k] = v
	}
	canon, err := core.CanonicalJSON(map[string]interface{}{
		"schema_version": int64(manifestSchemaVersion),
		"name":           name,
		"files":          filesIface,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("blueprint: canonicalize manifest: %w", err)
	}
	return &Manifest{
		SchemaVersion: manifestSchemaVersion,
		Name:          name,
		Files:         files,
		ContentHash:   sha256Hex(canon),
	}, excluded, nil
}

// writeManifest writes m as indented JSON into dir/ManifestFileName --
// indented deliberately (unlike core's own compact canonical bytes,
// which only ever exist transiently for hashing): blueprint.lock.json is
// meant to be committed to git and read by a human reviewing a diff, not
// just machine-parsed.
func writeManifest(dir string, m *Manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("blueprint: marshal manifest: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, ManifestFileName), append(raw, '\n'), 0o644)
}

// readManifest reads and parses dir/ManifestFileName.
func readManifest(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFileName))
	if err != nil {
		return nil, fmt.Errorf("blueprint: read %s: %w", ManifestFileName, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("blueprint: parse %s: %w", ManifestFileName, err)
	}
	return &m, nil
}
