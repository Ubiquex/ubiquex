package blueprint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitBlueprintRepo builds a git repo holding one blueprint at
// blueprints/<name>, tagged v1, and returns the repo dir. A git source
// rather than a local path on purpose: a local path is deliberately
// never cached (an author may be editing it), so it cannot exercise the
// content store at all.
func gitBlueprintRepo(t *testing.T, name string) string {
	t.Helper()
	repoDir := initTestGitRepo(t)
	bpDir := filepath.Join(repoDir, "blueprints", name)
	if err := os.MkdirAll(filepath.Join(bpDir, "py"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bpDir, UbxfileName), []byte("lang: py\n\nresources: |\n  A trivial fake widget.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bpDir, "py", name+".py"), []byte("def add(name):\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(context.Background(), bpDir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}
	gitCommitAll(t, repoDir, "add blueprint")
	tag := exec.Command("git", "tag", "v1")
	tag.Dir = repoDir
	if out, err := tag.CombinedOutput(); err != nil {
		t.Fatalf("git tag v1: %v: %s", err, out)
	}
	return repoDir
}

func resolveFromSource(t *testing.T, name, source string) []PyDepMount {
	t.Helper()
	progDir := t.TempDir()
	writeRequirementsTxt(t, progDir, name+" @ "+source+"\n")
	mounts, _, err := ResolvePyDependencies(context.Background(), filepath.Join(progDir, "main.py"))
	if err != nil {
		t.Fatalf("resolve from %s: %v", source, err)
	}
	return mounts
}

// TestContentStore_IndexWrittenAtPullTime is why the index exists at
// all: storage is keyed by content hash, and a directory of bare hashes
// says nothing about what any of it is. Pull time is the one moment both
// the origin and the resulting hash are in hand, since a blueprint's own
// manifest records a hash and no origin.
func TestContentStore_IndexWrittenAtPullTime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repo := gitBlueprintRepo(t, "widgetlib")
	source := "git+file://" + repo + "@v1#subdirectory=blueprints/widgetlib"
	mounts := resolveFromSource(t, "widgetlib", source)

	idx, err := LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := idx.Entries[mounts[0].ContentHash]
	if !ok {
		t.Fatalf("pulling wrote no index entry, so the cache cannot be explained: %+v", idx)
	}
	if e.Name != "widgetlib" {
		t.Errorf("index recorded name %q, want widgetlib -- the hash alone cannot give this", e.Name)
	}
	if len(e.Sources) != 1 || e.Sources[0] != source {
		t.Errorf("index recorded sources %v, want the declaration it came from", e.Sources)
	}
	if e.FileCount == 0 || e.SizeBytes == 0 {
		t.Errorf("index recorded no size or file count, so a future prune has nothing to reason about: %+v", e)
	}
	if e.FirstPulled == "" || e.LastUsed == "" {
		t.Errorf("index recorded no timestamps: %+v", e)
	}

	// The content really is where the layout says it is.
	dir, err := BlueprintContentDir(mounts[0].ContentHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("content is not at sha256/<hex>: %v", err)
	}
}

// TestContentStore_OneEntryForTwoSources is the shape a content-addressed
// store produces and a declaration-keyed one could not: identical bytes
// reached through two declarations are one directory with two recorded
// origins, not two copies.
func TestContentStore_OneEntryForTwoSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repo := gitBlueprintRepo(t, "widgetlib")
	sub := "#subdirectory=blueprints/widgetlib"
	// Two references to the same commit: the tag, and the branch it is
	// on. Different declarations, identical bytes.
	viaTag := "git+file://" + repo + "@v1" + sub
	viaBranch := "git+file://" + repo + sub

	first := resolveFromSource(t, "widgetlib", viaTag)
	second := resolveFromSource(t, "widgetlib", viaBranch)

	if first[0].ContentHash != second[0].ContentHash {
		t.Fatalf("fixture is wrong: the two references produced different content (%s vs %s)",
			first[0].ContentHash, second[0].ContentHash)
	}
	if first[0].HostDir != second[0].HostDir {
		t.Fatalf("identical content must share one directory, got %q and %q", first[0].HostDir, second[0].HostDir)
	}

	idx, err := LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Entries) != 1 {
		t.Fatalf("identical content from two sources must be ONE entry, got %d", len(idx.Entries))
	}
	e := idx.Entries[first[0].ContentHash]
	if len(e.Sources) != 2 {
		t.Fatalf("both origins should be recorded, got %v -- a tag that has since moved still explains why the content is here", e.Sources)
	}
}

// TestContentStore_LockedHitNeverAsksTheSource pins the property the
// lock buys, at the store level: with a recorded hash, the question "do
// I have exactly this" is answerable locally.
func TestContentStore_LockedHitNeverAsksTheSource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repo := gitBlueprintRepo(t, "widgetlib")
	source := "git+file://" + repo + "@v1#subdirectory=blueprints/widgetlib"
	first := resolveFromSource(t, "widgetlib", source)

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	dep := PyDependency{Name: "widgetlib", URL: source, Source: "file://" + repo}
	dir, m, fromCache, err := fetchIntoContentStore(context.Background(), dep, first[0].ContentHash)
	if err != nil {
		t.Fatalf("a locked hash must resolve from the store with the source gone: %v", err)
	}
	if !fromCache {
		t.Error("expected a cache hit, not a pull")
	}
	if m.ContentHash != first[0].ContentHash {
		t.Errorf("wrong content returned: %s, want %s", m.ContentHash, first[0].ContentHash)
	}
	// dir is the content root; HostDir is the py/ package inside it.
	want, err := BlueprintContentDir(first[0].ContentHash)
	if err != nil {
		t.Fatal(err)
	}
	if dir != want {
		t.Errorf("served from %q, want the content-addressed directory %q", dir, want)
	}

	// And without one there is nothing to look up, so it has to ask.
	if _, _, _, err := fetchIntoContentStore(context.Background(), dep, ""); err == nil {
		t.Error("an unlocked fetch must ask the source -- caching a mutable pointer locally is the unsoundness this replaces")
	}
}
