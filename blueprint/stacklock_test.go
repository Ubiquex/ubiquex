package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackLock_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	l, err := LoadStackLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.Existed() {
		t.Fatal("a directory with no lock file must not report one as existing")
	}

	l.Set("payments", "ci-platform", LockEntry{
		Source:      "oci://ghcr.io/ubiquex/ci-platform:v3",
		ContentHash: "sha256:aaa",
	})
	// A second stack in the SAME directory, which .ubx/plans/ already
	// proves is a real situation.
	l.Set("billing", "ci-platform", LockEntry{
		Source:      "oci://ghcr.io/ubiquex/ci-platform:v2",
		ContentHash: "sha256:bbb",
	})
	if err := l.Save(dir); err != nil {
		t.Fatal(err)
	}

	back, err := LoadStackLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Existed() {
		t.Fatal("a saved lock must report as existing")
	}
	e, ok := back.Entry("payments", "ci-platform")
	if !ok || e.ContentHash != "sha256:aaa" || e.Source != "oci://ghcr.io/ubiquex/ci-platform:v3" {
		t.Fatalf("payments entry did not survive: %+v %v", e, ok)
	}
	// The two stacks must not have collapsed into one another, which is
	// the whole reason this is stack-keyed from its first version.
	if e, _ := back.Entry("billing", "ci-platform"); e.ContentHash != "sha256:bbb" {
		t.Fatalf("billing entry = %+v, want its own distinct hash", e)
	}
	if _, ok := back.Entry("payments", "not-declared"); ok {
		t.Fatal("an absent blueprint must not resolve")
	}
}

// TestStackLock_UnknownSchemaVersion: a file from a different ubx is a
// named migration problem, not a confusing parse failure or a silently
// ignored file.
func TestStackLock_UnknownSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".ubx", StackLockFileName)
	if err := os.WriteFile(path, []byte(`{"schema_version":99,"stacks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadStackLock(dir)
	if err == nil {
		t.Fatal("expected a refusal for an unknown schema_version")
	}
	if !strings.Contains(err.Error(), "99") || !strings.Contains(err.Error(), path) {
		t.Fatalf("error should name both the version it found and the file: %v", err)
	}
}

// TestCheckLocked_TwoDistinctMessages is the heart of the mechanism.
//
// The two mismatches mean genuinely different things and the right
// response differs, so telling them apart is not cosmetic. A changed
// declaration is ordinary and the fix is to update. Content moving under
// an unchanged declaration is either a repointed mutable tag or a
// registry serving different bytes, and "just update" is the wrong first
// suggestion for either.
func TestCheckLocked_TwoDistinctMessages(t *testing.T) {
	locked := LockEntry{
		Source:      "oci://ghcr.io/ubiquex/ci-platform:v3",
		ContentHash: "sha256:aaa",
	}

	t.Run("agreement passes", func(t *testing.T) {
		if err := CheckLocked("payments", "ci-platform", locked, locked.Source, locked.ContentHash); err != nil {
			t.Fatalf("matching source and hash must pass: %v", err)
		}
	})

	t.Run("the declaration changed", func(t *testing.T) {
		err := CheckLocked("payments", "ci-platform", locked, "oci://ghcr.io/ubiquex/ci-platform:v4", "sha256:ccc")
		if err == nil {
			t.Fatal("expected a refusal")
		}
		if !strings.Contains(err.Error(), "the declaration changed") {
			t.Fatalf("wrong message for a changed declaration: %v", err)
		}
		if !strings.Contains(err.Error(), "--update-lock") {
			t.Fatalf("a changed declaration should name the update path: %v", err)
		}
	})

	t.Run("the content moved under an unchanged declaration", func(t *testing.T) {
		err := CheckLocked("payments", "ci-platform", locked, locked.Source, "sha256:zzz")
		if err == nil {
			t.Fatal("expected a refusal")
		}
		msg := err.Error()
		if !strings.Contains(msg, "the content behind it did") {
			t.Fatalf("wrong message for moved content: %v", err)
		}
		// Both hashes have to appear, because the first thing anyone
		// does is compare them.
		if !strings.Contains(msg, "sha256:zzz") || !strings.Contains(msg, "sha256:aaa") {
			t.Fatalf("both the resolved and locked hashes must be named: %v", err)
		}
		// "Find out why" comes before "update", which is the whole point
		// of separating this case from the one above.
		if strings.Index(msg, "Find out which") > strings.Index(msg, "--update-lock") {
			t.Fatalf("the investigate-first instruction must precede the update path: %v", err)
		}
	})
}

// TestMergeDeclaredBlueprints covers the precedence rule and that it is
// reported rather than applied quietly.
func TestMergeDeclaredBlueprints(t *testing.T) {
	table := map[string]string{
		"ci-platform": "oci://ghcr.io/ubiquex/ci-platform:v3",
		"alerts":      "oci://ghcr.io/ubiquex/alerts:v1",
	}
	reqs := []PyDependency{
		{Name: "ci-platform", URL: "oci://ghcr.io/ubiquex/ci-platform:OLD"},
		{Name: "py-only", URL: "git+https://example.com/py-only"},
	}

	merged, superseded := mergeDeclaredBlueprints(table, reqs)

	byName := map[string]string{}
	for _, d := range merged {
		byName[d.Name] = d.URL
	}
	if got := byName["ci-platform"]; got != table["ci-platform"] {
		t.Fatalf("the table must win a name collision, got %q", got)
	}
	if _, ok := byName["py-only"]; !ok {
		t.Fatal("a requirements.txt-only entry must survive -- removing it would break every stack UBI-130 serves")
	}
	if len(superseded) != 1 || superseded[0] != "ci-platform" {
		t.Fatalf("the supersession must be reported, got %v", superseded)
	}

	// Deterministic order: the table's own entries sorted, then
	// requirements.txt's in file order. A map range would make receipts
	// reorder between runs.
	if merged[0].Name != "alerts" || merged[1].Name != "ci-platform" {
		t.Fatalf("table entries should come first, sorted: %v", merged)
	}
}

// TestStackLock_Prune: a declaration removed from config removes the
// lock entry, and says so.
func TestStackLock_Prune(t *testing.T) {
	l := &StackLock{Stacks: map[string]map[string]LockEntry{}}
	l.Set("payments", "keep", LockEntry{ContentHash: "sha256:a"})
	l.Set("payments", "drop", LockEntry{ContentHash: "sha256:b"})
	l.Set("billing", "untouched", LockEntry{ContentHash: "sha256:c"})

	removed := l.Prune("payments", map[string]string{"keep": "oci://x"})
	if len(removed) != 1 || removed[0] != "drop" {
		t.Fatalf("removed = %v, want [drop]", removed)
	}
	if _, ok := l.Entry("payments", "keep"); !ok {
		t.Fatal("a still-declared blueprint must survive pruning")
	}
	// Another stack's entries are not this stack's business.
	if _, ok := l.Entry("billing", "untouched"); !ok {
		t.Fatal("pruning one stack must not touch another")
	}

	// Pruning the last entry removes the stack, so an abandoned stack
	// does not linger as an empty object in a committed file.
	if got := l.Prune("payments", nil); len(got) != 1 {
		t.Fatalf("removed = %v, want [keep]", got)
	}
	if _, ok := l.Stacks["payments"]; ok {
		t.Fatal("a stack with no entries left should be dropped entirely")
	}
}

func TestBlueprintContentDir(t *testing.T) {
	a, err := BlueprintContentDir("sha256:abc")
	if err != nil {
		t.Fatal(err)
	}
	b, err := BlueprintContentDir("sha256:def")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("different content must not share a directory")
	}
	// The scheme is a directory level, so the leaf is bare hex and a
	// second algorithm would be a sibling rather than a rename.
	if filepath.Base(a) != "abc" || filepath.Base(filepath.Dir(a)) != "sha256" {
		t.Fatalf("layout should be sha256/<hex>, got %s", a)
	}
	// Two stacks pinning the same content through different tags share
	// one directory, which a declaration-keyed layout could not do.
	again, _ := BlueprintContentDir("sha256:abc")
	if again != a {
		t.Fatal("the same content hash must map to the same directory")
	}
}
