// Package tsassets embeds the TypeScript files ubx's own evaluator
// harness needs to run at all -- the eager nondeterminism guards
// (evaluator/guards.ts) and the @ubx/sdk runtime itself
// (runtime/src/index.ts) -- directly into the `ubx` binary, so
// evaluating an SDK program never depends on the user having separately
// installed `@ubx/sdk` (via npm, not yet even published -- UBI-34's own
// "Out of scope") or on any file living alongside the binary at a
// predictable path. tseval (the Go-side harness, a sibling top-level
// package) extracts these to a temp directory once per process and
// wires them in via a generated import map.
//
// go:embed patterns can only reach files at or below the directory of
// the .go file declaring them -- this is why this file lives at
// sdk/ts/ specifically (the nearest common ancestor of evaluator/ and
// runtime/src/), not inside tseval/ itself, which sits outside sdk/ts/
// entirely and couldn't embed a sibling tree via a "../" pattern.
package tsassets

import "embed"

//go:embed evaluator/guards.ts runtime/src/index.ts
var Assets embed.FS
