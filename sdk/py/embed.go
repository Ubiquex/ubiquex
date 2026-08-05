// Package pyassets embeds ubx_sdk's own runtime source
// (ubx_sdk/__init__.py) directly into the `ubx` binary, so evaluating a
// Python SDK program never depends on the user having separately
// installed `ubx_sdk` (real, published to PyPI as of UBI-107 -- but
// `ubx`'s own hermetic WASI evaluator never relies on that install
// existing) or on any file living alongside the binary at a predictable
// path. pyeval (the
// Go-side harness, a sibling top-level package) extracts this to a temp
// directory once per process and preopens it into the WASI sandbox at a
// fixed guest path.
//
// go:embed patterns can only reach files at or below the directory of
// the .go file declaring them -- this is why this file lives at sdk/py/
// specifically (the parent of ubx_sdk/), not inside pyeval/ itself,
// which sits outside sdk/py/ entirely and couldn't embed a sibling tree
// via a "../" pattern. Mirrors sdk/ts/embed.go's own precedent exactly.
package pyassets

import "embed"

//go:embed ubx_sdk/__init__.py
var Assets embed.FS
