package server

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// ubxConfigFileNames is the real set of file names a `.ubx/` directory
// can hold its config under -- a verbatim mirror of cli/configcascade.
// go's own configFileCandidates (`config.hcl`, `config.toml`, `config`,
// `config.yaml`, where `config` without an extension is UBI-19's
// original legacy name, kept forever). Duplicated rather than imported
// because the dependency only runs one way: package cli imports package
// server (cli/server.go wires up `ubx server`), so server importing cli
// back would be a real import cycle. Order is irrelevant here -- unlike
// the cascade loader, which uses it as a first-found-wins priority list
// within one directory, discovery only ever asks "does this directory
// declare a stack at all", and any one of these names answers yes.
var ubxConfigFileNames = []string{"config.hcl", "config.toml", "config", "config.yaml"}

// discoverStacks is UBI-167's own real replacement for the ledger_dir
// field Config.Repos entries used to declare by hand: every real stack
// present in repoDir's own checkout, found by walking it for
// directories that actually contain a `.ubx/config*` file.
//
// Returns repo-relative directories (the repo root is "."), sorted, so
// the same checkout always yields the same list in the same order --
// the determinism this codebase already requires of anything feeding a
// decision, not an accident of filesystem walk order.
//
// A stack root is exactly "a directory with a real .ubx/config in it",
// which is the same thing `ubx plan --ledger-dir` has always meant: the
// directory the ledger and `.ubx/` live under. A `.ubx/` directory with
// no config file in it is deliberately NOT a stack -- ubx writes real
// working state (`.ubx/plans/`, `.ubx/ledger.lock`) under `.ubx/` too,
// and a leftover lock file is not a declaration that a stack lives
// here.
//
// Never follows symlinks (fs.WalkDir doesn't), and never descends into
// `.git` (a real repository's own object store has no stacks in it and
// can be very large) or into a `.ubx` directory itself (nothing nests
// below it that this is looking for).
func discoverStacks(repoDir string) ([]string, error) {
	var stacks []string

	err := filepath.WalkDir(repoDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case ".git":
			return fs.SkipDir
		case ".ubx":
			if declaresStack(path) {
				rel, relErr := filepath.Rel(repoDir, filepath.Dir(path))
				if relErr != nil {
					return relErr
				}
				stacks = append(stacks, filepath.Clean(rel))
			}
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover stacks in %s: %w", repoDir, err)
	}

	sort.Strings(stacks)
	return stacks, nil
}

// declaresStack reports whether ubxDir (a real `.ubx` directory) holds
// any one of the real config file names ubx itself recognizes.
func declaresStack(ubxDir string) bool {
	for _, name := range ubxConfigFileNames {
		info, err := os.Stat(filepath.Join(ubxDir, name))
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
