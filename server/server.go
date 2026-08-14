package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	glab "github.com/ubiquex/ubiquex/gitlab"
)

// Server is ubx server's own real, running instance -- everything
// New wires together, and Run drives until ctx is cancelled.
type Server struct {
	cfg           *Config
	installations *installationClients
	botLogin      string
	gitlab        *glab.Client // nil when Config.GitLabToken is unset -- GitLab support not configured
	self          string       // this process's own resolved executable path (exec.go)
}

// New builds a Server from cfg, resolving the GitHub App credentials and
// this process's own executable path once, up front -- both real,
// fail-fast checks (a bad private key path or a resolve failure should
// stop startup, not surface as a mysterious first-webhook error).
// GitLab's own client is only built when Config.GitLabToken is set --
// unlike GitHub, GitLab support is genuinely optional per deployment
// (Phase 1 configs never set it at all), so no GitLab field being unset
// is itself a real, valid startup state, not an error.
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

	return &Server{
		cfg:           cfg,
		installations: installations,
		botLogin:      cfg.GitHubBotLogin,
		gitlab:        gitlabClient,
		self:          self,
	}, nil
}

// Run serves the webhook endpoints and drives the drift-watch loop until
// ctx is cancelled, then shuts the HTTP server down gracefully.
// "/webhook/github" and "/webhook/gitlab" are real, separate delivery
// targets per platform -- Phase 1's original single "/webhook" route is
// renamed here (never deployed anywhere with real users depending on the
// old form yet, so this is a safe, proactive rename, not a breaking
// change to anything real) rather than kept as a third, redundant alias.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/webhook/github", webhookHandler{s: s})
	mux.Handle("/webhook/gitlab", gitlabWebhookHandler{s: s})
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

// ledgerDirFor looks up owner/repo's own configured LedgerDir --
// Config.Repos' own real per-repo setting -- falling back to "." (`ubx
// plan`'s own default) for a repo a webhook fired for but that isn't
// explicitly listed in Config.Repos, the same forgiving default the CLI
// itself already applies when --ledger-dir is omitted.
func (s *Server) ledgerDirFor(owner, repo string) string {
	for _, r := range s.cfg.Repos {
		if r.Owner == owner && r.Name == repo {
			return r.LedgerDir
		}
	}
	return "."
}

// repoDirFor is ensureRepoCheckout's own Server-level wrapper: resolve a
// real installation token, then guarantee a real, current working tree
// for owner/repo under Config.WorkDir.
func (s *Server) repoDirFor(ctx context.Context, installationID int64, owner, repo string) (string, error) {
	token, err := s.installations.installationToken(ctx, installationID)
	if err != nil {
		return "", err
	}
	return ensureRepoCheckout(ctx, s.cfg.WorkDir, owner, repo, token)
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
func (s *Server) runPlanAndComment(ctx context.Context, installationID int64, owner, repo string, prNumber int, headSHA string) error {
	api, err := s.installations.forInstallation(installationID)
	if err != nil {
		return err
	}
	token, err := s.installations.installationToken(ctx, installationID)
	if err != nil {
		return err
	}
	repoDir, err := ensureRepoCheckout(ctx, s.cfg.WorkDir, owner, repo, token)
	if err != nil {
		return err
	}
	if err := checkoutRef(ctx, repoDir, headSHA); err != nil {
		return err
	}

	args := append([]string{"plan", "--ledger-dir", s.ledgerDirFor(owner, repo)}, s.providerArgs()...)
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
func (s *Server) runAutomaticShip(ctx context.Context, installationID int64, owner, repo string, prNumber int, baseRef string) error {
	api, err := s.installations.forInstallation(installationID)
	if err != nil {
		return err
	}
	token, err := s.installations.installationToken(ctx, installationID)
	if err != nil {
		return err
	}
	repoDir, err := ensureRepoCheckout(ctx, s.cfg.WorkDir, owner, repo, token)
	if err != nil {
		return err
	}
	if err := checkoutRef(ctx, repoDir, baseRef); err != nil {
		return err
	}

	args := append([]string{"ship", "--yes", "--ledger-dir", s.ledgerDirFor(owner, repo)}, s.providerArgs()...)
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
func (s *Server) runManualShip(ctx context.Context, installationID int64, owner, repo string, prNumber int, headSHA string, confirmDestroys bool) error {
	api, err := s.installations.forInstallation(installationID)
	if err != nil {
		return err
	}
	token, err := s.installations.installationToken(ctx, installationID)
	if err != nil {
		return err
	}
	repoDir, err := ensureRepoCheckout(ctx, s.cfg.WorkDir, owner, repo, token)
	if err != nil {
		return err
	}
	if err := checkoutRef(ctx, repoDir, headSHA); err != nil {
		return err
	}

	args := append([]string{"ship", "--yes", "--ledger-dir", s.ledgerDirFor(owner, repo)}, s.providerArgs()...)
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

// ledgerDirForGitLab is ledgerDirFor's own GitLab counterpart, matched
// on project's real, full namespace path rather than a separate
// owner/name pair.
func (s *Server) ledgerDirForGitLab(project string) string {
	for _, r := range s.cfg.Repos {
		if r.Platform == "gitlab" && r.Project == project {
			return r.LedgerDir
		}
	}
	return "."
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
func (s *Server) runPlanAndCommentGitLab(ctx context.Context, api *glab.Client, project string, mrIID int64, headSHA string) error {
	repoDir, err := s.repoDirForGitLab(ctx, project)
	if err != nil {
		return err
	}
	if err := checkoutRef(ctx, repoDir, headSHA); err != nil {
		return err
	}

	args := append([]string{"plan", "--ledger-dir", s.ledgerDirForGitLab(project)}, s.providerArgs()...)
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
func (s *Server) runAutomaticShipGitLab(ctx context.Context, api *glab.Client, project string, mrIID int64, baseRef string) error {
	repoDir, err := s.repoDirForGitLab(ctx, project)
	if err != nil {
		return err
	}
	if err := checkoutRef(ctx, repoDir, baseRef); err != nil {
		return err
	}

	args := append([]string{"ship", "--yes", "--ledger-dir", s.ledgerDirForGitLab(project)}, s.providerArgs()...)
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
func (s *Server) runManualShipGitLab(ctx context.Context, api *glab.Client, project string, mrIID int64, headSHA string, confirmDestroys bool) error {
	repoDir, err := s.repoDirForGitLab(ctx, project)
	if err != nil {
		return err
	}
	if err := checkoutRef(ctx, repoDir, headSHA); err != nil {
		return err
	}

	args := append([]string{"ship", "--yes", "--ledger-dir", s.ledgerDirForGitLab(project)}, s.providerArgs()...)
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
