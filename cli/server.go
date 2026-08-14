package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/ubiquex/ubiquex/server"
)

func newServerCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run ubx server: a self-hosted automation daemon for plan/accept/ship/drift-watch (UBI-28: GitHub, GitLab)",
		Long: `ubx server turns the same real plan/accept/ship/status --drift flow into a continuously-running
daemon, reacting to webhook events instead of a human or a CI job invoking each command by hand.

Real, current scope (UBI-28): GitHub (a real, installable GitHub App) and GitLab (a real Group Access
Token plus a separately-configured webhook signing token) -- webhooks land on "/webhook/github" and
"/webhook/gitlab" respectively. Azure DevOps/Bamboo are real, planned follow-up phases, not yet built.

The real flow: a PR/MR opening runs "ubx plan" automatically, posted as a single PR/MR comment; its own
creator can comment "ubx plan" again to re-run it, edited in place; a native Approve action -- GitHub's
own review, GitLab's own approval -- never a comment, derives acceptance; a merge ships automatically
(policy-gated) or a CODEOWNERS-authorized "ubx ship" comment ships manually; destroy is disabled by
default and, once enabled, still needs a separate, explicit --confirm-destroys in the comment itself,
every time.

Same binary as ubx itself, not a second codebase -- every actual plan/accept/ship/scan operation shells
back out to this exact executable, reusing its real safety properties (confirm-destroys enforcement,
freshness re-verification) exactly as a human invoking it directly would.

Configuration cascades flags > environment variables (UBX_SERVER_*) > --config's own YAML file > built-in
defaults -- see the Configuration reference for the complete, real key mapping.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := server.Load(configPath, cmd.Flags())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("server: %w", err)}
			}

			s, err := server.New(cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("server: %w", err)}
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if err := s.Run(ctx); err != nil {
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("server: %w", err)}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "path to a YAML config file (see the Configuration reference for the real schema)")
	cmd.Flags().String("listen-addr", "", "address to listen for GitHub webhooks on (default \":8080\")")
	cmd.Flags().String("work-dir", "", "directory to clone/fetch watched repos into (default \"/var/lib/ubx-server/repos\")")
	cmd.Flags().Int64("github-app-id", 0, "the GitHub App's own numeric ID")
	cmd.Flags().String("github-app-private-key-path", "", "path to the GitHub App's own PEM private key")
	cmd.Flags().String("github-bot-login", "", "the GitHub App's own bot user login, \"<app-slug>[bot]\" (default \"ubx[bot]\")")
	cmd.Flags().String("github-webhook-secret", "", "the webhook secret configured on the GitHub App (required; also settable via UBX_SERVER_GITHUB_WEBHOOK_SECRET, never the YAML file)")
	cmd.Flags().String("github-api-base-url", "", "override the GitHub API base URL (test-only)")
	cmd.Flags().String("gitlab-token", "", "the GitLab Group Access Token to authenticate as (required for GitLab support; also settable via UBX_SERVER_GITLAB_TOKEN, never the YAML file)")
	cmd.Flags().String("gitlab-bot-username", "", "the Group Access Token's own bot user username, as GitLab assigned it at token-creation time")
	cmd.Flags().String("gitlab-webhook-secret", "", "the webhook signing token (\"whsec_...\") configured on the GitLab webhook (required for GitLab support; also settable via UBX_SERVER_GITLAB_WEBHOOK_SECRET, never the YAML file)")
	cmd.Flags().String("gitlab-api-base-url", "", "override the GitLab API base URL (a self-managed GitLab instance, or test-only; default the real gitlab.com)")
	cmd.Flags().String("intent-provider-key", "", "the [intent] provider's own API key, forwarded to \"ubx plan\" as UBX_INTENT_PROVIDER_KEY for markdown-authored (--from-doc) proposals (also settable via UBX_SERVER_INTENT_PROVIDER_KEY, never the YAML file; unset means markdown-authored proposals aren't supported)")
	cmd.Flags().String("provider-source", "", "provider source for plan/ship/status --drift, e.g. hashicorp/aws")
	cmd.Flags().String("provider-version", "", "provider version to acquire")
	cmd.Flags().String("provider-config", "", "JSON object configuring the provider (default \"{}\")")
	cmd.Flags().Bool("ship-on-merge", true, "attempt an automatic ship when a watched PR merges")
	cmd.Flags().Bool("allow-destroy", false, "allow a manual \"ubx ship --confirm-destroys\" comment to actually pass --confirm-destroys through (default false -- destroy is disabled by default)")
	cmd.Flags().String("surface-as", "", "how drift-watch surfaces real drift: \"issue\" or \"pr\" (default \"issue\")")
	cmd.Flags().String("drift-watch-interval", "", "how often to run the drift-watch loop, a Go duration (default \"24h\")")
	cmd.Flags().StringArray("repo", nil, "a watched repo: \"owner/name\" or \"owner/name:ledger_dir\" for GitHub, \"gitlab:namespace/project\" or \"gitlab:namespace/project:ledger_dir\" for GitLab (repeatable; the YAML file's own repos: list is the alternative for more than a few)")

	return cmd
}
