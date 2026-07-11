// Package github is UBI-11 stage 1's PR-merge acceptance derivation: git
// history verification (shelling out to the local `git` binary — no pure-Go
// git reimplementation needed for the handful of read-only plumbing
// commands this requires) plus a GitHub API client (github.com/google/
// go-github) for the pull request a merge commit belongs to and its
// reviews.
//
// core stays entirely dependency-free of this package (see
// core/accept.go's AcceptFromMerge doc comment) — everything here produces
// plain data that core.AcceptFromMerge takes as already-verified input, the
// same dependency-inversion discipline as core.StateReader/EventLookup for
// the tfplugin provider client and CloudTrail.
package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrCommitNotFound means a SHA doesn't resolve to a commit in the given
// repository's history — one of UBI-11 stage 1's named adversarial cases
// ("merge SHA not in history").
var ErrCommitNotFound = errors.New("commit not found in git history")

// ErrFileNotFoundAtCommit means the requested path doesn't exist in a
// commit's tree — the other named adversarial case ("proposal file absent
// from merge").
var ErrFileNotFoundAtCommit = errors.New("file not found at commit")

// CommitExists reports whether sha resolves to an actual commit object in
// the git repository at repoDir — not just any object (a blob or tree SHA
// would not count), and not merely "looks like a hex string": this runs
// `git cat-file -e <sha>^{commit}` against real history.
func CommitExists(ctx context.Context, repoDir, sha string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "cat-file", "-e", sha+"^{commit}")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("git cat-file: %w: %s", err, stderr.String())
	}
	return true, nil
}

// FileAtCommit returns path's exact content as it was committed at sha
// (`git show <sha>:<path>`) — the merge commit's tree, not whatever's
// currently on disk, which may have moved on since. Returns
// ErrFileNotFoundAtCommit if the commit exists but has no such path.
func FileAtCommit(ctx context.Context, repoDir, sha, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "show", sha+":"+path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && looksLikeMissingPath(stderr.String()) {
			return nil, fmt.Errorf("%w: %s at %s", ErrFileNotFoundAtCommit, path, sha)
		}
		return nil, fmt.Errorf("git show %s:%s: %w: %s", sha, path, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// looksLikeMissingPath recognizes git's own wording for "commit exists but
// this path doesn't" (`git show`'s error text, not a machine-readable exit
// code — git doesn't distinguish "bad revision" from "missing path" via
// exit code alone, so this checks the message git itself uses for exactly
// that case).
func looksLikeMissingPath(stderr string) bool {
	return strings.Contains(stderr, "does not exist in") || strings.Contains(stderr, "exists on disk, but not in")
}
