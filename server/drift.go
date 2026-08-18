package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// runDriftWatchLoop is UBI-28's own "Drift-watch (Surface) loop", core
// flow step 6: every Config.DriftWatchInterval, for every configured
// repo on every supported platform, shell out to `ubx scan --stack ...
// --surface-as <issue|pr>` -- the same, already-existing `--surface-as`
// machinery cli/surface.go implements (a drift-generated proposal
// opening a real issue or draft pull request), reused exactly as-is
// here, not reimplemented. A drift-driven pull request this produces is
// attributed to that platform's own configured bot identity -- see
// auth.go's own isAuthorizedToReplan for the real, resolved
// authorization consequence of that.
//
// Runs until ctx is cancelled. The first tick fires after one full
// interval, not immediately at startup -- a freshly (re)started server
// re-scanning every configured repo's own drift state instantly, on
// every restart, would turn an ordinary deploy into a surprise burst of
// API calls and (if anything's genuinely drifted) issue/PR creation;
// waiting for the first real interval keeps a restart's own real
// behavior boring.
func (s *Server) runDriftWatchLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.DriftWatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.driftWatchOnce(ctx)
		}
	}
}

// driftSurfaceTarget is everything one repo needs to run a real drift
// sweep: where its checkout is, which credential the subprocess gets,
// and which `ubx scan` flags name it on its own platform (UBI-165).
type driftSurfaceTarget struct {
	label       string   // "owner/name", "acme/infra", ... for logs
	repoDir     string   // a real, current checkout of the repo's default branch
	env         []string // the token env var `ubx scan` reads for this platform
	surfaceArgs []string // the platform selector flags `ubx scan --surface-as` needs
}

// driftWatchOnce runs one real pass over every configured repo, on every
// platform. A failure on one repo is logged and does not stop the
// others -- the same "one bad stack shouldn't block a fleet-wide sweep"
// reasoning `ubx status`'s own fleet report already applies.
//
// UBI-165: this used to be GitHub-only twice over. It resolved a GitHub
// App installation for every configured repo regardless of platform, and
// it shelled out to `ubx status --drift --surface-as ... --github-repo
// ...`, which never worked at all: `ubx status` has no --surface-as flag
// and never had one, so every drift sweep this loop ever ran died on
// "unknown flag" before reaching any platform's API. Both are fixed
// here: the checkout and credential are resolved per platform, and the
// command is `ubx scan`, which is where the real --surface-as machinery
// has always lived.
func (s *Server) driftWatchOnce(ctx context.Context) {
	self, err := selfExePath()
	if err != nil {
		slog.Error("ubx server: drift watch: resolve own executable", "error", err)
		return
	}

	for _, r := range s.cfg.Repos {
		target, err := s.driftTargetFor(ctx, r)
		if err != nil {
			slog.Error("ubx server: drift watch: prepare repository",
				"platform", r.Platform, "repo", repoConfigLabel(r), "error", err)
			continue
		}

		// UBI-167: the stacks come from the repo's own checkout, not
		// from a ledger_dir this entry used to declare. Unlike every
		// webhook-triggered path, drift-watch has no event and therefore
		// no changed files to disambiguate a multi-stack repository
		// with -- and it doesn't need any: drift genuinely applies to
		// every stack the repo declares, so each discovered stack gets
		// its own real sweep rather than one of them being picked.
		stacks, err := discoverStacks(target.repoDir)
		if err != nil {
			slog.Error("ubx server: drift watch: discover stacks", "repo", target.label, "error", err)
			continue
		}
		if len(stacks) == 0 {
			slog.Warn("ubx server: drift watch: skipping repository -- no .ubx/config found anywhere in its own checkout, so it declares no stack to check (UBI-167)",
				"repo", target.label)
			continue
		}

		for _, ledgerDir := range stacks {
			args := []string{"scan", "--ledger-dir", ledgerDir, "--surface-as", s.cfg.SurfaceAs}
			args = append(args, target.surfaceArgs...)
			if s.cfg.ProviderSource != "" {
				args = append(args, "--source", s.cfg.ProviderSource, "--provider-version", s.cfg.ProviderVersion)
			}
			if s.cfg.ProviderConfig != "" {
				args = append(args, "--provider-config", s.cfg.ProviderConfig)
			}

			result, err := runUbx(ctx, self, target.repoDir, target.env, args...)
			if err != nil {
				slog.Error("ubx server: drift watch: run ubx scan --surface-as",
					"platform", r.Platform, "repo", target.label, "ledger_dir", ledgerDir, "error", err)
				continue
			}
			if result.ExitCode != 0 && result.ExitCode != 1 {
				// 0 clean, 1 drift found (and surfaced) -- both real,
				// successful outcomes; anything else is a genuine failure.
				slog.Error("ubx server: drift watch: ubx scan --surface-as failed",
					"platform", r.Platform, "repo", target.label, "ledger_dir", ledgerDir,
					"exit_code", result.ExitCode, "stderr", result.Stderr)
			}
		}
	}
}

// driftTargetFor resolves one configured repo into a real checkout of
// its default branch plus the credential and flags its own platform
// needs.
//
// Every platform's checkout goes through the same helpers the webhook
// paths already use, so drift-watch inherits UBI-171's enterprise-host
// handling and every platform's own real clone-credential convention
// without restating any of it.
func (s *Server) driftTargetFor(ctx context.Context, r RepoConfig) (*driftSurfaceTarget, error) {
	label := repoConfigLabel(r)

	switch r.Platform {
	case "gitlab":
		repoDir, err := s.checkoutForGitLab(ctx, r.Project, defaultBranchRef)
		if err != nil {
			return nil, err
		}
		return &driftSurfaceTarget{
			label:       label,
			repoDir:     repoDir,
			env:         []string{"GITLAB_TOKEN=" + s.cfg.GitLabToken},
			surfaceArgs: gitlabSurfaceArgs(s.cfg, r),
		}, nil

	case "azuredevops":
		repoDir, err := s.checkoutForAzureDevOps(ctx, r.Project, r.Repository, defaultBranchRef)
		if err != nil {
			return nil, err
		}
		args := []string{"--azure-devops-project",
			s.cfg.AzureDevOpsOrganization + "/" + r.Project + "/" + r.Repository}
		if s.cfg.AzureDevOpsAPIBaseURL != "" {
			args = append(args, "--azure-devops-api-base-url", s.cfg.AzureDevOpsAPIBaseURL)
		}
		return &driftSurfaceTarget{
			label:       label,
			repoDir:     repoDir,
			env:         []string{"AZURE_DEVOPS_TOKEN=" + s.cfg.AzureDevOpsToken},
			surfaceArgs: args,
		}, nil

	case "bitbucketserver":
		repoDir, err := s.checkoutForBitbucketServer(ctx, r.Project, r.Repository, defaultBranchRef)
		if err != nil {
			return nil, err
		}
		return &driftSurfaceTarget{
			label:   label,
			repoDir: repoDir,
			env:     []string{"BITBUCKET_SERVER_TOKEN=" + s.cfg.BitbucketServerToken},
			surfaceArgs: []string{
				"--bitbucket-server-url", s.cfg.BitbucketServerURL,
				"--bitbucket-server-project", r.Project + "/" + r.Repository,
			},
		}, nil

	case "bitbucketcloud":
		repoDir, err := s.checkoutForBitbucketCloud(ctx, r.Project, r.Repository, defaultBranchRef)
		if err != nil {
			return nil, err
		}
		return &driftSurfaceTarget{
			label:       label,
			repoDir:     repoDir,
			env:         []string{"BITBUCKET_CLOUD_TOKEN=" + s.cfg.BitbucketCloudToken},
			surfaceArgs: []string{"--bitbucket-cloud-repo", r.Project + "/" + r.Repository},
		}, nil

	default: // github
		installationID, err := s.installations.installationIDFor(ctx, r.Owner, r.Name)
		if err != nil {
			return nil, fmt.Errorf("resolve installation: %w", err)
		}
		token, err := s.installations.installationToken(ctx, installationID)
		if err != nil {
			return nil, fmt.Errorf("get installation token: %w", err)
		}
		repoDir, err := ensureRepoCheckout(ctx, s.cfg.WorkDir, s.cfg.GitHubAPIBaseURL, r.Owner, r.Name, token)
		if err != nil {
			return nil, err
		}
		if err := checkoutRef(ctx, repoDir, defaultBranchRef); err != nil {
			return nil, err
		}
		args := []string{"--github-repo", r.Owner + "/" + r.Name}
		if s.cfg.GitHubAPIBaseURL != "" {
			args = append(args, "--github-api-base-url", s.cfg.GitHubAPIBaseURL)
		}
		return &driftSurfaceTarget{
			label:       label,
			repoDir:     repoDir,
			env:         []string{"GITHUB_TOKEN=" + token},
			surfaceArgs: args,
		}, nil
	}
}

func gitlabSurfaceArgs(cfg *Config, r RepoConfig) []string {
	args := []string{"--gitlab-project", r.Project}
	if cfg.GitLabAPIBaseURL != "" {
		args = append(args, "--gitlab-api-base-url", cfg.GitLabAPIBaseURL)
	}
	return args
}

// defaultBranchRef is the branch drift-watch checks out before scanning.
//
// "main" specifically, the same value this loop has always used: drift
// is measured against what a repository declares on its own mainline,
// never against whatever a previous webhook event happened to leave
// checked out in the shared working clone.
const defaultBranchRef = "main"

// repoConfigLabel renders a RepoConfig the way its own platform names a
// repository, for logs.
func repoConfigLabel(r RepoConfig) string {
	switch r.Platform {
	case "gitlab":
		return r.Project
	case "azuredevops", "bitbucketserver", "bitbucketcloud":
		return r.Project + "/" + r.Repository
	default:
		return r.Owner + "/" + r.Name
	}
}
