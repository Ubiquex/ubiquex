package blueprint

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeGoBlueprint(t *testing.T, dir, goMod string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, goModFileName), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestGoBlueprintDeps_SkipsLocalReplaces: a replace to a path on disk
// resolves from the filesystem, so it is neither fetched nor recorded in
// go.sum. Counting one would refuse a blueprint that needs no network at
// all, which is the shape a blueprint developed against a local SDK
// checkout has.
func TestGoBlueprintDeps_SkipsLocalReplaces(t *testing.T) {
	dir := writeGoBlueprint(t, t.TempDir(), `module example.com/bp

go 1.23

require (
	github.com/google/uuid v1.6.0
	github.com/ubiquex/ubx-sdk-go v0.6.0
	example.com/vendored v0.0.0
)

replace example.com/vendored => ./vendored
`)
	got, err := goBlueprintDeps(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"github.com/google/uuid", "github.com/ubiquex/ubx-sdk-go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestGoBlueprintDeps_NoModuleOrNoRequires: a blueprint with nothing to
// fetch must cost nothing. No go.mod at all is not an error, since the
// caller asks this of every blueprint.
func TestGoBlueprintDeps_NoModuleOrNoRequires(t *testing.T) {
	if got, err := goBlueprintDeps(t.TempDir()); err != nil || len(got) != 0 {
		t.Errorf("a directory with no go.mod: got %v, %v", got, err)
	}
	dir := writeGoBlueprint(t, t.TempDir(), "module example.com/bp\n\ngo 1.23\n")
	if got, err := goBlueprintDeps(dir); err != nil || len(got) != 0 {
		t.Errorf("a module requiring nothing: got %v, %v", got, err)
	}
}

// TestCheckGoBlueprintPinned is the boundary, and it is the same one the
// TypeScript side settled on: a graph with a pin that travels under the
// content hash may be fetched, one without has nothing saying what would
// arrive.
func TestCheckGoBlueprintPinned(t *testing.T) {
	const withDeps = `module example.com/bp

go 1.23

require github.com/google/uuid v1.6.0
`

	t.Run("deps and no go.sum is refused", func(t *testing.T) {
		dir := writeGoBlueprint(t, t.TempDir(), withDeps)
		err := checkGoBlueprintPinned(dir, "widget-bp")
		if err == nil {
			t.Fatal("expected a refusal")
		}
		msg := err.Error()
		for _, want := range []string{"widget-bp", "github.com/google/uuid", goSumFileName, "go mod tidy"} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal does not name %q: %s", want, msg)
			}
		}
		// The author has a working module and no reason to think ubx cares
		// about go.sum, so the message has to say why ubx will not just
		// run the command itself. Without that it reads as ubx being
		// unhelpful rather than deliberate.
		if !strings.Contains(msg, "rewrites go.mod") {
			t.Errorf("refusal must say why ubx does not run it: %s", msg)
		}
		// A Linear id means nothing to anyone outside this org.
		if strings.Contains(msg, "UBI-") {
			t.Errorf("CLI output must not cite a ticket id: %s", msg)
		}
	})

	t.Run("deps with a go.sum passes", func(t *testing.T) {
		dir := writeGoBlueprint(t, t.TempDir(), withDeps)
		if err := os.WriteFile(filepath.Join(dir, goSumFileName), []byte("github.com/google/uuid v1.6.0 h1:x=\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := checkGoBlueprintPinned(dir, "bp"); err != nil {
			t.Fatalf("a pinned blueprint must pass: %v", err)
		}
	})

	t.Run("no deps needs no go.sum", func(t *testing.T) {
		dir := writeGoBlueprint(t, t.TempDir(), "module example.com/bp\n\ngo 1.23\n")
		if err := checkGoBlueprintPinned(dir, "bp"); err != nil {
			t.Fatalf("a blueprint importing only the standard library must not be refused: %v", err)
		}
	})

	t.Run("a local replace alone needs no go.sum", func(t *testing.T) {
		dir := writeGoBlueprint(t, t.TempDir(), `module example.com/bp

go 1.23

require example.com/vendored v0.0.0

replace example.com/vendored => ./vendored
`)
		if err := checkGoBlueprintPinned(dir, "bp"); err != nil {
			t.Fatalf("a filesystem replace is not fetched and must not be refused: %v", err)
		}
	})
}

// goModCacheDir is a module cache t.TempDir can actually clean up.
//
// Go writes its module cache read-only on purpose, so TempDir's own
// RemoveAll fails with "permission denied" on the way out and fails the
// test after it has already passed. Made writable before cleanup rather
// than working around it in the code under test.
func goModCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			_ = os.Chmod(p, 0o700)
			return nil
		})
	})
	return dir
}

// TestPrefetchGoBlueprintDeps_EnforcesTheSum is what the pass buys.
//
// Not availability alone: the point is that the bytes are checked against
// the go.sum the blueprint ships before anything runs. Without that,
// evaluation would take whatever the module source served and the
// blueprint's go.sum would be decorative.
//
// Gated because it reaches the real module proxy. `go test ./...` stays
// hermetic, and a faked proxy would only assert that ubx builds the
// command it builds.
func TestPrefetchGoBlueprintDeps_EnforcesTheSum(t *testing.T) {
	if os.Getenv("UBX_GOMOD_LIVE") != "1" {
		t.Skip("skipping: set UBX_GOMOD_LIVE=1 to fetch real modules from the real proxy")
	}
	dir := writeGoBlueprint(t, t.TempDir(), `module example.com/bp

go 1.23

require github.com/google/uuid v1.6.0
`)
	// A source file that actually imports it: `go mod tidy` drops a
	// require nothing uses, so a go.mod alone produces no go.sum.
	if err := os.WriteFile(filepath.Join(dir, "bp.go"),
		[]byte("package bp\n\nimport \"github.com/google/uuid\"\n\nfunc New() string { return uuid.Nil.String() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMODCACHE", goModCacheDir(t))

	// A real go.sum, produced the way the refusal tells an author to.
	cmd := osexec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	good, err := os.ReadFile(filepath.Join(dir, goSumFileName))
	if err != nil {
		t.Fatalf("go mod tidy wrote no %s: %v", goSumFileName, err)
	}

	t.Setenv("GOMODCACHE", goModCacheDir(t))
	if err := prefetchGoBlueprintDeps(context.Background(), dir, "bp"); err != nil {
		t.Fatalf("an honest go.sum must fetch: %v", err)
	}

	// A tampered one must not.
	tampered := strings.Replace(string(good), "h1:", "h1:AAAA", 1)
	if tampered == string(good) {
		t.Fatal("test bug: nothing was tampered with")
	}
	if err := os.WriteFile(filepath.Join(dir, goSumFileName), []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMODCACHE", goModCacheDir(t))
	err = prefetchGoBlueprintDeps(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("bytes that do not match the blueprint's go.sum must be refused")
	}
	if !strings.Contains(err.Error(), `"bp"`) {
		t.Errorf("the refusal should name the blueprint: %v", err)
	}
}
