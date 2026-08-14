package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Server is ubx server's own real, running instance -- everything
// New wires together, and Run drives until ctx is cancelled.
type Server struct {
	cfg           *Config
	installations *installationClients
	botLogin      string
	self          string // this process's own resolved executable path (exec.go)
}

// New builds a Server from cfg, resolving the GitHub App credentials and
// this process's own executable path once, up front -- both real,
// fail-fast checks (a bad private key path or a resolve failure should
// stop startup, not surface as a mysterious first-webhook error).
func New(cfg *Config) (*Server, error) {
	installations, err := newInstallationClients(cfg.GitHubAppID, cfg.GitHubAppPrivateKeyPath, cfg.GitHubAPIBaseURL)
	if err != nil {
		return nil, err
	}
	self, err := selfExePath()
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:           cfg,
		installations: installations,
		botLogin:      cfg.GitHubBotLogin,
		self:          self,
	}, nil
}

// Run serves the webhook endpoint and drives the drift-watch loop until
// ctx is cancelled, then shuts the HTTP server down gracefully. The one
// real route is "/webhook" -- GitHub's own real, single delivery target
// for every event type this server subscribes to.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/webhook", webhookHandler{s: s})
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
	result, err := runUbx(ctx, s.self, repoDir, nil, args...)
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
