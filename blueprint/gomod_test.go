package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoModDirectives covers the real shapes a go.mod can arrive in.
//
// The line scanner this replaced understood exactly one of them, the
// single-line require that GenerateGo emits, and its own doc comment
// correctly said so. It was then pointed at a CODE blueprint's go.mod,
// which a person writes and `go mod tidy` reformats into blocks, and
// calling a Go code blueprint failed outright while every test passed,
// because every fixture wrote the shape the scanner wanted.
func TestGoModDirectives(t *testing.T) {
	const sdk = "github.com/ubiquex/ubx-sdk-go"

	for _, tc := range []struct {
		name        string
		goMod       string
		wantRequire string
		wantReplace string
	}{
		{
			// What GenerateGo emits. Still has to work.
			name:        "single-line require",
			goMod:       "module x\n\ngo 1.23\n\nrequire " + sdk + " v0.6.0\n",
			wantRequire: "require " + sdk + " v0.6.0",
		},
		{
			// What `go mod tidy` writes, and what actually broke.
			name:        "block require, one dependency",
			goMod:       "module x\n\ngo 1.23\n\nrequire (\n\t" + sdk + " v0.6.0\n)\n",
			wantRequire: "require " + sdk + " v0.6.0",
		},
		{
			name: "block require, several dependencies, sdk not first",
			goMod: "module x\n\ngo 1.23\n\nrequire (\n" +
				"\tgithub.com/google/uuid v1.6.0\n\t" + sdk + " v0.6.0\n)\n",
			wantRequire: "require " + sdk + " v0.6.0",
		},
		{
			// `go mod tidy` marks transitive dependencies this way, and a
			// blueprint's own SDK dependency can legitimately end up in an
			// indirect block if the author imports it only through another
			// package. The directive is still real.
			name: "indirect comment",
			goMod: "module x\n\ngo 1.23\n\nrequire (\n" +
				"\t" + sdk + " v0.6.0 // indirect\n)\n",
			wantRequire: "require " + sdk + " v0.6.0",
		},
		{
			name: "block replace alongside a block require",
			goMod: "module x\n\ngo 1.23\n\nrequire (\n\t" + sdk + " v0.0.0\n)\n\n" +
				"replace (\n\t" + sdk + " => /local/sdk/go\n)\n",
			wantRequire: "require " + sdk + " v0.0.0",
			wantReplace: "replace " + sdk + " => /local/sdk/go",
		},
		{
			name: "single-line replace",
			goMod: "module x\n\ngo 1.23\n\nrequire " + sdk + " v0.0.0\n\n" +
				"replace " + sdk + " => ../sdk/go\n",
			wantRequire: "require " + sdk + " v0.0.0",
			wantReplace: "replace " + sdk + " => ../sdk/go",
		},
		{
			// A replace onto a different module AND version, which the
			// format allows and which must round-trip both halves.
			name: "replace with a version",
			goMod: "module x\n\ngo 1.23\n\nrequire " + sdk + " v0.0.0\n\n" +
				"replace " + sdk + " => example.com/fork v1.2.3\n",
			wantRequire: "require " + sdk + " v0.0.0",
			wantReplace: "replace " + sdk + " => example.com/fork v1.2.3",
		},
		{
			// A replace for a DIFFERENT module must not be picked up.
			name: "unrelated replace is ignored",
			goMod: "module x\n\ngo 1.23\n\nrequire " + sdk + " v0.6.0\n\n" +
				"replace github.com/google/uuid => ../uuid\n",
			wantRequire: "require " + sdk + " v0.6.0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "go.mod")
			if err := os.WriteFile(path, []byte(tc.goMod), 0o644); err != nil {
				t.Fatal(err)
			}
			req, rep, err := goModDirectives(path, sdk)
			if err != nil {
				t.Fatalf("goModDirectives: %v", err)
			}
			if req != tc.wantRequire {
				t.Errorf("require = %q, want %q", req, tc.wantRequire)
			}
			if rep != tc.wantReplace {
				t.Errorf("replace = %q, want %q", rep, tc.wantReplace)
			}
		})
	}
}

// TestGoModDirectives_MissingRequire_NamesTheFile: the original message
// said "go.mod has no require line" with no indication of WHICH go.mod,
// at the one moment the reader's own directory is the obvious wrong
// guess. An HCL stack calling a Go blueprint has no go.mod at all, so
// the natural reading was that it needed one, which would have meant an
// HCL author needing a Go toolchain and a module they never wrote.
func TestGoModDirectives_MissingRequire_NamesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(path, []byte("module x\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := goModDirectives(path, "github.com/ubiquex/ubx-sdk-go")
	if err == nil {
		t.Fatal("expected an error for a go.mod that requires nothing")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error does not name the file it read, so the reader cannot tell whose go.mod is meant: %v", err)
	}
	if !strings.Contains(err.Error(), "blueprint's own go.mod") {
		t.Errorf("error does not say whose go.mod this is: %v", err)
	}
}

// TestGoModDirectives_Unparseable_ReportsTheFile: a malformed go.mod is
// a real authoring mistake, and the parser's own message is more useful
// than anything reconstructed here, so it is wrapped rather than
// replaced.
func TestGoModDirectives_Unparseable_ReportsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(path, []byte("module x\n\nrequire (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := goModDirectives(path, "github.com/ubiquex/ubx-sdk-go")
	if err == nil {
		t.Fatal("expected a parse error for an unterminated require block")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("parse error does not name the file: %v", err)
	}
}
