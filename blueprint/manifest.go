package blueprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// skipEntry reports whether a directory entry should be excluded from a
// blueprint package's own file set -- anything dot-prefixed (a `.git`
// directory a locally-authored blueprint happens to sit inside, or any
// future dotfile convention), so packaging/pulling never accidentally
// treats VCS internals or hidden tooling state as blueprint content.
func skipEntry(name string) bool {
	return strings.HasPrefix(name, ".")
}

// hashFiles walks dir recursively (skipping dot-prefixed entries and
// ManifestFileName itself -- a manifest can't hash its own not-yet-
// written content, and once written its bytes are metadata ABOUT the
// package, not package content) and returns relative-path ->
// "sha256:<hex>" for every real file found.
func hashFiles(dir string) (map[string]string, error) {
	files := map[string]string{}
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
		return nil, err
	}
	return files, nil
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
	files, err := hashFiles(dir)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("blueprint: canonicalize manifest: %w", err)
	}
	return &Manifest{
		SchemaVersion: manifestSchemaVersion,
		Name:          name,
		Files:         files,
		ContentHash:   sha256Hex(canon),
	}, nil
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
