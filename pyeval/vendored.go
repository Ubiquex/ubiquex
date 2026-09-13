package pyeval

import (
	"embed"
	"io/fs"
)

// vendored.go embeds the ubx_sdk runtime source this evaluator needs,
// from a copy that lives inside this repository (UBI-254).
//
// It really lives in ubx-sdk-python, checked out here as the sdk/py
// submodule. See tseval/vendored.go for the full account: the short
// version is that the Go module proxy zips a repository without
// submodule contents, so importing this module's own blueprint package
// from outside this repository failed to build.
//
// The submodule stays the source. To change this file, change it there
// and run `make vendor-assets`; vendored_test.go fails if the two ever
// disagree.

// all: is load-bearing, not decoration. go:embed skips any file whose
// name begins with "_" or ".", and the Python runtime this embeds is
// literally ubx_sdk/__init__.py, so a bare "//go:embed vendored"
// refuses to compile with "contains no embeddable files". Kept on both
// evaluators so the two directories behave identically rather than one
// of them being quietly special.
//
//go:embed all:vendored
var vendoredFS embed.FS

// assets is rooted so "ubx_sdk/__init__.py" keeps resolving exactly as
// it did when this read from the submodule's own package.
var assets = func() fs.FS {
	sub, err := fs.Sub(vendoredFS, "vendored")
	if err != nil {
		panic("pyeval: embedded assets are missing from this binary: " + err.Error())
	}
	return sub
}()
