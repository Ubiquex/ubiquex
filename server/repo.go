package server

import (
	"context"
	"fmt"
	"net/url"
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
	remote := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", installationToken, owner, name)
	return ensureRepoCheckoutFromRemote(ctx, workDir, filepath.Join(owner, name), remote)
}

// ensureRepoCheckoutGitLab is ensureRepoCheckout's GitLab counterpart --
// same real "clone if missing, fetch otherwise" mechanism, but GitLab's
// own real git-over-HTTPS credential convention instead: any GitLab
// token type (including a Group Access Token) authenticates as
// "oauth2:<token>@..." (confirmed directly against GitLab's own current
// docs, not assumed symmetric with GitHub's x-access-token scheme).
// project is the full, real, possibly-nested namespace path
// ("acme/backend/infra") -- used directly as the on-disk subdirectory
// too, since it's already a valid relative path with no owner/name
// split to make.
func ensureRepoCheckoutGitLab(ctx context.Context, workDir, project, token, apiBaseURL string) (string, error) {
	host := "gitlab.com"
	if apiBaseURL != "" {
		if u, err := url.Parse(apiBaseURL); err == nil && u.Host != "" {
			host = u.Host
		}
	}
	remote := fmt.Sprintf("https://oauth2:%s@%s/%s.git", token, host, project)
	return ensureRepoCheckoutFromRemote(ctx, workDir, project, remote)
}

// ensureRepoCheckoutAzureDevOps is ensureRepoCheckout's Azure DevOps
// counterpart -- same real "clone if missing, fetch otherwise"
// mechanism, Azure DevOps' own real git-over-HTTPS PAT convention
// instead: any real Personal Access Token authenticates as
// "oauth2:<token>@dev.azure.com/{organization}/{project}/_git/
// {repository}" (confirmed directly against Microsoft's own current
// docs). repositoryID (the real GUID this package's own webhook
// handlers always have on hand, never a possibly-ambiguous name) is
// used directly in the clone URL -- Azure DevOps' own real repository
// routing resolves either a name or an ID equally in this same path
// position, so no separate name lookup is needed just to clone.
func ensureRepoCheckoutAzureDevOps(ctx context.Context, workDir, organization, project, repositoryID, token string) (string, error) {
	remote := fmt.Sprintf("https://oauth2:%s@dev.azure.com/%s/%s/_git/%s", token, organization, project, repositoryID)
	return ensureRepoCheckoutFromRemote(ctx, workDir, filepath.Join("azuredevops", project, repositoryID), remote)
}

// ensureRepoCheckoutFromRemote is ensureRepoCheckout's and
// ensureRepoCheckoutGitLab's own shared real mechanism, parameterized on
// subPath (workDir's own subdirectory a given repo checks out into) and
// remote (the full, already-credentialed clone URL) -- the one real
// structural difference between the two platforms (GitHub's fixed
// owner/name vs. GitLab's arbitrarily-nested project path) is already
// resolved by each caller before this function ever runs, so this
// function itself stays platform-agnostic.
func ensureRepoCheckoutFromRemote(ctx context.Context, workDir, subPath, remote string) (string, error) {
	repoDir := filepath.Join(workDir, subPath)

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
		return "", fmt.Errorf("create work dir for %s: %w", subPath, err)
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
