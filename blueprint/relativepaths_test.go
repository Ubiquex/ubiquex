package blueprint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/oci"
)

// push resolved a relative tarball path against the wrong base, one
// directory too high: from a/b, pushing "../x.tar.gz" looked for
// "a/../x.tar.gz".
//
// The cause is that file.New roots a store at a directory and
// file.Store.Add resolves a relative path against that same root, so
// passing Dir(p) to one and p to the other applies the directory
// component twice. It only ever appeared to work by coincidence: a bare
// filename has no directory component to double, and "../sub/x" doubles
// to "../sub/../sub/x", which path cleaning collapses back to the right
// answer.

// chdir moves into dir for one test, restoring afterwards. Relative
// paths only mean anything against a working directory, so these tests
// have to establish one.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

func TestPushToTarget_AcceptsEveryRelativePathShape(t *testing.T) {
	ctx := context.Background()

	// Each case places the tarball somewhere under the root, picks a
	// working directory, and names the path a user would type from
	// there to reach it.
	for _, c := range []struct {
		name    string
		tarRel  string // where the tarball really is, relative to the root
		workRel string // where the command runs from
		arg     string // the path as typed; empty means use the absolute one
	}{
		// The reported shape: one level up.
		{"parent directory", "work/bp.tar.gz", "work/deep", "../bp.tar.gz"},
		// Doubling cannot cancel, so this failed too.
		{"subdirectory", "work/bp.tar.gz", ".", "work/bp.tar.gz"},
		// Doubling cannot cancel here either, despite looking like the
		// case below: "../../work" doubled cleans to "../../../work",
		// which lands outside the root entirely.
		{"up twice then down", "work/bp.tar.gz", "work/deep", "../../work/bp.tar.gz"},
		// The one shape that survived. "../sib" doubled is
		// "../sib/../sib", which path cleaning collapses back to
		// "../sib", so it found the file by coincidence rather than by
		// resolving correctly.
		{"sibling directory", "work/sib/bp.tar.gz", "work/deep", "../sib/bp.tar.gz"},
		// Never had a directory component to double.
		{"bare filename", "work/bp.tar.gz", "work", "bp.tar.gz"},
		{"explicit current directory", "work/bp.tar.gz", "work", "./bp.tar.gz"},
		// Always worked, and is what the reporter fell back to.
		{"absolute", "work/bp.tar.gz", "work/deep", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			src := writeSampleBuiltBlueprint(t)
			for _, d := range []string{"work/deep", "work/sib"} {
				if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			tarPath := filepath.Join(root, filepath.FromSlash(c.tarRel))
			m, err := Package(ctx, src, tarPath)
			if err != nil {
				t.Fatalf("Package: %v", err)
			}
			target, err := oci.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}

			chdir(t, filepath.Join(root, filepath.FromSlash(c.workRel)))
			arg := c.arg
			if arg == "" {
				arg = tarPath
			}
			if err := pushToTarget(ctx, arg, m, target, "v1"); err != nil {
				t.Fatalf("pushToTarget(%q) from %s: %v", arg, c.workRel, err)
			}
		})
	}
}

// pull was checked for the same class of bug and does not have it: its
// own store is rooted at an absolute scratch directory, and the
// destination is only ever handed to extractTarGz, which resolves
// against the working directory like any other path.
func TestPullFromTarget_AcceptsARelativeDest(t *testing.T) {
	ctx := context.Background()

	root := t.TempDir()
	src := writeSampleBuiltBlueprint(t)
	tarPath := filepath.Join(t.TempDir(), "bp.tar.gz")
	m, err := Package(ctx, src, tarPath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := pushToTarget(ctx, tarPath, m, target, "v1"); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, "work", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, filepath.Join(root, "work", "deep"))

	if err := pullFromTarget(ctx, target, "v1", "../dest"); err != nil {
		t.Fatalf("pullFromTarget with a relative dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "dest", ManifestFileName)); err != nil {
		t.Errorf("pulled to the wrong directory: %v", err)
	}
}

// package was checked too. Its directory argument goes through
// filepath.Abs and its output path straight to os.Create, both of which
// resolve against the working directory correctly.
func TestPackage_AcceptsRelativeDirAndOutput(t *testing.T) {
	ctx := context.Background()

	root := t.TempDir()
	src := writeSampleBuiltBlueprint(t)
	if err := os.MkdirAll(filepath.Join(root, "work", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "work", "deep", "bp")
	if err := copyDir(src, dest); err != nil {
		t.Fatal(err)
	}

	chdir(t, filepath.Join(root, "work", "deep"))
	if _, err := Package(ctx, "./bp", "../out.tar.gz"); err != nil {
		t.Fatalf("Package with relative paths: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "out.tar.gz")); err != nil {
		t.Errorf("packaged to the wrong directory: %v", err)
	}
}
