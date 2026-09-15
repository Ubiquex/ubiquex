package blueprint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// TestCallIsLocal follows Pull's own dispatch, which is the only rule
// that decides what actually happens to a source.
//
// PyDependency's isLocalSource asks whether a URL carries a scheme,
// which is right for a requirements.txt entry and wrong here: an HCL
// call's "file:///path" carries one and is handled by Pull as a GIT
// source, because os.Stat fails on a URL and the git branch takes
// everything that reaches it. Classifying by scheme would call that
// local and then fetch it as git.
func TestCallIsLocal(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "bp.tar.gz")
	if err := os.WriteFile(tarball, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		source string
		local  bool
	}{
		{dir, true},
		{tarball, true},
		{"oci://ghcr.io/acme/bp:v1", false},
		{"https://github.com/acme/bp.git", false},
		// A URL, not a path: os.Stat fails and Pull clones it.
		{"file:///not/a/real/path", false},
		{filepath.Join(dir, "does-not-exist"), false},
	} {
		if got := callIsLocal(resolver.BlueprintCall{Blueprint: tc.source}); got != tc.local {
			t.Errorf("callIsLocal(%q) = %v, want %v", tc.source, got, tc.local)
		}
	}
}

// TestResolveCallBlueprints_LocalIsNotLocked is the line this change
// draws, and it is principled rather than a compromise.
//
// The lock exists because a REFERENCE can be repointed under you: an OCI
// tag moved, a branch advanced, a registry serving different bytes. A
// local path is not a reference to anything that can move; it names a
// directory, and whatever is in it is what you are calling.
//
// It also matters that a local blueprint is usually one the author is
// editing. The documented HCL example calls ../blueprints/postgres, and
// locking that would turn every edit into a refusal demanding
// --update-lock.
func TestResolveCallBlueprints_LocalIsNotLocked(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blueprint.go"), []byte("package bp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := WithLockPolicy(context.Background(), LockPolicy{Stack: "payments", Mode: LockWrite, LedgerDir: t.TempDir()})
	resolved, _, err := resolveCallBlueprints(ctx, []resolver.BlueprintCall{
		{Name: "bp call", Blueprint: dir},
	})
	if err != nil {
		t.Fatalf("a local call must resolve without being locked: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("a local call must not be resolved through the content store, got %+v", resolved)
	}
}

// TestResolveCallBlueprints_RefusesTwoSourcesUnderOneName is the one
// shape that worked before and does not now, so it is named precisely.
//
// A blueprint's name comes from its source, and the lock records one
// content hash per name per stack. Two sources deriving the same name
// have no representation in it, and silently locking whichever resolved
// last would make the lock describe half the document.
func TestResolveCallBlueprints_RefusesTwoSourcesUnderOneName(t *testing.T) {
	ctx := WithLockPolicy(context.Background(), LockPolicy{Stack: "payments", Mode: LockWrite, LedgerDir: t.TempDir()})
	_, _, err := resolveCallBlueprints(ctx, []resolver.BlueprintCall{
		{Name: "a", Blueprint: "oci://ghcr.io/acme/ci-platform:v1"},
		{Name: "b", Blueprint: "oci://ghcr.io/other/ci-platform:v2"},
	})
	if err == nil {
		t.Fatal("two sources deriving one blueprint name must be refused")
	}
	msg := err.Error()
	for _, want := range []string{"ci-platform", "ghcr.io/acme", "ghcr.io/other"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal should name %q so the conflict is visible: %s", want, msg)
		}
	}
	// It must not read as "you cannot call a blueprint twice", which is
	// fine and common.
	if !strings.Contains(msg, "Calling the same blueprint twice is") {
		t.Errorf("the refusal should say what IS allowed: %s", msg)
	}
}

// TestCallDeclaration_KeepsTheSourceVerbatim: Pull takes an HCL call's
// source exactly as written, so anything reshaped here would be
// classified by one rule and fetched by another.
func TestCallDeclaration_KeepsTheSourceVerbatim(t *testing.T) {
	for _, src := range []string{"oci://ghcr.io/acme/bp:v1", "file:///x/bp", "../blueprints/bp"} {
		if got := callDeclaration(resolver.BlueprintCall{Blueprint: src}).Source; got != src {
			t.Errorf("callDeclaration(%q).Source = %q, want it unchanged", src, got)
		}
	}
}
