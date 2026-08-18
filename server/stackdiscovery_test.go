package server

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeStack creates a real .ubx/<configName> file at dir (relative to
// repoDir), which is exactly what makes that directory a real stack as
// far as ubx itself is concerned.
func writeStack(t *testing.T, repoDir, dir, configName string) {
	t.Helper()
	ubxDir := filepath.Join(repoDir, dir, ".ubx")
	if err := os.MkdirAll(ubxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ubxDir, configName), []byte("stack = \"payments\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResolveStackIn_SingleRealConfigDiscovered is UBI-167's own core
// case: an allowlisted repository with one real .ubx/config is
// discovered and resolved with no declared path anywhere, and without
// the changed-files fetch ever being made (there is nothing to
// disambiguate).
func TestResolveStackIn_SingleRealConfigDiscovered(t *testing.T) {
	repoDir := t.TempDir()
	writeStack(t, repoDir, "stacks/payments", "config")

	fetchCalled := false
	dir, err := resolveStackIn(repoDir, func() ([]string, error) {
		fetchCalled = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("resolveStackIn: %v", err)
	}
	if dir != "stacks/payments" {
		t.Errorf("dir = %q, want %q -- the stack's real location must come from the repo's own .ubx/config, never a declared path", dir, "stacks/payments")
	}
	if fetchCalled {
		t.Error("expected no changed-files fetch for a single discovered stack")
	}
}

// TestResolveStackIn_TwoRealStacksResolvedByDeepestMatch proves
// UBI-166's deepest-matching-stack-wins logic is genuinely unchanged
// by UBI-167, just fed auto-discovered candidates: each PR resolves to
// the stack its own changed files actually touch.
func TestResolveStackIn_TwoRealStacksResolvedByDeepestMatch(t *testing.T) {
	repoDir := t.TempDir()
	writeStack(t, repoDir, "stacks/payments", "config")
	writeStack(t, repoDir, "stacks/networking", "config.toml")

	dir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return []string{"stacks/payments/proposals/db-replica.json"}, nil
	})
	if err != nil {
		t.Fatalf("resolveStackIn (payments PR): %v", err)
	}
	if dir != "stacks/payments" {
		t.Errorf("dir = %q, want %q", dir, "stacks/payments")
	}

	dir, err = resolveStackIn(repoDir, func() ([]string, error) {
		return []string{"stacks/networking/proposals/vpc.json"}, nil
	})
	if err != nil {
		t.Fatalf("resolveStackIn (networking PR): %v", err)
	}
	if dir != "stacks/networking" {
		t.Errorf("dir = %q, want %q -- each discovered stack must resolve independently", dir, "stacks/networking")
	}
}

// TestResolveStackIn_RootAndNestedDiscovered covers the real, legitimate
// layout of a repo-root stack plus a nested one, both auto-discovered.
func TestResolveStackIn_RootAndNestedDiscovered(t *testing.T) {
	repoDir := t.TempDir()
	writeStack(t, repoDir, ".", "config.hcl")
	writeStack(t, repoDir, "stacks/payments", "config")

	dir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return []string{"stacks/payments/proposals/db-replica.json"}, nil
	})
	if err != nil {
		t.Fatalf("resolveStackIn: %v", err)
	}
	if dir != "stacks/payments" {
		t.Errorf("dir = %q, want %q -- the nested, more specific discovered stack must win", dir, "stacks/payments")
	}

	dir, err = resolveStackIn(repoDir, func() ([]string, error) {
		return []string{"README.md"}, nil
	})
	if err != nil {
		t.Fatalf("resolveStackIn: %v", err)
	}
	if dir != "." {
		t.Errorf("dir = %q, want %q", dir, ".")
	}
}

// TestResolveStackIn_AmbiguousDiscoveredStacksRefused proves the
// "never guess" property survives the switch to auto-discovery: a PR
// touching two equally-specific discovered stacks is refused, not
// resolved to whichever the walk happened to find first.
func TestResolveStackIn_AmbiguousDiscoveredStacksRefused(t *testing.T) {
	repoDir := t.TempDir()
	writeStack(t, repoDir, "stacks/payments", "config")
	writeStack(t, repoDir, "stacks/networking", "config")

	_, err := resolveStackIn(repoDir, func() ([]string, error) {
		return []string{
			"stacks/payments/proposals/db-replica.json",
			"stacks/networking/proposals/vpc.json",
		}, nil
	})
	if !errors.Is(err, ErrAmbiguousStack) {
		t.Fatalf("err = %v, want ErrAmbiguousStack", err)
	}
}

// TestResolveStackIn_NoConfigAnywhereRefused proves an allowlisted
// repository that declares no stack at all is refused on its own real,
// distinct terms rather than silently falling back to ledger_dir "."
// the way a pre-UBI-166 deployment did.
func TestResolveStackIn_NoConfigAnywhereRefused(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("no stacks here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveStackIn(repoDir, func() ([]string, error) {
		return []string{"README.md"}, nil
	})
	if !errors.Is(err, ErrNoStackDiscovered) {
		t.Fatalf("err = %v, want ErrNoStackDiscovered", err)
	}
}

// TestDiscoverStacks_UbxDirWithoutConfigIsNotAStack is the real,
// adversarial case behind keying discovery on the config file rather
// than on the `.ubx` directory itself: ubx writes real working state
// (`.ubx/plans/`, `.ubx/ledger.lock`) under `.ubx/` too, and a leftover
// lock file must never be read as "a stack lives here".
func TestDiscoverStacks_UbxDirWithoutConfigIsNotAStack(t *testing.T) {
	repoDir := t.TempDir()
	writeStack(t, repoDir, "stacks/payments", "config")
	if err := os.MkdirAll(filepath.Join(repoDir, "stacks/leftover/.ubx/plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "stacks/leftover/.ubx/ledger.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	stacks, err := discoverStacks(repoDir)
	if err != nil {
		t.Fatalf("discoverStacks: %v", err)
	}
	if !reflect.DeepEqual(stacks, []string{"stacks/payments"}) {
		t.Errorf("stacks = %v, want only [stacks/payments] -- a .ubx directory with no config file declares no stack", stacks)
	}
}

// TestDiscoverStacks_SkipsGitDirAndIsDeterministic covers the two real
// properties the walk itself has to hold: a repository's own .git
// object store is never searched (it holds no stacks and can be very
// large), and the same checkout always yields the same order.
func TestDiscoverStacks_SkipsGitDirAndIsDeterministic(t *testing.T) {
	repoDir := t.TempDir()
	writeStack(t, repoDir, "stacks/networking", "config")
	writeStack(t, repoDir, "stacks/payments", "config")
	writeStack(t, repoDir, ".", "config")
	// A real .git directory that happens to contain something shaped
	// like a stack must never be discovered as one.
	writeStack(t, repoDir, ".git/modules/vendored", "config")

	want := []string{".", "stacks/networking", "stacks/payments"}
	for i := 0; i < 3; i++ {
		stacks, err := discoverStacks(repoDir)
		if err != nil {
			t.Fatalf("discoverStacks: %v", err)
		}
		if !reflect.DeepEqual(stacks, want) {
			t.Fatalf("stacks = %v, want %v (run %d)", stacks, want, i)
		}
	}
}

// TestDiscoverStacks_EveryRealConfigFileNameCounts pins discovery to
// the same four real file names cli/configcascade.go's own cascade
// loader recognizes -- a repo using any one of them is a real stack.
func TestDiscoverStacks_EveryRealConfigFileNameCounts(t *testing.T) {
	for _, name := range ubxConfigFileNames {
		repoDir := t.TempDir()
		writeStack(t, repoDir, "stacks/payments", name)

		stacks, err := discoverStacks(repoDir)
		if err != nil {
			t.Fatalf("discoverStacks (%s): %v", name, err)
		}
		if !reflect.DeepEqual(stacks, []string{"stacks/payments"}) {
			t.Errorf("stacks = %v for .ubx/%s, want [stacks/payments]", stacks, name)
		}
	}
}
