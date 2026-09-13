package blueprint

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// extractTarGz guards against a tarball naming a path outside the
// destination. The guard was comparing paths that could be rewritten
// into a different spelling of themselves: Clean(".") is ".", and
// Join(".", "x") is "x", so for a destination of "." the destination
// stopped being a prefix of its own entries and every legitimate file
// was reported as escaping.
//
// The half of this that matters is the second test. A correctness bug
// inside a security check invites loosening the check, so the fix has
// to be shown to leave it at least as strict.

// writeTestTarGz writes a gzipped tar containing exactly the named
// entries, so an entry name can be anything a malicious tarball might
// carry rather than only what writeTarGz produces.
func writeTestTarGz(t *testing.T, path string, names ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for _, name := range names {
		body := []byte("content of " + name + "\n")
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTarGz_AcceptsEverySpellingOfTheDestination(t *testing.T) {
	// Each of these is a legitimate extraction that the guard must
	// allow. "." and "./" and "" all clean to ".", which is where it
	// failed.
	for _, dest := range []string{".", "./", "", "sub", "./sub", "../sibling"} {
		t.Run("dest="+dest, func(t *testing.T) {
			root := t.TempDir()
			tarPath := filepath.Join(root, "bp.tar.gz")
			writeTestTarGz(t, tarPath, "blueprint.lock.json", "nested/deep/file.txt")

			work := filepath.Join(root, "work", "here")
			if err := os.MkdirAll(work, 0o755); err != nil {
				t.Fatal(err)
			}
			old, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			defer os.Chdir(old)
			if err := os.Chdir(work); err != nil {
				t.Fatal(err)
			}
			if dest != "" && dest != "." && dest != "./" {
				if err := os.MkdirAll(dest, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			if err := extractTarGz(tarPath, dest); err != nil {
				t.Fatalf("extractTarGz into %q: %v", dest, err)
			}

			where := dest
			if where == "" {
				where = "."
			}
			for _, rel := range []string{"blueprint.lock.json", "nested/deep/file.txt"} {
				if _, err := os.Stat(filepath.Join(where, filepath.FromSlash(rel))); err != nil {
					t.Errorf("%s was not extracted: %v", rel, err)
				}
			}
		})
	}
}

// The guard still refuses everything it exists to refuse. Without this,
// the fix above could have been a loosening rather than a correction.
func TestExtractTarGz_StillRefusesEscapingEntries(t *testing.T) {
	for _, c := range []struct {
		name  string
		entry string
	}{
		{"parent directory", "../escaped.txt"},
		{"several levels up", "../../../escaped.txt"},
		{"traversal hidden mid-path", "nested/../../escaped.txt"},
		{"traversal after a legitimate prefix", "ok/../../../escaped.txt"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			tarPath := filepath.Join(root, "evil.tar.gz")
			writeTestTarGz(t, tarPath, c.entry)

			dest := filepath.Join(root, "work", "dest")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}

			// Through the relative destination that used to break, so
			// this proves the fix did not turn the check off for exactly
			// the shape it now has to handle.
			old, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			defer os.Chdir(old)
			if err := os.Chdir(dest); err != nil {
				t.Fatal(err)
			}

			err = extractTarGz(tarPath, ".")
			if err == nil {
				t.Fatalf("entry %q was extracted instead of refused", c.entry)
			}
			if !strings.Contains(err.Error(), "escapes the destination directory") {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, "escaped.txt")); statErr == nil {
				t.Error("the escaping file was written outside the destination")
			}
		})
	}
}

// An absolute entry name is refused rather than relocated. Join would
// neutralise the leading separator and quietly place "/etc/passwd" at
// <dest>/etc/passwd, which is contained but is not the file the tarball
// named. writeTarGz never emits such a name, so one is foreign or
// tampered and saying so beats silently accepting a rewritten version.
func TestExtractTarGz_RefusesAnAbsoluteEntryName(t *testing.T) {
	root := t.TempDir()
	tarPath := filepath.Join(root, "abs.tar.gz")
	writeTestTarGz(t, tarPath, "/etc/passwd")

	dest := filepath.Join(root, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	err := extractTarGz(tarPath, dest)
	if err == nil {
		t.Fatal("an absolute entry name was accepted")
	}
	if !strings.Contains(err.Error(), "escapes the destination directory") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "etc", "passwd")); statErr == nil {
		t.Error("the entry was relocated into the destination instead of refused")
	}
}
