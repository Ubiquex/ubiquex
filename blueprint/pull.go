package blueprint

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Pull resolves source into a real local blueprint directory at dest,
// supporting exactly the two source types UBI-74 Slice 3 scopes (OCI/
// Strata is Slice 7): a local filesystem path (source already exists on
// disk -- resolved and copied into dest, ref/path unused), or a git
// repository (cloned into a scratch directory, checked out at ref --
// branch/tag/commit, default the repo's own default branch when ref is
// "" -- with path naming the blueprint's own location within that repo,
// default "." for a repo whose root IS the blueprint). Either way, dest
// ends up holding an ordinary local blueprint directory, indistinguishable
// afterward from one authored locally to begin with -- including
// blueprint.lock.json if the source already carried one; Verify reads it
// from dest exactly the same way regardless of how it got there.
//
// dest must not already exist, or must be empty -- Pull never overwrites
// existing content.
func Pull(ctx context.Context, source, dest, ref, path string) (string, error) {
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("blueprint pull: %s already exists and is not empty", dest)
	}

	if info, err := os.Stat(source); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("blueprint pull: %s exists but isn't a directory -- pulling a bare tarball directly isn't supported yet (Slice 8)", source)
		}
		absSrc, err := filepath.Abs(source)
		if err != nil {
			return "", fmt.Errorf("blueprint pull: %w", err)
		}
		if err := copyDir(absSrc, dest); err != nil {
			return "", fmt.Errorf("blueprint pull: %w", err)
		}
		return dest, nil
	}

	tmp, err := os.MkdirTemp("", "ubx-blueprint-pull-*")
	if err != nil {
		return "", fmt.Errorf("blueprint pull: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := gitClone(ctx, source, tmp); err != nil {
		return "", fmt.Errorf("blueprint pull: %w", err)
	}
	if ref != "" {
		if err := gitCheckout(ctx, tmp, ref); err != nil {
			return "", fmt.Errorf("blueprint pull: %w", err)
		}
	}

	if path == "" {
		path = "."
	}
	src := filepath.Join(tmp, path)
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return "", fmt.Errorf("blueprint pull: %q not found (or not a directory) in %s@%s", path, source, refOrDefault(ref))
	}
	if _, err := os.Stat(filepath.Join(src, UbxfileName)); err != nil {
		return "", fmt.Errorf("blueprint pull: %s at %s in %s@%s has no %s -- not a blueprint package", path, source, source, refOrDefault(ref), UbxfileName)
	}

	if err := copyDir(src, dest); err != nil {
		return "", fmt.Errorf("blueprint pull: %w", err)
	}
	return dest, nil
}

func refOrDefault(ref string) string {
	if ref == "" {
		return "HEAD"
	}
	return ref
}

// gitClone shells out to the real `git` binary (matching github/git.go's
// own "no pure-Go git reimplementation needed" precedent, for the same
// reason -- every real environment this runs in already has a working
// git install) to clone url into dest.
func gitClone(ctx context.Context, url, dest string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", url, dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone %s: %w: %s", url, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitCheckout(ctx context.Context, repoDir, ref string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", "--quiet", ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout %s: %w: %s", ref, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// copyDir copies every real file from src into dest (creating dest and
// any needed subdirectories), skipping dot-prefixed entries -- the same
// skipEntry convention hashFiles already uses (manifest.go), so a
// pulled dest ends up with EXACTLY the file set Package/Verify hash (a
// stray .git directory a locally-authored blueprint happens to sit
// inside, for instance, never leaks into a pulled copy).
func copyDir(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return os.MkdirAll(dest, 0o755)
		}
		if skipEntry(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, raw, info.Mode().Perm())
	})
}
