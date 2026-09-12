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
// supporting all four source types UBI-74 scopes across Slices 3, 7, and
// 8: a local directory (source already exists on disk as a directory --
// resolved and copied into dest, ref/path unused), a bare tarball FILE
// (source exists on disk but is NOT a directory -- extracted directly
// into dest, Slice 8's own offline/email/support-ticket delivery mode,
// no network involved at all), a git repository (cloned into a scratch
// directory, checked out at ref -- branch/tag/commit, default the repo's
// own default branch when ref is "" -- with path naming the blueprint's
// own location within that repo, default "." for a repo whose root IS
// the blueprint), or a real OCI artifact ("oci://registry/repo:tag",
// Slice 7 -- ref/path are git-specific and refused if set, since the tag
// is already embedded in the oci:// reference itself). Either way, dest
// ends up holding an ordinary local blueprint directory, indistinguishable
// afterward from one authored locally to begin with -- including
// blueprint.lock.json if the source already carried one; Verify reads it
// from dest exactly the same way regardless of how it got there --
// deliberately so for the tarball-file source specifically: Pull itself
// neither trusts nor rejects the tarball's own declared content_hash, it
// only extracts it, exactly like every other source type leaves hash
// verification as a separate, explicit `ubx blueprint verify` step. This
// is the one delivery mode with no git history or registry-native
// integrity to lean on at all (the original design record's own
// framing) -- Verify is what actually protects it.
//
// dest must not already exist, or must be empty -- Pull never overwrites
// existing content.
func Pull(ctx context.Context, source, dest, ref, path string) (string, error) {
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("blueprint pull: %s already exists and is not empty", dest)
	}

	if strings.HasPrefix(source, "oci://") {
		if path != "" {
			return "", fmt.Errorf("blueprint pull: --path is git-specific and meaningless for an oci:// source -- drop it (an artifact is the whole blueprint, there is no subdirectory to select)")
		}
		reference, err := ociReference(source, ref)
		if err != nil {
			return "", fmt.Errorf("blueprint pull: %w", err)
		}
		if err := pullOCI(ctx, reference, dest); err != nil {
			return "", fmt.Errorf("blueprint pull: %w", err)
		}
		return dest, nil
	}

	if info, err := os.Stat(source); err == nil {
		if !info.IsDir() {
			if ref != "" || path != "" {
				return "", fmt.Errorf("blueprint pull: --ref/--path are git-specific and meaningless for a bare tarball file -- drop them")
			}
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return "", fmt.Errorf("blueprint pull: %w", err)
			}
			if err := extractTarGz(source, dest); err != nil {
				return "", fmt.Errorf("blueprint pull: %s doesn't look like a real gzipped blueprint tarball: %w", source, err)
			}
			if !IsBlueprintDir(dest) {
				return "", fmt.Errorf("blueprint pull: %s has no %s after extraction -- not a real blueprint tarball", source, blueprintMarkers())
			}
			return dest, nil
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
	if !IsBlueprintDir(src) {
		return "", fmt.Errorf("blueprint pull: %s at %s in %s@%s has no %s -- not a blueprint package", path, source, source, refOrDefault(ref), blueprintMarkers())
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

// ociReference composes an oci:// source with a separately-supplied
// version into one complete artifact reference (UBI-256).
//
// The version is the same idea for both source kinds: which version of
// this blueprint. For git it is a ref; for OCI it is a tag, and a tag
// is part of the reference rather than a parameter alongside it. So it
// is composed in here, once, rather than by each medium that can supply
// one.
//
// Before this, an oci:// source refused a version outright, because the
// tag "is already embedded in the reference". That is true of a
// reference that carries one, and it left the HCL blueprint block with
// no working spelling at all: putting the tag in source produced a
// blueprint name containing a colon, and supplying it as version
// produced a git-specific refusal. Both are real reports from
// publishing the first blueprint to a registry.
//
// Supplying it in BOTH places is refused rather than resolved by a
// precedence rule. They can disagree, and silently picking a winner
// would pull a version the author did not ask for, which is the worst
// available outcome for something content-addressed.
func ociReference(source, version string) (string, error) {
	if version == "" {
		return source, nil
	}
	if ociHasVersion(source) {
		return "", fmt.Errorf("%s already names a version, and one was also given separately -- put it in exactly one place, since two can disagree", source)
	}
	if strings.HasPrefix(version, "sha256:") {
		return source + "@" + version, nil
	}
	return source + ":" + version, nil
}

// ociHasVersion reports whether an oci:// reference already carries a
// tag or a digest.
//
// The colon check deliberately looks only after the last slash: a
// registry may carry a port ("oci://localhost:5000/repo"), and that
// colon is part of the host, not a tag.
func ociHasVersion(source string) bool {
	rest := strings.TrimPrefix(source, "oci://")
	if strings.Contains(rest, "@") {
		return true
	}
	lastSlash := strings.LastIndex(rest, "/")
	return strings.Contains(rest[lastSlash+1:], ":")
}
