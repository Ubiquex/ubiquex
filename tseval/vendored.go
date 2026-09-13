package tseval

import (
	"embed"
	"io/fs"
)

// vendored.go embeds the TypeScript files this evaluator needs, from a
// copy that lives inside this repository (UBI-254).
//
// They really live in ubx-sdk-typescript, checked out here as the
// sdk/ts submodule, and this package used to import that submodule's
// own Go package (github.com/ubiquex/ubiquex/sdk/ts) directly. That
// worked from a working tree and nowhere else: the Go module proxy zips
// a repository WITHOUT submodule contents, so every published version
// of this module was missing sdk/ts and sdk/py entirely, and
// github.com/ubiquex/ubiquex/blueprint could not be built by any
// consumer outside this repository.
//
// A copy is a real cost and is only defensible because
// vendored_test.go proves the two agree, running for real in CI where
// the submodule is checked out. The submodule stays the source: to
// change these files, change them there and run `make vendor-assets`.
//
// The alternative was making ubx-sdk-typescript and ubx-sdk-python into
// published Go modules, which removes the duplication entirely but
// makes a TypeScript package and a Python package answer to Go's own
// release model forever. The Go embed file is already the odd thing
// living in those repositories; that option doubles down on it.

// all: is load-bearing, not decoration. go:embed skips any file whose
// name begins with "_" or ".", and the Python runtime this embeds is
// literally ubx_sdk/__init__.py, so a bare "//go:embed vendored"
// refuses to compile with "contains no embeddable files". Kept on both
// evaluators so the two directories behave identically rather than one
// of them being quietly special.
//
//go:embed all:vendored
var vendoredFS embed.FS

// assets is rooted so the paths embeddedFiles names ("evaluator/
// guards.ts", "runtime/src/index.ts") keep working unchanged, which is
// what they were before the copy existed.
var assets = func() fs.FS {
	sub, err := fs.Sub(vendoredFS, "vendored")
	if err != nil {
		// Unreachable: the directory is embedded at compile time, so a
		// failure here means the binary was built without it.
		panic("tseval: embedded assets are missing from this binary: " + err.Error())
	}
	return sub
}()
