package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// contentstore.go is the local blueprint cache: content-addressed
// storage, plus the index that makes it legible.
//
// It replaces a cache keyed on the declaration string (name + URL,
// hashed). That layout had one slot per mutable name, and the failure it
// produced was not the one anyone would assume.
//
// It did NOT clobber. A repointed tag mapped to the same directory, and
// a cache hit verified that directory against ITS OWN manifest, which
// always succeeds, so the registry was never asked again. Whichever
// content arrived first stayed, on that machine, indefinitely. A machine
// pulling for the first time got the new content. Both verified. Nothing
// anywhere noticed the two disagreed.
//
// A clobber is loud and local: the wrong thing is there, and the next
// verify or run finds it. A permanent silent stale serve is neither, and
// it diverges machine by machine according to who happened to pull
// first. Only the first is what "cache collision" suggests, which is
// exactly why it is worth writing down.
//
// Storage is by content hash and nothing else: sha256/<hex>/. Tags
// resolve through the stack's lock file, the way a container runtime
// keeps digests as storage and tags as pointers. The consequence is
// deliberate and is the point: a tag cannot be resolved without asking
// the registry, so a stack with no lock entry pulls every time. Caching
// a mutable pointer locally is the unsoundness this replaces, not a
// feature to preserve.

// contentStoreDirName is the scheme directory under the cache root.
// A directory level rather than a filename prefix so a second hash
// algorithm is a sibling rather than a rename, matching how OCI lays out
// its own blobs.
const contentStoreDirName = "sha256"

// IndexFileName is the origin index, at the cache root.
//
// A content-addressed store cannot be read by a human without one: a
// directory of bare hashes says nothing about what any of it is. The
// index is therefore not a convenience on top of the layout, it is what
// keeps the layout from being a regression in legibility.
//
// It is also the only place an origin can live at all. A blueprint's own
// blueprint.lock.json records its content hash and no origin, so a
// pulled directory cannot say which reference produced it, and a stack's
// own lock knows only about that stack. Pull time is the one moment both
// halves are in hand.
const IndexFileName = "index.json"

// IndexSchemaVersion versions the index file's shape.
const IndexSchemaVersion = 1

// IndexEntry is one cached blueprint.
type IndexEntry struct {
	// Name is the blueprint's own declared name, from its manifest.
	Name string `json:"name"`

	// Sources is every declaration seen to produce this content, oldest
	// first. A list rather than one value because two tags legitimately
	// resolve to the same bytes, and because a tag that moved away still
	// explains why the content is here.
	Sources []string `json:"sources"`

	FirstPulled string `json:"first_pulled"`
	LastUsed    string `json:"last_used"`

	FileCount int   `json:"file_count"`
	SizeBytes int64 `json:"size_bytes"`
}

// Index is the cache root's own index.json.
type Index struct {
	SchemaVersion int `json:"schema_version"`

	// Entries maps content hash -> what is known about it.
	Entries map[string]IndexEntry `json:"entries"`
}

func indexPath(root string) string { return filepath.Join(root, IndexFileName) }

// BlueprintContentDir is where content with this hash lives.
func BlueprintContentDir(contentHash string) (string, error) {
	root, err := defaultBlueprintCacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, contentStoreDirName, hashHex(contentHash)), nil
}

// hashHex strips the "sha256:" prefix, since the scheme is already the
// directory this sits under. A hash arriving without one is used as-is
// rather than rejected: this is a path helper, and a caller handing it
// something unexpected gets a directory that simply will not exist,
// which is a better failure than a panic in a cache path.
func hashHex(contentHash string) string {
	if len(contentHash) > 7 && contentHash[:7] == "sha256:" {
		return contentHash[7:]
	}
	return contentHash
}

// LoadIndex reads the cache index. A missing file is not an error: a
// machine that has never pulled a blueprint legitimately has none.
func LoadIndex() (*Index, error) {
	root, err := defaultBlueprintCacheRoot()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(indexPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return &Index{SchemaVersion: IndexSchemaVersion, Entries: map[string]IndexEntry{}}, nil
		}
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		// A corrupt cache index is not worth failing a command over:
		// everything it holds is recoverable by pulling again, and the
		// alternative is a machine that cannot plan until someone
		// deletes a file they have never heard of. Rebuilt from empty.
		return &Index{SchemaVersion: IndexSchemaVersion, Entries: map[string]IndexEntry{}}, nil
	}
	if idx.Entries == nil {
		idx.Entries = map[string]IndexEntry{}
	}
	return &idx, nil
}

// saveIndex writes the index atomically, so a killed process leaves the
// previous index rather than a truncated one.
//
// Concurrent ubx invocations can still interleave read-modify-write and
// lose an entry. Accepted deliberately rather than locked: the index
// describes a cache, every entry in it is reconstructible by pulling
// again, and a lock file around a cache index is more machinery than the
// worst case justifies.
func saveIndex(idx *Index) error {
	root, err := defaultBlueprintCacheRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	idx.SchemaVersion = IndexSchemaVersion
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(root, ".index-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), indexPath(root))
}

// recordInIndex notes that source produced this content, now.
//
// Never fatal. The index exists to explain a cache, and a machine whose
// home directory is read-only should still be able to plan.
func recordInIndex(manifest *Manifest, source string, dir string) {
	idx, err := LoadIndex()
	if err != nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)

	e, ok := idx.Entries[manifest.ContentHash]
	if !ok {
		e = IndexEntry{FirstPulled: now}
	}
	e.Name = manifest.Name
	e.LastUsed = now
	e.FileCount = len(manifest.Files)
	if size, err := dirSize(dir); err == nil {
		e.SizeBytes = size
	}
	if source != "" && !containsSource(e.Sources, source) {
		e.Sources = append(e.Sources, source)
	}
	idx.Entries[manifest.ContentHash] = e
	_ = saveIndex(idx)
}

func containsSource(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// SortedIndexEntries returns the index's entries in a stable order for
// display: by name, then by hash, so two runs on one machine print the
// same thing and a hash with no name still has a defined place.
func (idx *Index) SortedIndexEntries() []struct {
	Hash  string
	Entry IndexEntry
} {
	out := make([]struct {
		Hash  string
		Entry IndexEntry
	}, 0, len(idx.Entries))
	for h, e := range idx.Entries {
		out = append(out, struct {
			Hash  string
			Entry IndexEntry
		}{h, e})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Entry.Name != out[j].Entry.Name {
			return out[i].Entry.Name < out[j].Entry.Name
		}
		return out[i].Hash < out[j].Hash
	})
	return out
}

// fetchIntoContentStore returns a verified local directory for dep,
// pulling only when it has to.
//
// expectHash is the stack lock's own recorded hash, or "". With one, the
// content store is consulted directly and a hit never touches the
// network, because the question "do I have exactly this content" is
// answerable locally. Without one, there is nothing to look up: a tag
// cannot be resolved without asking the registry, so this pulls.
//
// A pull lands in a temp directory inside the cache root first, so the
// rename into place is same-filesystem and atomic, and a failed or
// interrupted pull never leaves a partial directory under a hash that
// would then verify as absent-but-present.
func fetchIntoContentStore(ctx context.Context, dep PyDependency, expectHash string) (dir string, manifest *Manifest, fromCache bool, err error) {
	if expectHash != "" {
		hit, err := BlueprintContentDir(expectHash)
		if err != nil {
			return "", nil, false, err
		}
		if m, verr := Verify(hit); verr == nil && m.ContentHash == expectHash {
			recordInIndex(m, dep.URL, hit)
			return hit, m, true, nil
		}
	}

	root, err := defaultBlueprintCacheRoot()
	if err != nil {
		return "", nil, false, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", nil, false, err
	}
	staging, err := os.MkdirTemp(root, ".pull-*")
	if err != nil {
		return "", nil, false, err
	}
	defer os.RemoveAll(staging)

	dest := filepath.Join(staging, "blueprint")
	if _, err := Pull(ctx, dep.Source, dest, dep.Ref, dep.Path); err != nil {
		return "", nil, false, fmt.Errorf("pull: %w", err)
	}
	m, err := Verify(dest)
	if err != nil {
		return "", nil, false, fmt.Errorf("verify: %w", err)
	}

	final, err := BlueprintContentDir(m.ContentHash)
	if err != nil {
		return "", nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return "", nil, false, err
	}
	if err := os.Rename(dest, final); err != nil {
		// Already present is the ordinary race: two invocations pulled
		// the same content at once, and content-addressed storage means
		// whichever won is byte-identical to what this one has. Reuse it
		// rather than failing, which is the whole advantage of keying on
		// content.
		if _, statErr := os.Stat(final); statErr != nil {
			return "", nil, false, err
		}
	}
	recordInIndex(m, dep.URL, final)
	return final, m, false, nil
}
