package pyeval

import (
	"os"
	"path/filepath"
	"sort"
)

// venv.go makes a project's own installed Python packages importable
// inside the sandbox.
//
// The sandbox preopens the embedded runtime and each ExtraDep, and
// nothing else, so a pip-installed distribution was invisible to an
// evaluated program no matter what the host's own PYTHONPATH said.
// docs' own Python hello world (`pip install ubx-sdk-aws`, then
// `from ubx.aws.sqs import Queue`) failed with ModuleNotFoundError for
// that reason, while `ubx init` printed an install instruction for the
// equivalent package (UBI-260).
//
// Discovery is a virtualenv, deliberately, rather than the host's own
// PYTHONPATH. A venv is where `pip install` puts things for the
// overwhelming majority of Python projects, it is a single directory the
// project owns, and it is an explicit act by the person running ubx.
// Mounting whatever the ambient PYTHONPATH happened to name would pull
// arbitrary host directories into the sandbox as a side effect of an
// environment variable, which is the opposite of what this evaluator is
// for.

// venvSitePackages walks up from dir looking for a virtualenv, and
// returns its site-packages directory, or "" when there is none.
//
// Only lib/python*/site-packages is considered (the POSIX layout);
// Windows venvs use Lib/site-packages and are not handled here, matching
// the rest of this package, which already assumes a POSIX wasmtime
// invocation.
func venvSitePackages(dir string) string {
	for {
		for _, name := range []string{".venv", "venv"} {
			if sp := sitePackagesIn(filepath.Join(dir, name)); sp != "" {
				return sp
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// sitePackagesIn returns venvRoot's own site-packages directory, or ""
// when venvRoot is not a virtualenv.
//
// pyvenv.cfg is the marker, not the mere existence of a lib/ directory:
// it is what actually distinguishes a virtualenv from any other
// directory that happens to be called "venv", and every tool that
// creates one writes it.
func sitePackagesIn(venvRoot string) string {
	if _, err := os.Stat(filepath.Join(venvRoot, "pyvenv.cfg")); err != nil {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(venvRoot, "lib", "python*", "site-packages"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	// A venv holds exactly one pythonX.Y directory in practice. Sorted
	// so a directory carrying more than one resolves deterministically
	// rather than by filesystem order, since the choice feeds an
	// evaluation whose output is hashed.
	sort.Strings(matches)
	info, err := os.Stat(matches[0])
	if err != nil || !info.IsDir() {
		return ""
	}
	return matches[0]
}
