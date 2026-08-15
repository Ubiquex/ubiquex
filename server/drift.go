package server

import (
	"context"
	"log/slog"
	"time"
)

// runDriftWatchLoop is UBI-28's own "Drift-watch (Surface) loop", core
// flow step 6: every Config.DriftWatchInterval, for every configured
// repo, shell out to `ubx status --drift --surface-as <issue|pr>` --
// the same, already-existing `--surface-as` machinery cli/surface.go
// implements (a drift-generated proposal opening a real issue or draft
// PR), reused exactly as-is here, not reimplemented. A drift-driven PR
// this produces is attributed to Config.GitHubBotLogin -- see auth.go's
// own isAuthorizedToReplan for the real, resolved authorization
// consequence of that.
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

// driftWatchOnce runs one real pass over every configured repo. A
// failure on one repo is logged and does not stop the others -- the
// same "one bad stack shouldn't block a fleet-wide sweep" reasoning
// `ubx status`'s own fleet report already applies.
func (s *Server) driftWatchOnce(ctx context.Context) {
	self, err := selfExePath()
	if err != nil {
		slog.Error("ubx server: drift watch: resolve own executable", "error", err)
		return
	}

	for _, r := range s.cfg.Repos {
		installationID, err := s.installations.installationIDFor(ctx, r.Owner, r.Name)
		if err != nil {
			slog.Error("ubx server: drift watch: resolve installation", "repo", r.Owner+"/"+r.Name, "error", err)
			continue
		}
		token, err := s.installations.installationToken(ctx, installationID)
		if err != nil {
			slog.Error("ubx server: drift watch: get installation token", "repo", r.Owner+"/"+r.Name, "error", err)
			continue
		}
		repoDir, err := ensureRepoCheckout(ctx, s.cfg.WorkDir, s.cfg.GitHubAPIBaseURL, r.Owner, r.Name, token)
		if err != nil {
			slog.Error("ubx server: drift watch: checkout", "repo", r.Owner+"/"+r.Name, "error", err)
			continue
		}
		if err := checkoutRef(ctx, repoDir, "main"); err != nil {
			slog.Error("ubx server: drift watch: checkout main", "repo", r.Owner+"/"+r.Name, "error", err)
			continue
		}

		// UBI-167: the stacks come from the repo's own checkout now,
		// not from a ledger_dir this entry used to declare. Unlike
		// every webhook-triggered path, drift-watch has no event and
		// therefore no changed files to disambiguate a multi-stack
		// repository with -- and it doesn't need any: drift genuinely
		// applies to every stack the repo declares, so each discovered
		// stack gets its own real `ubx status --drift` pass rather
		// than one of them being picked.
		stacks, err := discoverStacks(repoDir)
		if err != nil {
			slog.Error("ubx server: drift watch: discover stacks", "repo", r.Owner+"/"+r.Name, "error", err)
			continue
		}
		if len(stacks) == 0 {
			slog.Warn("ubx server: drift watch: skipping repository -- no .ubx/config found anywhere in its own checkout, so it declares no stack to check (UBI-167)",
				"repo", r.Owner+"/"+r.Name)
			continue
		}

		for _, ledgerDir := range stacks {
			args := []string{"status", "--drift", "--surface-as", s.cfg.SurfaceAs,
				"--ledger-dir", ledgerDir, "--github-repo", r.Owner + "/" + r.Name}
			if s.cfg.ProviderSource != "" {
				args = append(args, "--source", s.cfg.ProviderSource, "--provider-version", s.cfg.ProviderVersion)
			}
			if s.cfg.ProviderConfig != "" {
				args = append(args, "--provider-config", s.cfg.ProviderConfig)
			}

			result, err := runUbx(ctx, self, repoDir, []string{"GITHUB_TOKEN=" + token}, args...)
			if err != nil {
				slog.Error("ubx server: drift watch: run ubx status --drift",
					"repo", r.Owner+"/"+r.Name, "ledger_dir", ledgerDir, "error", err)
				continue
			}
			if result.ExitCode != 0 && result.ExitCode != 1 {
				// 0 clean, 1 drift found (and surfaced) -- both real,
				// successful outcomes; anything else is a genuine failure.
				slog.Error("ubx server: drift watch: ubx status --drift failed",
					"repo", r.Owner+"/"+r.Name, "ledger_dir", ledgerDir, "exit_code", result.ExitCode, "stderr", result.Stderr)
			}
		}
	}
}
