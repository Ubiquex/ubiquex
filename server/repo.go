package server

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ensureRepoCheckout guarantees workDir/owner/name is a real, current git
// working tree of owner/name on whatever GitHub host apiBaseURL names
// (github.com when it is empty, a GitHub Enterprise Server instance
// otherwise -- see gitHostForGitHub), authenticated with
// installationToken (an x-access-token, GitHub's own real scheme for a
// GitHub App's installation token as a git-over-HTTPS credential) --
// cloning it fresh if it doesn't exist yet, fetching main otherwise.
// Shells out to the real `git` binary, the same convention every other
// git-plumbing helper in this codebase already uses (package github's
// own git.go, every platform package's own test fixtures) -- no pure-Go
// git reimplementation for what a handful of real, ordinary git
// subcommands already do correctly.
func ensureRepoCheckout(ctx context.Context, workDir, apiBaseURL, owner, name, installationToken string) (string, error) {
	host, err := gitHostForGitHub(apiBaseURL)
	if err != nil {
		return "", err
	}
	remote := fmt.Sprintf("https://x-access-token:%s@%s/%s/%s.git", installationToken, host, owner, name)
	return ensureRepoCheckoutFromRemote(ctx, workDir, filepath.Join(owner, name), remote)
}

// gitHostForGitHub resolves the real host git clones and fetches go to,
// from whatever API base URL is configured (UBI-171).
//
// Before this, the clone host was the literal string "github.com",
// unconditionally, which made GitHub Enterprise Server impossible no
// matter how correctly its API base URL was configured: every event on
// a GHES repository would authenticate against the instance and then
// try to clone from github.com. Its GitLab and Bitbucket Server
// siblings in this file already derived their host from real config;
// this brings GitHub in line with them.
//
// Empty means github.com, the real SaaS default. The one real
// translation worth doing explicitly: github.com's own API lives on
// api.github.com while its git remotes live on github.com, so an
// operator who configured the SaaS API host by hand still gets the
// correct git host rather than a clone URL pointed at an API endpoint.
// A GitHub Enterprise Server instance serves both from the same host,
// so every other value is used as-is.
func gitHostForGitHub(apiBaseURL string) (string, error) {
	if apiBaseURL == "" {
		return "github.com", nil
	}
	u, err := url.Parse(apiBaseURL)
	if err != nil {
		return "", fmt.Errorf("github api base URL %q: %w", apiBaseURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("github api base URL %q names no host", apiBaseURL)
	}
	if u.Host == "api.github.com" {
		return "github.com", nil
	}
	return u.Host, nil
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
func ensureRepoCheckoutAzureDevOps(ctx context.Context, workDir, apiBaseURL, organization, project, repositoryID, token string) (string, error) {
	remote, err := azureDevOpsCloneURL(apiBaseURL, organization, project, repositoryID, token)
	if err != nil {
		return "", err
	}
	return ensureRepoCheckoutFromRemote(ctx, workDir, filepath.Join("azuredevops", project, repositoryID), remote)
}

// azureDevOpsCloneURL builds the real git remote for project/
// repositoryID, from whatever API base URL is configured (UBI-171).
//
// Before this, the host was the literal string "dev.azure.com",
// unconditionally, the identical defect GitHub's own clone URL had:
// an on-prem Azure DevOps Server deployment could authenticate against
// its own instance and would then try to clone from the SaaS host.
//
// Empty means the real SaaS shape, https://dev.azure.com/{organization}.
// Otherwise the configured base URL is used as the collection root and
// the project/_git/{repository} path is appended to it. That is the
// right composition for on-prem Azure DevOps Server, whose collection
// URL carries its own virtual directory and collection name (for
// example https://azuredevops.example.com/tfs/DefaultCollection), and
// where the collection plays exactly the role the organization plays in
// the SaaS URL.
func azureDevOpsCloneURL(apiBaseURL, organization, project, repositoryID, token string) (string, error) {
	base := apiBaseURL
	if base == "" {
		base = "https://dev.azure.com/" + organization
	}
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", fmt.Errorf("azure devops api base URL %q: %w", base, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("azure devops api base URL %q names no host", base)
	}
	u.User = url.UserPassword("oauth2", token)
	u.Path = strings.TrimRight(u.Path, "/") + "/" + project + "/_git/" + repositoryID
	return u.String(), nil
}

// ensureRepoCheckoutBitbucketServer is ensureRepoCheckout's Bitbucket
// Server counterpart -- same real "clone if missing, fetch otherwise"
// mechanism, Bitbucket Server's own real git-over-HTTPS HTTP-access-
// token convention instead: "https://<username>:<token>@<host>/scm/
// {projectKey}/{repositorySlug}.git" (confirmed directly against
// Atlassian's own current HTTP access tokens docs). Unlike GitHub's
// placeholder "x-access-token" or GitLab's/Azure DevOps' placeholder
// "oauth2" convention, Bitbucket Server's own real HTTP access tokens
// require the REAL username the token belongs to in that position --
// there is no platform-fixed placeholder string here, so username is
// the token's own real, operator-configured owner (Config.
// BitbucketServerBotName, the same identity used for edit-in-place/
// two-tier-authorization elsewhere in this package). baseURL's own
// host is used directly -- Bitbucket Server has no separate API-vs-git
// host split the way Azure DevOps does.
func ensureRepoCheckoutBitbucketServer(ctx context.Context, workDir, baseURL, projectKey, repositorySlug, username, token string) (string, error) {
	host := ""
	if u, err := url.Parse(baseURL); err == nil {
		host = u.Host
	}
	remote := fmt.Sprintf("https://%s:%s@%s/scm/%s/%s.git", username, token, host, projectKey, repositorySlug)
	return ensureRepoCheckoutFromRemote(ctx, workDir, filepath.Join("bitbucketserver", projectKey, repositorySlug), remote)
}

// ensureRepoCheckoutBitbucketCloud is ensureRepoCheckout's Bitbucket
// Cloud counterpart -- same real "clone if missing, fetch otherwise"
// mechanism, Bitbucket Cloud's own real git-over-HTTPS access-token
// convention instead: "https://x-token-auth:{token}@bitbucket.org/
// {workspace}/{repo_slug}.git".
//
// The literal string "x-token-auth" in the username position is
// required, and is a real, third distinct convention -- confirmed
// directly against Atlassian's own current documentation, which calls
// it out explicitly as differing from GitHub's (where the token itself
// goes in the username field). It matches neither GitHub's
// "x-access-token" placeholder nor Bitbucket Server's requirement for
// the token owner's own real username. Unlike Bitbucket Server, the
// host is fixed: Bitbucket Cloud is SaaS.
func ensureRepoCheckoutBitbucketCloud(ctx context.Context, workDir, workspace, repoSlug, token string) (string, error) {
	remote := fmt.Sprintf("https://x-token-auth:%s@bitbucket.org/%s/%s.git", token, workspace, repoSlug)
	return ensureRepoCheckoutFromRemote(ctx, workDir, filepath.Join("bitbucketcloud", workspace, repoSlug), remote)
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
