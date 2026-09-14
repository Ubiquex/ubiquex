package tseval

import (
	"fmt"
	"os"
	"path/filepath"
)

// lock.go keeps evaluation from writing to the author's deno.lock.
//
// deno discovers a project's deno.lock on its own and UPDATES it as a
// side effect of running: evaluating a program that resolves anything
// not already recorded there rewrites the file. `ubx plan` and `ubx
// resolve` read a stack; they should not leave a git diff behind. This
// is the same failure goeval already fixed one language over, where the
// build mutated the author's go.mod, and the fix has the same shape:
// carry the real file's CONTENT into a copy ubx owns, let the tool write
// to the copy, and leave the original alone.
//
// Copying rather than passing --no-lock, deliberately. --no-lock would
// stop the mutation by dropping the pinning with it, so a stack that
// pinned its own dependencies would silently stop having them verified
// during evaluation. The copy keeps every entry the author committed and
// enforces it exactly as before; only the write lands somewhere
// disposable.

// lockFileName is deno's own name for it.
const lockFileName = "deno.lock"

// findLockFile walks up from dir looking for a lock, mirroring deno's
// own upward discovery. Returns "" when there is none, which is the
// ordinary case for a stack that depends on nothing beyond the runtime.
func findLockFile(dir string) string {
	for {
		candidate := filepath.Join(dir, lockFileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// writeThrowawayLock returns a lock path for one evaluation to write to,
// seeded with the project's own lock when it has one.
//
// The path is inside a temp directory rather than being a temp file,
// because when the project has no lock there must be nothing there for
// deno to parse: an empty file is not valid JSON, while an absent one is
// simply a lock deno creates.
func writeThrowawayLock(entryDir string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ubx-tseval-lock-*")
	if err != nil {
		return "", nil, fmt.Errorf("tseval: lock: %w", err)
	}
	cleanup := func() { os.RemoveAll(dir) }
	path := filepath.Join(dir, lockFileName)

	if src := findLockFile(entryDir); src != "" {
		data, err := os.ReadFile(src)
		if err != nil {
			// Unreadable is not fatal: the project is no worse off than
			// it would be with no lock, and failing an evaluation over a
			// file deno would have simply rewritten is the wrong trade.
			return path, cleanup, nil
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("tseval: lock: %w", err)
		}
	}
	return path, cleanup, nil
}
