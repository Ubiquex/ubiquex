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
		Short: "Run ubx server: a self-hosted automation daemon for plan/accept/ship/drift-watch (UBI-28/UBI-170: GitHub, GitLab, Azure DevOps, Bitbucket Server, Bitbucket Cloud)",
		Long: `ubx server turns the same real plan/accept/ship/status --drift flow into a continuously-running
daemon, reacting to webhook events instead of a human or a CI job invoking each command by hand.

Real, current scope (UBI-28, final): GitHub (a real, installable GitHub App), GitLab (a real Group
Access Token plus a separately-configured webhook signing token), Azure DevOps (a real Personal
Access Token plus a real, static shared-secret Service Hooks header -- Azure DevOps has no
cryptographic webhook signature scheme at all), and Bitbucket Server/Data Center (a real HTTP
access token plus Bitbucket Server's own real, bundled HMAC-SHA256 webhook signature, native since
version 5.4 with no plugin required -- see the Bitbucket Server setup guide), and Bitbucket Cloud
(a real access token plus its own HMAC X-Hub-Signature webhook signature -- a genuinely different
platform from Bitbucket Server, not a hosted variant of it: different auth model, different event
keys, no username on an account at all) -- webhooks land on "/webhook/github", "/webhook/gitlab",
"/webhook/azuredevops", "/webhook/bitbucketserver", and "/webhook/bitbucketcloud" respectively.

The real flow: a PR/MR opening runs "ubx plan" automatically, posted as a single PR/MR comment; its own
creator can comment "ubx plan" again to re-run it, edited in place; a native Approve action -- GitHub's
own review, GitLab's own approval, Azure DevOps' own vote, Bitbucket Server's or Bitbucket Cloud's
own approval -- never a comment, derives acceptance; a merge ships automatically (policy-gated) or a CODEOWNERS-authorized
"ubx ship" comment ships manually; destroy is disabled by default and, once enabled, still needs a
separate, explicit --confirm-destroys in the comment itself, every time.

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
	cmd.Flags().String("github-api-base-url", "", "a GitHub Enterprise Server instance URL, e.g. https://github.example.com (GHES' own /api/v3 and /api/uploads paths are applied for you, and the same host is used to clone; default the real api.github.com/github.com)")
	cmd.Flags().String("gitlab-token", "", "the GitLab Group Access Token to authenticate as (required for GitLab support; also settable via UBX_SERVER_GITLAB_TOKEN, never the YAML file)")
	cmd.Flags().String("gitlab-bot-username", "", "the Group Access Token's own bot user username, as GitLab assigned it at token-creation time")
	cmd.Flags().String("gitlab-webhook-secret", "", "the webhook signing token (\"whsec_...\") configured on the GitLab webhook (required for GitLab support; also settable via UBX_SERVER_GITLAB_WEBHOOK_SECRET, never the YAML file)")
	cmd.Flags().String("gitlab-api-base-url", "", "a self-managed GitLab instance URL, e.g. https://gitlab.example.com (also used to clone; default the real gitlab.com)")
	cmd.Flags().String("azure-devops-organization", "", "the real Azure DevOps organization name (the first path segment of https://dev.azure.com/{organization})")
	cmd.Flags().String("azure-devops-token", "", "the Azure DevOps Personal Access Token to authenticate as (required for Azure DevOps support; also settable via UBX_SERVER_AZURE_DEVOPS_TOKEN, never the YAML file)")
	cmd.Flags().String("azure-devops-bot-display-name", "", "the Personal Access Token's own real, current display name (Azure DevOps has no fixed bot-naming convention)")
	cmd.Flags().String("azure-devops-webhook-secret-header", "", "the custom HTTP header name every Azure DevOps Service Hook subscription carries the shared secret in (default \"X-Ubx-Webhook-Secret\" -- Azure DevOps has no webhook signature scheme at all, only Microsoft's own documented shared-secret-header recommendation)")
	cmd.Flags().String("azure-devops-webhook-secret", "", "the shared secret value configured in that same header on every Azure DevOps Service Hook subscription (required for Azure DevOps support; also settable via UBX_SERVER_AZURE_DEVOPS_WEBHOOK_SECRET, never the YAML file)")
	cmd.Flags().String("azure-devops-api-base-url", "", "an on-prem Azure DevOps Server collection URL, e.g. https://tfs.example.com/tfs/DefaultCollection (used for the git/policy API, the Graph API -- on-prem has no separate vssps host -- and cloning; default the real Azure DevOps Services)")
	cmd.Flags().String("bitbucket-server-url", "", "the real, self-hosted Bitbucket Server/Data Center base URL, e.g. https://bitbucket.example.com (required for Bitbucket Server support -- no default host exists)")
	cmd.Flags().String("bitbucket-server-token", "", "the Bitbucket Server HTTP access token to authenticate as (required for Bitbucket Server support; also settable via UBX_SERVER_BITBUCKET_SERVER_TOKEN, never the YAML file)")
	cmd.Flags().String("bitbucket-server-bot-name", "", "the token's own real, current username (Bitbucket Server has no fixed bot-naming convention; also used as the git-over-HTTPS clone username, a real Bitbucket Server requirement)")
	cmd.Flags().String("bitbucket-server-webhook-secret", "", "the real, static secret configured on every Bitbucket Server webhook, verified via its own bundled HMAC-SHA256 X-Hub-Signature header (required for Bitbucket Server support; also settable via UBX_SERVER_BITBUCKET_SERVER_WEBHOOK_SECRET, never the YAML file)")
	cmd.Flags().String("bitbucket-cloud-token", "", "the Bitbucket Cloud access token to authenticate as, repository/project/workspace scoped (required for Bitbucket Cloud support; app passwords are deprecated by Atlassian, use an access token; also settable via UBX_SERVER_BITBUCKET_CLOUD_TOKEN, never the YAML file)")
	cmd.Flags().String("bitbucket-cloud-bot-account-id", "", "the access token's own real, current account_id (Bitbucket Cloud accounts carry no username at all, and nickname/display_name are both user-changeable, so identity here is an account_id)")
	cmd.Flags().String("bitbucket-cloud-webhook-secret", "", "the real, static secret configured on every Bitbucket Cloud webhook, verified via its own HMAC X-Hub-Signature header (required for Bitbucket Cloud support; also settable via UBX_SERVER_BITBUCKET_CLOUD_WEBHOOK_SECRET, never the YAML file)")
	cmd.Flags().String("bitbucket-cloud-api-base-url", "", "override the Bitbucket Cloud API base URL (test-only; the real default is https://api.bitbucket.org/2.0)")
	cmd.Flags().String("intent-provider-key", "", "forwarded to \"ubx plan\" as UBX_INTENT_PROVIDER_KEY (also settable via UBX_SERVER_INTENT_PROVIDER_KEY, never the YAML file) -- UBI-224 removed the markdown-authored (--from-doc) mode this once served; no known live consumer left, kept only pending a decision on removing it (see UBI-224's own Stage 1 report)")
	cmd.Flags().String("provider-source", "", "provider source for plan/ship/status --drift, e.g. hashicorp/aws")
	cmd.Flags().String("provider-version", "", "provider version to acquire")
	cmd.Flags().String("provider-config", "", "JSON object configuring the provider (default \"{}\")")
	cmd.Flags().Bool("ship-on-merge", true, "attempt an automatic ship when a watched PR merges")
	cmd.Flags().Bool("allow-destroy", false, "allow a manual \"ubx ship --confirm-destroys\" comment to actually pass --confirm-destroys through (default false -- destroy is disabled by default)")
	cmd.Flags().String("surface-as", "", "how drift-watch surfaces real drift: \"issue\" or \"pr\" (default \"issue\")")
	cmd.Flags().String("drift-watch-interval", "", "how often to run the drift-watch loop, a Go duration (default \"24h\")")
	cmd.Flags().StringArray("repo", nil, "a repo ubx server is allowed to act on: \"owner/name\" for GitHub, \"gitlab:namespace/project\" for GitLab, \"azuredevops:project/repository\" for Azure DevOps, \"bitbucketserver:PROJECTKEY/repository-slug\" for Bitbucket Server, \"bitbucketcloud:workspace/repo-slug\" for Bitbucket Cloud (repeatable; the YAML file's own repos: list is the alternative for more than a few). Repository identity only -- each stack's own location is discovered from that repository's real .ubx/config, never declared here")

	return cmd
}
