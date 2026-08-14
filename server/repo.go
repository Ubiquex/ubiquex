package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ensureRepoCheckout guarantees workDir/owner/name is a real, current git
// working tree of https://github.com/owner/name.git, authenticated with
// installationToken (an x-access-token, GitHub's own real scheme for a
// GitHub App's installation token as a git-over-HTTPS credential) --
// cloning it fresh if it doesn't exist yet, fetching main otherwise.
// Shells out to the real `git` binary, the same convention every other
// git-plumbing helper in this codebase already uses (package github's
// own git.go, every platform package's own test fixtures) -- no pure-Go
// git reimplementation for what a handful of real, ordinary git
// subcommands already do correctly.
func ensureRepoCheckout(ctx context.Context, workDir, owner, name, installationToken string) (string, error) {
	repoDir := filepath.Join(workDir, owner, name)
	remote := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", installationToken, owner, name)

	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil {
		if err := runGit(ctx, repoDir, "remote", "set-url", "origin", remote); err != nil {
			return "", err
		}
		if err := runGit(ctx, repoDir, "fetch", "--prune", "origin"); err != nil {
			return "", err
		}
		return repoDir, nil
	}

	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		return "", fmt.Errorf("create work dir for %s/%s: %w", owner, name, err)
	}
	if err := runGit(ctx, "", "clone", remote, repoDir); err != nil {
		return "", err
	}
	return repoDir, nil
}

// checkoutRef resets repoDir's own working tree to ref exactly (a branch
// name or a commit SHA) -- always from a real, just-fetched origin, never
// trusting whatever the working tree happened to have checked out from a
// previous event.
func checkoutRef(ctx context.Context, repoDir, ref string) error {
	if err := runGit(ctx, repoDir, "fetch", "origin", ref); err != nil {
		return err
	}
	return runGit(ctx, repoDir, "reset", "--hard", "FETCH_HEAD")
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %w\n%s", args, err, out)
	}
	return nil
}
