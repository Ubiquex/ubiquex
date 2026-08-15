package server

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"
)

// captureSlog redirects the real, default slog logger into an
// in-memory buffer for the duration of a test -- UBI-166's own
// "refusal must be loud, never a silent no-op" requirement means the
// real, structured log line itself is the thing under test here, not
// just the return value.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	return &buf
}

func TestResolveLedgerDir_ZeroCandidatesRefused(t *testing.T) {
	_, err := resolveLedgerDir(nil, nil)
	if !errors.Is(err, ErrRepoNotAllowed) {
		t.Fatalf("err = %v, want ErrRepoNotAllowed", err)
	}
}

func TestResolveLedgerDir_OneCandidateUsedDirectly(t *testing.T) {
	candidates := []RepoConfig{{LedgerDir: "stacks/payments"}}
	// changedFiles is deliberately nil -- a single candidate must never
	// need to inspect it at all.
	dir, err := resolveLedgerDir(candidates, nil)
	if err != nil {
		t.Fatalf("resolveLedgerDir: %v", err)
	}
	if dir != "stacks/payments" {
		t.Errorf("dir = %q, want %q", dir, "stacks/payments")
	}
}

// TestResolveLedgerDir_TwoSiblingStacksResolvedIndependently proves
// UBI-166's own real fix for the former "first Config.Repos entry
// silently wins" bug: two real, independently configured stacks in the
// same repository (neither a prefix of the other) each resolve
// correctly based on which stack's own files a given PR actually
// touches.
func TestResolveLedgerDir_TwoSiblingStacksResolvedIndependently(t *testing.T) {
	candidates := []RepoConfig{
		{LedgerDir: "stacks/payments"},
		{LedgerDir: "stacks/networking"},
	}

	dir, err := resolveLedgerDir(candidates, []string{"stacks/payments/proposals/db-replica.json"})
	if err != nil {
		t.Fatalf("resolveLedgerDir (payments PR): %v", err)
	}
	if dir != "stacks/payments" {
		t.Errorf("dir = %q, want %q -- a payments-only PR must resolve to the payments stack, never the first-listed entry", dir, "stacks/payments")
	}

	dir, err = resolveLedgerDir(candidates, []string{"stacks/networking/proposals/vpc.json"})
	if err != nil {
		t.Fatalf("resolveLedgerDir (networking PR): %v", err)
	}
	if dir != "stacks/networking" {
		t.Errorf("dir = %q, want %q -- a networking-only PR must resolve to the networking stack independently of listing order", dir, "stacks/networking")
	}
}

// TestResolveLedgerDir_RootAndNestedStack_MostSpecificWins proves a
// real, legitimate repo-root stack ("." ledger_dir) coexisting with a
// real, nested subdirectory stack is not treated as a misconfiguration
// -- the more specific, nested stack wins for a changed file under its
// own subtree.
func TestResolveLedgerDir_RootAndNestedStack_MostSpecificWins(t *testing.T) {
	candidates := []RepoConfig{
		{LedgerDir: "."},
		{LedgerDir: "stacks/payments"},
	}

	dir, err := resolveLedgerDir(candidates, []string{"stacks/payments/proposals/db-replica.json"})
	if err != nil {
		t.Fatalf("resolveLedgerDir: %v", err)
	}
	if dir != "stacks/payments" {
		t.Errorf("dir = %q, want %q -- the nested, more specific stack must win over the repo-root stack", dir, "stacks/payments")
	}

	dir, err = resolveLedgerDir(candidates, []string{"README.md"})
	if err != nil {
		t.Fatalf("resolveLedgerDir: %v", err)
	}
	if dir != "." {
		t.Errorf("dir = %q, want %q -- a file outside the nested stack falls back to the real repo-root stack", dir, ".")
	}
}

func TestResolveLedgerDir_NoMatchingFiles_Refused(t *testing.T) {
	candidates := []RepoConfig{
		{LedgerDir: "stacks/payments"},
		{LedgerDir: "stacks/networking"},
	}
	_, err := resolveLedgerDir(candidates, []string{"docs/README.md"})
	if !errors.Is(err, ErrAmbiguousStack) {
		t.Fatalf("err = %v, want ErrAmbiguousStack -- a change touching neither configured stack must never guess one", err)
	}
}

func TestResolveLedgerDir_EquallySpecificTie_Refused(t *testing.T) {
	candidates := []RepoConfig{
		{LedgerDir: "stacks/payments"},
		{LedgerDir: "stacks/networking"},
	}
	// A real PR touching both configured stacks at once -- genuinely
	// ambiguous, never guessed either direction.
	_, err := resolveLedgerDir(candidates, []string{
		"stacks/payments/proposals/db-replica.json",
		"stacks/networking/proposals/vpc.json",
	})
	if !errors.Is(err, ErrAmbiguousStack) {
		t.Fatalf("err = %v, want ErrAmbiguousStack -- changes touching two equally-specific stacks at once must never guess one", err)
	}
}

func TestResolveLedgerDirLazy_SkipsFetchWhenUnambiguous(t *testing.T) {
	fetchCalled := false
	fetch := func() ([]string, error) {
		fetchCalled = true
		return nil, nil
	}

	if _, err := resolveLedgerDirLazy(nil, fetch); !errors.Is(err, ErrRepoNotAllowed) {
		t.Fatalf("err = %v, want ErrRepoNotAllowed", err)
	}
	if fetchCalled {
		t.Error("expected fetchChangedFiles to never be called for zero candidates")
	}

	dir, err := resolveLedgerDirLazy([]RepoConfig{{LedgerDir: "."}}, fetch)
	if err != nil {
		t.Fatalf("resolveLedgerDirLazy: %v", err)
	}
	if dir != "." {
		t.Errorf("dir = %q, want %q", dir, ".")
	}
	if fetchCalled {
		t.Error("expected fetchChangedFiles to never be called for exactly one candidate -- the common, single-stack-per-repository case must stay free")
	}
}

func TestResolveLedgerDirLazy_FetchesOnlyWhenAmbiguous(t *testing.T) {
	fetchCalled := false
	candidates := []RepoConfig{{LedgerDir: "stacks/payments"}, {LedgerDir: "stacks/networking"}}
	fetch := func() ([]string, error) {
		fetchCalled = true
		return []string{"stacks/payments/proposals/db-replica.json"}, nil
	}

	dir, err := resolveLedgerDirLazy(candidates, fetch)
	if err != nil {
		t.Fatalf("resolveLedgerDirLazy: %v", err)
	}
	if dir != "stacks/payments" {
		t.Errorf("dir = %q, want %q", dir, "stacks/payments")
	}
	if !fetchCalled {
		t.Error("expected fetchChangedFiles to be called for a genuinely multi-stack repository")
	}
}

func TestPathUnderLedgerDir(t *testing.T) {
	cases := []struct {
		file, ledgerDir string
		want            bool
	}{
		{"stacks/payments/main.tf", "stacks/payments", true},
		{"stacks/payments/nested/deep.tf", "stacks/payments", true},
		{"stacks/networking/main.tf", "stacks/payments", false},
		{"stacks/payments-extra/main.tf", "stacks/payments", false}, // a real, deliberate non-match: sibling directory sharing a name prefix must never falsely match
		{"anything.tf", ".", true},
		{"/stacks/payments/main.tf", "stacks/payments", true}, // a leading slash some APIs include must not break the match
	}
	for _, c := range cases {
		got := pathUnderLedgerDir(c.file, c.ledgerDir)
		if got != c.want {
			t.Errorf("pathUnderLedgerDir(%q, %q) = %v, want %v", c.file, c.ledgerDir, got, c.want)
		}
	}
}
