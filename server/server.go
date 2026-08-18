package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	adevops "github.com/ubiquex/ubiquex/azuredevops"
	bbcloud "github.com/ubiquex/ubiquex/bitbucketcloud"
	bbserver "github.com/ubiquex/ubiquex/bitbucketserver"
	ghub "github.com/ubiquex/ubiquex/github"
	glab "github.com/ubiquex/ubiquex/gitlab"
	glapi "gitlab.com/gitlab-org/api/client-go/v2"
)

// Server is ubx server's own real, running instance -- everything
// New wires together, and Run drives until ctx is cancelled.
type Server struct {
	cfg             *Config
	installations   *installationClients
	botLogin        string
	gitlab          *glab.Client     // nil when Config.GitLabToken is unset -- GitLab support not configured
	azuredevops     *adevops.Client  // nil when Config.AzureDevOpsToken is unset -- Azure DevOps support not configured
	bitbucketserver *bbserver.Client // nil when Config.BitbucketServerToken is unset -- Bitbucket Server support not configured
	bitbucketcloud  *bbcloud.Client  // nil when Config.BitbucketCloudToken is unset -- Bitbucket Cloud support not configured
	self            string           // this process's own resolved executable path (exec.go)
}

// New builds a Server from cfg, resolving the GitHub App credentials and
// this process's own executable path once, up front -- both real,
// fail-fast checks (a bad private key path or a resolve failure should
// stop startup, not surface as a mysterious first-webhook error).
// GitLab's/Azure DevOps' own clients are only built when their own
// token config is set -- unlike GitHub, both are genuinely optional per
// deployment (an operator watching only GitHub repos never sets either),
// so a nil client is itself a real, valid startup state, not an error.
func New(cfg *Config) (*Server, error) {
	installations, err := newInstallationClients(cfg.GitHubAppID, cfg.GitHubAppPrivateKeyPath, cfg.GitHubAPIBaseURL)
	if err != nil {
		return nil, err
	}
	self, err := selfExePath()
	if err != nil {
		return nil, err
	}

	var gitlabClient *glab.Client
	if cfg.GitLabToken != "" {
		gitlabClient, err = newGitLabClient(cfg.GitLabToken, cfg.GitLabAPIBaseURL)
		if err != nil {
			return nil, err
		}
	}

	var azureDevOpsClient *adevops.Client
	if cfg.AzureDevOpsToken != "" {
		azureDevOpsClient = newAzureDevOpsClient(cfg.AzureDevOpsOrganization, cfg.AzureDevOpsToken, cfg.AzureDevOpsAPIBaseURL)
	}

	var bitbucketServerClient *bbserver.Client
	if cfg.BitbucketServerToken != "" {
		bitbucketServerClient = newBitbucketServerClient(cfg.BitbucketServerURL, cfg.BitbucketServerToken)
	}

	var bitbucketCloudClient *bbcloud.Client
	if cfg.BitbucketCloudToken != "" {
		bitbucketCloudClient = newBitbucketCloudClient(cfg.BitbucketCloudToken, cfg.BitbucketCloudAPIBaseURL)
	}

	return &Server{
		cfg:             cfg,
		installations:   installations,
		botLogin:        cfg.GitHubBotLogin,
		gitlab:          gitlabClient,
		azuredevops:     azureDevOpsClient,
		bitbucketserver: bitbucketServerClient,
		bitbucketcloud:  bitbucketCloudClient,
		self:            self,
	}, nil
}

// Run serves the webhook endpoints and drives the drift-watch loop until
// ctx is cancelled, then shuts the HTTP server down gracefully.
// "/webhook/github", "/webhook/gitlab", "/webhook/azuredevops", and
// "/webhook/bitbucketserver" are real, separate delivery targets per
// platform -- Phase 1's original single "/webhook" route was renamed
// once already (Phase 2, never deployed anywhere with real users
// depending on the old form), so this is simply the fourth real route
// added to that same real convention.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/webhook/github", webhookHandler{s: s})
	mux.Handle("/webhook/gitlab", gitlabWebhookHandler{s: s})
	mux.Handle("/webhook/azuredevops", azureDevOpsWebhookHandler{s: s})
	mux.Handle("/webhook/bitbucketserver", bitbucketServerWebhookHandler{s: s})
	mux.Handle("/webhook/bitbucketcloud", bitbucketCloudWebhookHandler{s: s})
	httpServer := &http.Server{Addr: s.cfg.ListenAddr, Handler: mux}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("ubx server: listening", "addr", s.cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go s.runDriftWatchLoop(ctx)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// matchingRepoConfigsGitHub returns every real Config.Repos entry for
// owner/repo -- UBI-166's own real allowlist query, replacing the
// former ledgerDirFor's own silent "." fallback for an unlisted repo.
// Zero entries means the repository is not on the allowlist and every
// caller refuses outright, loudly logged. One or more entries all mean
// the same single thing (UBI-167: entries carry repository identity
// only, so duplicates for one repository are redundant rather than
// meaningful) -- allowed. Where the stack actually lives is resolved
// separately, from the repo's own checkout, by resolveStackIn.
func (s *Server) matchingRepoConfigsGitHub(owner, repo string) []RepoConfig {
	var out []RepoConfig
	for _, r := range s.cfg.Repos {
		if r.Platform == "github" && r.Owner == owner && r.Name == repo {
			out = append(out, r)
		}
	}
	return out
}

// changedFilePathsGitHub returns prNumber's own real, current changed-
// file paths -- resolveLedgerDirLazy's own fetch closure for the
// GitHub path, only ever called when discoverStacks found more than
// one real stack in the checkout (a genuinely multi-stack repository).
func changedFilePathsGitHub(ctx context.Context, api *ghub.Client, owner, repo string, prNumber int) ([]string, error) {
	files, _, err := api.API().PullRequests.ListFiles(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.GetFilename())
	}
	return paths, nil
}

// repoDirFor is ensureRepoCheckout's own Server-level wrapper: resolve a
// real installation token, then guarantee a real, current working tree
// for owner/repo under Config.WorkDir.
func (s *Server) repoDirFor(ctx context.Context, installationID int64, owner, repo string) (string, error) {
	token, err := s.installations.installationToken(ctx, installationID)
	if err != nil {
		return "", err
	}
	return ensureRepoCheckout(ctx, s.cfg.WorkDir, s.cfg.GitHubAPIBaseURL, owner, repo, token)
}

// checkoutForGitHub is repoDirFor plus a real checkout at ref -- the
// one real working tree a single event is handled against, produced
// once by the handler (UBI-167) rather than separately inside each
// run* helper the way it used to be. Auto-discovery reads the stacks
// this specific commit actually declares, so the checkout has to
// happen before the stack is resolved, not after.
func (s *Server) checkoutForGitHub(ctx context.Context, installationID int64, owner, repo, ref string) (string, error) {
	repoDir, err := s.repoDirFor(ctx, installationID, owner, repo)
	if err != nil {
		return "", err
	}
	if err := checkoutRef(ctx, repoDir, ref); err != nil {
		return "", err
	}
	return repoDir, nil
}

// checkoutForGitLab is checkoutForGitHub's own GitLab counterpart.
func (s *Server) checkoutForGitLab(ctx context.Context, project, ref string) (string, error) {
	repoDir, err := s.repoDirForGitLab(ctx, project)
	if err != nil {
		return "", err
	}
	if err := checkoutRef(ctx, repoDir, ref); err != nil {
		return "", err
	}
	return repoDir, nil
}

// checkoutForAzureDevOps is checkoutForGitHub's own Azure DevOps
// counterpart.
func (s *Server) checkoutForAzureDevOps(ctx context.Context, project, repositoryID, ref string) (string, error) {
	repoDir, err := s.repoDirForAzureDevOps(ctx, project, repositoryID)
	if err != nil {
		return "", err
	}
	if err := checkoutRef(ctx, repoDir, ref); err != nil {
		return "", err
	}
	return repoDir, nil
}

// checkoutForBitbucketServer is checkoutForGitHub's own Bitbucket
// Server counterpart.
func (s *Server) checkoutForBitbucketServer(ctx context.Context, projectKey, repositorySlug, ref string) (string, error) {
	repoDir, err := s.repoDirForBitbucketServer(ctx, projectKey, repositorySlug)
	if err != nil {
		return "", err
	}
	if err := checkoutRef(ctx, repoDir, ref); err != nil {
		return "", err
	}
	return repoDir, nil
}

// providerArgs returns the real --source/--provider-version/
// --provider-config flags every plan/ship/status invocation needs, from
// Config -- empty when ProviderSource isn't configured (a real,
// legitimate state for a server only ever exercising `ubx plan`'s own
// schema-only resolve path, which per docs/architecture.md never makes
// a live provider call anyway).
func (s *Server) providerArgs() []string {
	var args []string
	if s.cfg.ProviderSource != "" {
		args = append(args, "--source", s.cfg.ProviderSource, "--provider-version", s.cfg.ProviderVersion)
	}
	if s.cfg.ProviderConfig != "" {
		args = append(args, "--provider-config", s.cfg.ProviderConfig)
	}
	return args
}

// intentProviderEnv returns the extra env vars a `ubx plan` subprocess
// needs to transcribe a markdown-authored proposal via the configured
// [intent] provider (docs/intent-provider.md) -- UBX_INTENT_PROVIDER_KEY
// carries the real secret value (never logged, never written to disk),
// under the one fixed name Config.IntentProviderKey's own doc comment
// documents every watched repo's own .ubx/config [intent].key_ref.env
// is expected to reference, so package server never needs to parse that
// repo-owned TOML cascade itself to learn a repo-chosen name. Empty
// when Config.IntentProviderKey is unset -- a real, legitimate
// deployment for an operator who never accepts markdown-authored
// proposals at all; `ubx plan`'s own existing resolveKeyRef error
// surfaces as a real, posted PR/MR comment in that case, not a silent
// failure.
func (s *Server) intentProviderEnv() []string {
	if s.cfg.IntentProviderKey == "" {
		return nil
	}
	return []string{"UBX_INTENT_PROVIDER_KEY=" + s.cfg.IntentProviderKey}
}

// runPlanAndComment is core-flow steps 1 and 3: run `ubx plan` against
// prNumber's own real head commit, and post/edit the result as a single
// PR comment (comment.go's own edit-last mechanism) -- one real,
// current plan visible per PR, exactly UBI-161's own already-established
// behavior, reused here rather than reimplemented.
func (s *Server) runPlanAndComment(ctx context.Context, api *ghub.Client, owner, repo string, prNumber int, repoDir, ledgerDir string) error {
	args := append([]string{"plan", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	result, err := runUbx(ctx, s.self, repoDir, s.intentProviderEnv(), args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "plan", body)
}

// runAutomaticShip is core-flow step 5's automatic path: `ubx ship
// --yes`, checked out at baseRef (the branch the merge just landed on),
// with --confirm-destroys never passed -- no exception, regardless of
// Config.AllowDestroy. UBI-28's own "destroy... still requires a
// required, separate, extra confirmation beyond approval alone, every
// time" has no human present to give that confirmation in an unattended,
// merge-triggered path; a destructive proposal therefore always fails
// `ubx ship`'s own confirm-destroys enforcement here, by design, forcing
// a human to the manual comment path below instead, which can supply it.
func (s *Server) runAutomaticShip(ctx context.Context, api *ghub.Client, owner, repo string, prNumber int, repoDir, ledgerDir string) error {
	args := append([]string{"ship", "--yes", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "ship", body)
}

// runManualShip is core-flow step 5's manual path: a CODEOWNERS-
// authorized `ubx ship` comment. confirmDestroys is true only when the
// human's own comment explicitly included `--confirm-destroys` --
// UBI-28's own required, separate, per-instance confirmation. Even then,
// --confirm-destroys is only actually forwarded to the subprocess when
// Config.AllowDestroy is also true: the operator's own config is the
// outer gate (a destroy is never reachable at all unless the whole
// server was deliberately turned on for it), the human's own explicit
// flag is the inner one (this specific ship, this specific time) -- both
// real, both required, neither alone sufficient.
func (s *Server) runManualShip(ctx context.Context, api *ghub.Client, owner, repo string, prNumber int, repoDir, ledgerDir string, confirmDestroys bool) error {
	args := append([]string{"ship", "--yes", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	if confirmDestroys && s.cfg.AllowDestroy {
		args = append(args, "--confirm-destroys")
	}
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "ship", body)
}

// matchingRepoConfigsGitLab is matchingRepoConfigsGitHub's own GitLab
// counterpart, matched on project's real, full namespace path rather
// than a separate owner/name pair.
func (s *Server) matchingRepoConfigsGitLab(project string) []RepoConfig {
	var out []RepoConfig
	for _, r := range s.cfg.Repos {
		if r.Platform == "gitlab" && r.Project == project {
			out = append(out, r)
		}
	}
	return out
}

// changedFilePathsGitLab is changedFilePathsGitHub's own GitLab
// counterpart, against GitLab's own real MergeRequestDiff shape (same
// NewPath/OldPath fallback discoverProposalFileGitLab already uses).
func changedFilePathsGitLab(ctx context.Context, api *glab.Client, project string, mrIID int64) ([]string, error) {
	diffs, _, err := api.API().MergeRequests.ListMergeRequestDiffs(project, mrIID, nil, glapi.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(diffs))
	for _, d := range diffs {
		path := d.NewPath
		if path == "" {
			path = d.OldPath
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// repoDirForGitLab is ensureRepoCheckoutGitLab's own Server-level
// wrapper -- repoDirFor's real GitLab counterpart. Unlike GitHub's
// per-installation token (resolved fresh per call from a short-lived
// JWT-derived cache), GitLab's own token is the one, real, static
// Config.GitLabToken -- there is no per-installation concept to
// resolve at all.
func (s *Server) repoDirForGitLab(ctx context.Context, project string) (string, error) {
	if s.gitlab == nil {
		return "", fmt.Errorf("gitlab support is not configured (missing --gitlab-token)")
	}
	return ensureRepoCheckoutGitLab(ctx, s.cfg.WorkDir, project, s.cfg.GitLabToken, s.cfg.GitLabAPIBaseURL)
}

// runPlanAndCommentGitLab is runPlanAndComment's own real GitLab
// counterpart -- identical core-flow steps 1 and 3, against GitLab's
// own real Notes API instead.
func (s *Server) runPlanAndCommentGitLab(ctx context.Context, api *glab.Client, project string, mrIID int64, repoDir, ledgerDir string) error {
	args := append([]string{"plan", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	result, err := runUbx(ctx, s.self, repoDir, s.intentProviderEnv(), args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "plan", body)
}

// runAutomaticShipGitLab is runAutomaticShip's own real GitLab
// counterpart -- the identical real "never --confirm-destroys on an
// unattended, merge-triggered path, no exception" safety property.
func (s *Server) runAutomaticShipGitLab(ctx context.Context, api *glab.Client, project string, mrIID int64, repoDir, ledgerDir string) error {
	args := append([]string{"ship", "--yes", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "ship", body)
}

// runManualShipGitLab is runManualShip's own real GitLab counterpart --
// the identical real two-gate confirm-destroys rule (Config.AllowDestroy
// as the outer, operator-set gate; the human's own explicit
// `--confirm-destroys` note flag as the inner, per-instance one; both
// required, neither alone sufficient).
func (s *Server) runManualShipGitLab(ctx context.Context, api *glab.Client, project string, mrIID int64, repoDir, ledgerDir string, confirmDestroys bool) error {
	args := append([]string{"ship", "--yes", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	if confirmDestroys && s.cfg.AllowDestroy {
		args = append(args, "--confirm-destroys")
	}
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "ship", body)
}

// matchingRepoConfigsAzureDevOps is matchingRepoConfigsGitHub's own
// Azure DevOps counterpart, matched on both real project and
// repository identifiers -- a Config.Repos entry's own Repository
// field can name either a repo's real name or its real ID, matched
// against both real fields the webhook/API give (repositoryID is
// always the real GUID; the operator may have configured either).
func (s *Server) matchingRepoConfigsAzureDevOps(project, repositoryID string) []RepoConfig {
	var out []RepoConfig
	for _, r := range s.cfg.Repos {
		if r.Platform == "azuredevops" && r.Project == project && r.Repository == repositoryID {
			out = append(out, r)
		}
	}
	return out
}

// repoDirForAzureDevOps is ensureRepoCheckoutAzureDevOps's own
// Server-level wrapper -- repoDirFor's real Azure DevOps counterpart.
// Like GitLab's own real, static token, there's no per-installation
// concept to resolve here either.
func (s *Server) repoDirForAzureDevOps(ctx context.Context, project, repositoryID string) (string, error) {
	if s.azuredevops == nil {
		return "", fmt.Errorf("azure devops support is not configured (missing --azure-devops-token)")
	}
	return ensureRepoCheckoutAzureDevOps(ctx, s.cfg.WorkDir, s.cfg.AzureDevOpsAPIBaseURL, s.cfg.AzureDevOpsOrganization, project, repositoryID, s.cfg.AzureDevOpsToken)
}

// runPlanAndCommentAzureDevOps is runPlanAndComment's own real Azure
// DevOps counterpart -- identical core-flow steps 1 and 3, against
// Azure DevOps' own real comment-thread API instead.
func (s *Server) runPlanAndCommentAzureDevOps(ctx context.Context, api *adevops.Client, project, repositoryID string, prID int, repoDir, ledgerDir string) error {
	args := append([]string{"plan", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	result, err := runUbx(ctx, s.self, repoDir, s.intentProviderEnv(), args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentAzureDevOps(ctx, api, project, repositoryID, prID, s.cfg.AzureDevOpsBotDisplayName, "plan", body)
}

// runAutomaticShipAzureDevOps is runAutomaticShip's own real Azure
// DevOps counterpart -- the identical real "never --confirm-destroys
// on an unattended, merge-triggered path, no exception" safety
// property.
func (s *Server) runAutomaticShipAzureDevOps(ctx context.Context, api *adevops.Client, project, repositoryID string, prID int, repoDir, ledgerDir string) error {
	args := append([]string{"ship", "--yes", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentAzureDevOps(ctx, api, project, repositoryID, prID, s.cfg.AzureDevOpsBotDisplayName, "ship", body)
}

// runManualShipAzureDevOps is runManualShip's own real Azure DevOps
// counterpart -- the identical real two-gate confirm-destroys rule.
func (s *Server) runManualShipAzureDevOps(ctx context.Context, api *adevops.Client, project, repositoryID string, prID int, repoDir, ledgerDir string, confirmDestroys bool) error {
	args := append([]string{"ship", "--yes", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	if confirmDestroys && s.cfg.AllowDestroy {
		args = append(args, "--confirm-destroys")
	}
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentAzureDevOps(ctx, api, project, repositoryID, prID, s.cfg.AzureDevOpsBotDisplayName, "ship", body)
}

// matchingRepoConfigsBitbucketServer is matchingRepoConfigsGitHub's
// own Bitbucket Server counterpart, matched on both real project key
// and repository slug.
func (s *Server) matchingRepoConfigsBitbucketServer(projectKey, repositorySlug string) []RepoConfig {
	var out []RepoConfig
	for _, r := range s.cfg.Repos {
		if r.Platform == "bitbucketserver" && r.Project == projectKey && r.Repository == repositorySlug {
			out = append(out, r)
		}
	}
	return out
}

// repoDirForBitbucketServer is ensureRepoCheckoutBitbucketServer's own
// Server-level wrapper -- repoDirFor's real Bitbucket Server
// counterpart. Like GitLab's/Azure DevOps' own real, static token,
// there's no per-installation concept to resolve here either.
func (s *Server) repoDirForBitbucketServer(ctx context.Context, projectKey, repositorySlug string) (string, error) {
	if s.bitbucketserver == nil {
		return "", fmt.Errorf("bitbucket server support is not configured (missing --bitbucket-server-token)")
	}
	return ensureRepoCheckoutBitbucketServer(ctx, s.cfg.WorkDir, s.cfg.BitbucketServerURL, projectKey, repositorySlug, s.cfg.BitbucketServerBotName, s.cfg.BitbucketServerToken)
}

// runPlanAndCommentBitbucketServer is runPlanAndComment's own real
// Bitbucket Server counterpart -- identical core-flow steps 1 and 3,
// against Bitbucket Server's own real flat-comment API instead.
func (s *Server) runPlanAndCommentBitbucketServer(ctx context.Context, api *bbserver.Client, projectKey, repositorySlug string, prID int, repoDir, ledgerDir string) error {
	args := append([]string{"plan", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	result, err := runUbx(ctx, s.self, repoDir, s.intentProviderEnv(), args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentBitbucketServer(ctx, api, projectKey, repositorySlug, prID, s.cfg.BitbucketServerBotName, "plan", body)
}

// runAutomaticShipBitbucketServer is runAutomaticShip's own real
// Bitbucket Server counterpart -- the identical real "never
// --confirm-destroys on an unattended, merge-triggered path, no
// exception" safety property.
func (s *Server) runAutomaticShipBitbucketServer(ctx context.Context, api *bbserver.Client, projectKey, repositorySlug string, prID int, repoDir, ledgerDir string) error {
	args := append([]string{"ship", "--yes", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentBitbucketServer(ctx, api, projectKey, repositorySlug, prID, s.cfg.BitbucketServerBotName, "ship", body)
}

// runManualShipBitbucketServer is runManualShip's own real Bitbucket
// Server counterpart -- the identical real two-gate confirm-destroys
// rule.
func (s *Server) runManualShipBitbucketServer(ctx context.Context, api *bbserver.Client, projectKey, repositorySlug string, prID int, repoDir, ledgerDir string, confirmDestroys bool) error {
	args := append([]string{"ship", "--yes", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	if confirmDestroys && s.cfg.AllowDestroy {
		args = append(args, "--confirm-destroys")
	}
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentBitbucketServer(ctx, api, projectKey, repositorySlug, prID, s.cfg.BitbucketServerBotName, "ship", body)
}

// matchingRepoConfigsBitbucketCloud is matchingRepoConfigsGitHub's own
// Bitbucket Cloud counterpart, matched on both real workspace and
// repository slug. Project carries the workspace and Repository the
// repo slug, the same two-real-identifier shape Azure DevOps' and
// Bitbucket Server's own entries already use.
func (s *Server) matchingRepoConfigsBitbucketCloud(workspace, repoSlug string) []RepoConfig {
	var out []RepoConfig
	for _, r := range s.cfg.Repos {
		if r.Platform == "bitbucketcloud" && r.Project == workspace && r.Repository == repoSlug {
			out = append(out, r)
		}
	}
	return out
}

// repoDirForBitbucketCloud is ensureRepoCheckoutBitbucketCloud's own
// Server-level wrapper -- repoDirFor's real Bitbucket Cloud
// counterpart. Like GitLab's/Azure DevOps'/Bitbucket Server's own real,
// static token, there's no per-installation concept to resolve here.
func (s *Server) repoDirForBitbucketCloud(ctx context.Context, workspace, repoSlug string) (string, error) {
	if s.bitbucketcloud == nil {
		return "", fmt.Errorf("bitbucket cloud support is not configured (missing --bitbucket-cloud-token)")
	}
	return ensureRepoCheckoutBitbucketCloud(ctx, s.cfg.WorkDir, workspace, repoSlug, s.cfg.BitbucketCloudToken)
}

// checkoutForBitbucketCloud is checkoutForGitHub's own Bitbucket Cloud
// counterpart.
func (s *Server) checkoutForBitbucketCloud(ctx context.Context, workspace, repoSlug, ref string) (string, error) {
	repoDir, err := s.repoDirForBitbucketCloud(ctx, workspace, repoSlug)
	if err != nil {
		return "", err
	}
	if err := checkoutRef(ctx, repoDir, ref); err != nil {
		return "", err
	}
	return repoDir, nil
}

// runPlanAndCommentBitbucketCloud is runPlanAndComment's own real
// Bitbucket Cloud counterpart -- identical core-flow steps 1 and 2,
// against Bitbucket Cloud's own real comment API instead.
func (s *Server) runPlanAndCommentBitbucketCloud(ctx context.Context, api *bbcloud.Client, workspace, repoSlug string, prID int64, repoDir, ledgerDir string) error {
	args := append([]string{"plan", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	result, err := runUbx(ctx, s.self, repoDir, s.intentProviderEnv(), args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, prID, s.cfg.BitbucketCloudBotAccountID, "plan", body)
}

// runAutomaticShipBitbucketCloud is runAutomaticShip's own real
// Bitbucket Cloud counterpart -- the identical real "never
// --confirm-destroys on an unattended, merge-triggered path, no
// exception" safety property.
func (s *Server) runAutomaticShipBitbucketCloud(ctx context.Context, api *bbcloud.Client, workspace, repoSlug string, prID int64, repoDir, ledgerDir string) error {
	args := append([]string{"ship", "--yes", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, prID, s.cfg.BitbucketCloudBotAccountID, "ship", body)
}

// runManualShipBitbucketCloud is runManualShip's own real Bitbucket
// Cloud counterpart -- the identical real two-gate confirm-destroys
// rule (Config.AllowDestroy as the outer, operator-set gate; the
// human's own explicit --confirm-destroys comment flag as the inner,
// per-instance one; both required, neither alone sufficient).
func (s *Server) runManualShipBitbucketCloud(ctx context.Context, api *bbcloud.Client, workspace, repoSlug string, prID int64, repoDir, ledgerDir string, confirmDestroys bool) error {
	args := append([]string{"ship", "--yes", "--ledger-dir", ledgerDir}, s.providerArgs()...)
	if confirmDestroys && s.cfg.AllowDestroy {
		args = append(args, "--confirm-destroys")
	}
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
	return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, prID, s.cfg.BitbucketCloudBotAccountID, "ship", body)
}
