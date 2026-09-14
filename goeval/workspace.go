package goeval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// workspace.go carries a program's own Go workspace through the
// throwaway module copy this evaluator builds from.
//
// buildProgram copies the module out of its real directory before
// building, deliberately, so evaluating a program never mutates its
// author's checked-in files. That copy also moves the module out of any
// workspace it belonged to, and a workspace is not incidental context:
// it is how a monorepo says which local directories provide which
// modules. Both real layouts failed because of it, in different ways.
//
// A go.work BESIDE the go.mod is copied along with the module, so
// workspace mode is active in the copy, and the GOFLAGS=-mod=mod this
// evaluator sets is illegal there:
//
//	go: -mod may only be set to readonly or vendor when in workspace
//	mode, but it is set to "mod"
//
// A go.work ABOVE the module root is not copied at all, so the workspace
// silently disappears and every module it provided becomes unresolvable:
//
//	go: finding module for package example.com/lib
//	cannot find module providing package example.com/lib:
//	module lookup disabled by GOPROXY=off
//
// Neither has anything to do with blueprints. A monorepo holding several
// modules under one workspace, which is the ordinary reason to have one,
// could not be evaluated at all.
//
// The fix is the same shape rewriteLocalReplaces already uses for a
// go.mod's own local replace targets: keep the directive, repoint it at
// the original absolute path, and write the result into the build
// directory rather than editing anything the author owns.

// workspaceFileName is Go's own name for it.
const workspaceFileName = "go.work"

// findWorkspace resolves the workspace a build from moduleRoot would
// use, following Go's own rules: GOWORK names one explicitly, "off"
// disables the mechanism, and otherwise it is discovered by walking up.
//
// Returns "" when there is none, which is the ordinary case.
func findWorkspace(moduleRoot string) string {
	switch env := os.Getenv("GOWORK"); {
	case env == "off":
		return ""
	case env != "":
		return env
	}
	dir := moduleRoot
	for {
		candidate := filepath.Join(dir, workspaceFileName)
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

// writeBuildWorkspace writes a go.work into buildDir describing the same
// workspace the program really belongs to, with the module itself
// redirected to its copy.
//
// Every other `use` keeps pointing at its ORIGINAL directory rather than
// being copied too. Copying a whole monorepo to evaluate one program in
// it would be a large cost for no gain: those modules are read, never
// written, and the build already reads the author's own tree for a local
// `replace` target.
//
// `replace` directives are preserved as well as `use`. A merged
// workspace that silently dropped them would be the same failure a
// merged import map was built to avoid, and it would bite exactly the
// monorepo case this fixes: a local `replace` in a workspace is how
// people point at a module that is not published, so dropping it turns a
// working build into "cannot find module providing package" with nothing
// naming the cause.
//
// Returns the written path, or "" when the program is not in a
// workspace at all, in which case nothing changes about the build.
func writeBuildWorkspace(buildDir, moduleRoot, moduleCopy string, blueprintDirs []string) (string, error) {
	workPath := findWorkspace(moduleRoot)
	if workPath == "" && len(blueprintDirs) == 0 {
		return "", nil
	}
	// The copy's use entry is written with symlinks resolved.
	//
	// Not cosmetic. Go matches the build's current directory against the
	// workspace's use list AFTER resolving symlinks, and a temp directory
	// is reached through one on macOS (/var -> /private/var). An entry
	// written as the unresolved path never matches, and the build fails
	// with "current directory is contained in a module that is not one of
	// the workspace modules listed in go.work", which describes the
	// symptom and not the cause.
	copyPath := moduleCopy
	if resolved, err := filepath.EvalSymlinks(moduleCopy); err == nil {
		copyPath = resolved
	}

	// Syntax must be non-nil: modfile builds the file through it, and
	// AddGoStmt dereferences it immediately.
	out := &modfile.WorkFile{Syntax: &modfile.FileSyntax{}}

	absModuleRoot, err := filepath.Abs(moduleRoot)
	if err != nil {
		return "", err
	}
	sawModule := false

	// A program with no workspace of its own still gets one when it
	// declares blueprints, because a workspace is how a blueprint
	// becomes importable without touching the author's go.mod. Its only
	// members are the module copy and the blueprints.
	if workPath != "" {
		data, err := os.ReadFile(workPath)
		if err != nil {
			// A GOWORK pointing at a file that is not there is the
			// author's own misconfiguration, and reporting it is better
			// than building something that silently ignores it.
			return "", fmt.Errorf("read %s: %w", workPath, err)
		}
		wf, err := modfile.ParseWork(workPath, data, nil)
		if err != nil {
			return "", fmt.Errorf("parse %s: %w", workPath, err)
		}
		workDir := filepath.Dir(workPath)

		if wf.Go != nil {
			if err := out.AddGoStmt(wf.Go.Version); err != nil {
				return "", err
			}
		}
		for _, u := range wf.Use {
			abs := u.Path
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(workDir, abs)
			}
			abs = filepath.Clean(abs)
			// The module being evaluated is the one that moved.
			if abs == absModuleRoot {
				abs = copyPath
				sawModule = true
			}
			if err := out.AddUse(filepath.ToSlash(abs), u.ModulePath); err != nil {
				return "", err
			}
		}
		for _, r := range wf.Replace {
			newPath := r.New.Path
			// A filesystem replace target resolved relative to the
			// original workspace directory, which the build directory is
			// not. Same rewrite rewriteLocalReplaces performs for a
			// go.mod.
			if r.New.Version == "" && isLocalReplacePath(newPath) {
				if !filepath.IsAbs(newPath) {
					newPath = filepath.Join(workDir, newPath)
				}
				newPath = filepath.ToSlash(filepath.Clean(newPath))
			}
			if err := out.AddReplace(r.Old.Path, r.Old.Version, newPath, r.New.Version); err != nil {
				return "", err
			}
		}
	}

	// A synthesized workspace needs a go directive of its own. Without
	// one it implicitly requires go 1.18 and then refuses every module
	// that asks for more ("module . listed in go.work file requires go
	// >= 1.23, but go.work implicitly requires go 1.18"). The module's
	// own directive is the right floor: it is what the program was
	// written against, and a workspace cannot sensibly demand less.
	if out.Go == nil {
		if v := goDirectiveOf(filepath.Join(moduleCopy, "go.mod")); v != "" {
			if err := out.AddGoStmt(v); err != nil {
				return "", err
			}
		}
	}

	// A workspace that does not list this module is legal (GOWORK can
	// name any file), and a synthesized one has not listed it yet. The
	// copy has to be usable either way.
	if !sawModule {
		if err := out.AddUse(filepath.ToSlash(copyPath), ""); err != nil {
			return "", err
		}
	}

	// Blueprints last, so a program's own workspace members keep
	// precedence in the file's reading order.
	for _, dir := range blueprintDirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		if err := out.AddUse(filepath.ToSlash(abs), ""); err != nil {
			return "", err
		}
	}

	out.Cleanup()
	dest := filepath.Join(buildDir, workspaceFileName)
	if err := os.WriteFile(dest, modfile.Format(out.Syntax), 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// isLocalReplacePath reports whether a replace target names a directory
// rather than a module path. Go's own rule: a filesystem path starts
// with "./", "../", or is absolute.
func isLocalReplacePath(p string) bool {
	return strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") || filepath.IsAbs(p)
}

// goDirectiveOf reads a go.mod's own go version, or "" when it has none
// or cannot be read. Never fatal: a missing directive only means the
// synthesized workspace does not get one either, which is the state
// before this existed.
func goDirectiveOf(goModPath string) string {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}
	f, err := modfile.Parse(goModPath, data, nil)
	if err != nil || f.Go == nil {
		return ""
	}
	return f.Go.Version
}
