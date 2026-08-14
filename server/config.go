// Package server is `ubx server`: a self-hosted automation daemon that
// turns the same real CLI flows (plan/accept/ship/status --drift/scan
// --surface-as) into a continuously-running process, reacting to
// webhook events instead of a human or a CI job invoking each command
// by hand. Same binary as ubx itself, not a second codebase -- Server
// shells out to its own already-running executable for every actual
// plan/accept/ship/scan operation (see exec.go), reusing that logic
// exactly as-is rather than re-implementing or duplicating any of its
// safety properties (confirm-destroys, freshness re-verification) a
// second time in this package.
//
// GitHub (UBI-28 Phase 1), GitLab (UBI-28 Phase 2), Azure DevOps (UBI-28
// Phase 3), and Bamboo/Bitbucket Server (UBI-28 Phase 4, final) -- see
// bitbucketserver/client.go's own doc comment for why Bitbucket Server,
// not Bamboo itself, is the real integration surface (UBI-165 tracks
// the real, separate drift-watch/cli-surface gap found during Phase 2,
// confirmed to apply identically here, not blocking any phase). All
// four platforms' own real mechanisms are genuinely different, not
// symmetric: GitHub is a real installable App with short-lived
// per-installation tokens (githubapp.go); GitLab has no unified "App"
// concept at all -- confirmed directly against GitLab's own current
// docs -- so it authenticates with one real, static Group Access Token
// instead (gitlab.go), with its own real HMAC-SHA256 webhook
// signing-token mechanism; Azure DevOps has neither an App concept nor
// any real webhook signature scheme at all -- confirmed directly
// against Microsoft's own current docs, not assumed symmetric with
// either -- it authenticates with one real, static Personal Access
// Token, and its own Service Hooks webhook mechanism carries no
// cryptographic signature whatsoever, only an optional Basic-Auth
// header or custom headers on the subscription; this package follows
// Microsoft's own documented security recommendation (a static,
// shared-secret custom header, checked on every incoming request)
// rather than inventing a signing scheme Azure DevOps itself doesn't
// have. Bitbucket Server has real, bundled (no plugin required, since
// version 5.4) native webhooks with a real HMAC-SHA256 signature --
// confirmed directly against Atlassian's own current docs, contradicting
// this ticket's own initial "likely a plugin" suspicion, not assumed
// either way -- authenticating with one real, static HTTP access
// token, the same "one instance, one credential" shape GitLab's/Azure
// DevOps' own single client already have.
package server

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// Config is ubx server's own startup configuration -- deliberately
// separate from cli.Config (`.ubx/config`), which is per-stack-repo
// defaults for a human's own CLI invocations. This is daemon-level
// configuration: which repos to watch, how to authenticate to GitHub,
// and which policies gate the automated parts of the flow.
//
// Real, confirmed precedence: flags override environment variables,
// environment variables override the YAML file, YAML overrides these
// built-in defaults. Load implements that cascade directly -- lowest
// precedence applied first, each subsequent layer only overwriting a
// field the layer above it actually set.
//
// Every scalar field has a real YAML key, a real env var
// (UBX_SERVER_<UPPER_SNAKE_CASE key>), and a real flag
// (--kebab-case-key) -- documented on the field itself, not left
// implicit. GitHubWebhookSecret is the one deliberate exception: it has
// an env var and a flag, but no YAML key at all -- a webhook secret is
// exactly the kind of value that shouldn't be sitting in a config file
// that might get committed, the same "self-provisioned, masked variable,
// never literally in the file" discipline the CI/CD integration guides
// already apply to every platform token they use. Repos is YAML-only for
// the opposite reason: it's a real list of structured objects, not a
// scalar a single flag or env var can sensibly represent -- --repo is a
// repeatable flag offered as a real, documented alternative for the
// common one-or-few-repos case, not a full substitute for the YAML list.
type Config struct {
	// ListenAddr: yaml `listen_addr`, env UBX_SERVER_LISTEN_ADDR, flag --listen-addr.
	ListenAddr string `yaml:"listen_addr"`
	// WorkDir: yaml `work_dir`, env UBX_SERVER_WORK_DIR, flag --work-dir.
	// Where ubx server clones/fetches each watched repo's own working tree.
	WorkDir string `yaml:"work_dir"`

	// GitHubAppID: yaml `github_app_id`, env UBX_SERVER_GITHUB_APP_ID, flag --github-app-id.
	GitHubAppID int64 `yaml:"github_app_id"`
	// GitHubAppPrivateKeyPath: yaml `github_app_private_key_path`, env
	// UBX_SERVER_GITHUB_APP_PRIVATE_KEY_PATH, flag --github-app-private-key-path.
	GitHubAppPrivateKeyPath string `yaml:"github_app_private_key_path"`
	// GitHubBotLogin: yaml `github_bot_login`, env
	// UBX_SERVER_GITHUB_BOT_LOGIN, flag --github-bot-login. Default
	// "ubx[bot]". A real GitHub App's own bot user login is always
	// "<app-slug>[bot]" (GitHub's own fixed convention), but the App's
	// own slug is whatever name its operator gave it at creation time --
	// not something ubx can assume in advance -- so this is operator-set
	// config, not derived at runtime. Used to attribute every drift-
	// watch-opened PR (drift.go) and to find "this bot's own last
	// comment" for the edit-in-place mechanism (comment.go).
	GitHubBotLogin string `yaml:"github_bot_login"`
	// GitHubWebhookSecret: env UBX_SERVER_GITHUB_WEBHOOK_SECRET, flag
	// --github-webhook-secret. Deliberately no YAML key -- see the type
	// doc comment above.
	GitHubWebhookSecret string `yaml:"-"`
	// GitHubAPIBaseURL: env UBX_SERVER_GITHUB_API_BASE_URL, flag
	// --github-api-base-url. Deliberately no YAML key, test-only --
	// points the installation client at an httptest.Server instead of
	// the real api.github.com, same real-transport-fake-fixture pattern
	// UBX_GITHUB_API_BASE_URL already uses elsewhere in this codebase.
	GitHubAPIBaseURL string `yaml:"-"`

	// GitLabToken: env UBX_SERVER_GITLAB_TOKEN, flag --gitlab-token.
	// Deliberately no YAML key -- a real Group Access Token, the same
	// "self-provisioned, masked variable, never literally in the file"
	// reasoning GitHubWebhookSecret's own doc comment gives. Needs the
	// `api` scope (Notes/MergeRequests/Approvals/project-settings calls)
	// and `read_repository` (git clone/fetch over HTTPS).
	GitLabToken string `yaml:"-"`
	// GitLabBotUsername: yaml `gitlab_bot_username`, env
	// UBX_SERVER_GITLAB_BOT_USERNAME, flag --gitlab-bot-username. A
	// Group Access Token's own bot user is created automatically by
	// GitLab at token-creation time, with a real, GitLab-chosen
	// username -- not a fixed, predictable convention the way a GitHub
	// App's own "<slug>[bot]" is, so this is operator-set config, read
	// directly off the created bot user, not derived. Same real role
	// GitHubBotLogin plays: attributing drift-watch-opened MRs and
	// finding "this bot's own last note" for the edit-in-place
	// mechanism.
	GitLabBotUsername string `yaml:"gitlab_bot_username"`
	// GitLabWebhookSecret: env UBX_SERVER_GITLAB_WEBHOOK_SECRET, flag
	// --gitlab-webhook-secret. Deliberately no YAML key, same reasoning
	// as GitHubWebhookSecret. This is GitLab's own current, recommended
	// "signing token" (the `whsec_...`-prefixed value GitLab generates
	// once when you click "Generate signing token" on a webhook, never
	// shown again) -- not the older, GitLab-labeled-"not recommended"
	// plain-text secret-token header, which this package doesn't
	// support at all.
	GitLabWebhookSecret string `yaml:"-"`
	// GitLabAPIBaseURL: env UBX_SERVER_GITLAB_API_BASE_URL, flag
	// --gitlab-api-base-url. Deliberately no YAML key. Real, dual
	// purpose: a self-managed GitLab instance's own real API base (not
	// just test-only the way GitHubAPIBaseURL is, since GitLab, unlike
	// a GitHub App, has no single fixed host every real deployment
	// shares) -- empty means the real gitlab.com.
	GitLabAPIBaseURL string `yaml:"-"`

	// AzureDevOpsOrganization: yaml `azure_devops_organization`, env
	// UBX_SERVER_AZURE_DEVOPS_ORGANIZATION, flag
	// --azure-devops-organization. The real Azure DevOps organization
	// name (the first path segment of https://dev.azure.com/
	// {organization}) -- one real PAT authenticates every call this
	// server makes across every Azure-DevOps-watched repo, the same
	// "one instance, one credential" simplification GitLab's own
	// GitLabToken already makes; a server watching repos across more
	// than one real Azure DevOps organization is out of scope (would
	// need a second, per-organization client, not built here).
	AzureDevOpsOrganization string `yaml:"azure_devops_organization"`
	// AzureDevOpsToken: env UBX_SERVER_AZURE_DEVOPS_TOKEN, flag
	// --azure-devops-token. Deliberately no YAML key, same secrets
	// discipline as GitLabToken. A real Personal Access Token, needing
	// Code (Read & Write) and Graph (Read) scopes -- Code for every
	// git/policy/comment-thread call this package makes, Graph for the
	// user/group lookups isAuthorizedToReplanAzureDevOps/
	// isAuthorizedToShipAzureDevOps need.
	AzureDevOpsToken string `yaml:"-"`
	// AzureDevOpsBotDisplayName: yaml `azure_devops_bot_display_name`,
	// env UBX_SERVER_AZURE_DEVOPS_BOT_DISPLAY_NAME, flag
	// --azure-devops-bot-display-name. The PAT's own real, current
	// display name (Azure DevOps has no fixed bot-naming convention at
	// all, unlike GitHub's "<slug>[bot]" -- a PAT authenticates as
	// whichever real user or service account created it) -- operator-
	// set config, read directly off that identity, not derived. Same
	// real role GitHubBotLogin/GitLabBotUsername play: attributing
	// drift-watch-opened PRs and finding "this bot's own last comment"
	// for the edit-in-place mechanism.
	AzureDevOpsBotDisplayName string `yaml:"azure_devops_bot_display_name"`
	// AzureDevOpsWebhookSecretHeader: yaml
	// `azure_devops_webhook_secret_header`, env
	// UBX_SERVER_AZURE_DEVOPS_WEBHOOK_SECRET_HEADER, flag
	// --azure-devops-webhook-secret-header. Default
	// "X-Ubx-Webhook-Secret". Azure DevOps' own Service Hooks have no
	// real webhook signature scheme at all -- confirmed directly
	// against Microsoft's own current docs -- only an optional custom-
	// header mechanism the operator configures on the subscription
	// itself; Microsoft's own documented security recommendation is a
	// shared secret in a custom header, checked on every incoming
	// request, which is what this package implements. The header NAME
	// is configurable (not a fixed Azure-DevOps-chosen convention the
	// way GitHub's/GitLab's signature header names are); its value is
	// AzureDevOpsWebhookSecret below.
	AzureDevOpsWebhookSecretHeader string `yaml:"azure_devops_webhook_secret_header"`
	// AzureDevOpsWebhookSecret: env UBX_SERVER_AZURE_DEVOPS_WEBHOOK_SECRET,
	// flag --azure-devops-webhook-secret. Deliberately no YAML key, same
	// secrets discipline as every other webhook secret. The real,
	// static value the operator both sets as the custom header's value
	// on every Azure DevOps Service Hook subscription AND configures
	// here -- compared via constant-time equality, never a signature
	// (there is nothing to verify a signature against; the header
	// value itself is the whole credential).
	AzureDevOpsWebhookSecret string `yaml:"-"`
	// AzureDevOpsAPIBaseURL: env UBX_SERVER_AZURE_DEVOPS_API_BASE_URL,
	// flag --azure-devops-api-base-url. Deliberately no YAML key,
	// test-only -- points both the git/policy API base and the Graph
	// API base at the same httptest.Server; real production traffic
	// always splits across dev.azure.com and vssps.dev.azure.com, two
	// genuinely different real hosts (azuredevops.Client's own real
	// default derivation), which this override deliberately collapses
	// for hermetic testing only.
	AzureDevOpsAPIBaseURL string `yaml:"-"`

	// BitbucketServerURL: yaml `bitbucket_server_url`, env
	// UBX_SERVER_BITBUCKET_SERVER_URL, flag --bitbucket-server-url. The
	// real, self-hosted Bitbucket Server/Data Center base URL (e.g.
	// "https://bitbucket.example.com") -- confirmed directly against
	// bitbucketserver.Client's own doc comment (UBI-160 Phase 3):
	// unlike GitHub/GitLab/Azure DevOps, Bitbucket Server has no single
	// fixed default host at all, so this is always required, real
	// production config, never a test-only override the way
	// AzureDevOpsAPIBaseURL is -- tests point this same field at an
	// httptest.Server instead. Matches the existing `ubx accept
	// --bitbucket-server-url` flag's own real name for the identical
	// concept, deliberate consistency across the whole CLI.
	BitbucketServerURL string `yaml:"bitbucket_server_url"`
	// BitbucketServerToken: env UBX_SERVER_BITBUCKET_SERVER_TOKEN, flag
	// --bitbucket-server-token. Deliberately no YAML key, same secrets
	// discipline as every other platform token. A real HTTP access
	// token (bitbucketserver.Client's own doc comment: personal,
	// project, or repository-scoped), needing real REPO_READ (git
	// clone/fetch, CODEOWNERS/raw-file reads) and REPO_WRITE (posting/
	// editing comments) on every Bitbucket-Server-watched repo.
	BitbucketServerToken string `yaml:"-"`
	// BitbucketServerBotName: yaml `bitbucket_server_bot_name`, env
	// UBX_SERVER_BITBUCKET_SERVER_BOT_NAME, flag
	// --bitbucket-server-bot-name. The token's own real, current
	// username (Bitbucket Server has no fixed bot-naming convention any
	// more than GitLab/Azure DevOps do) -- operator-set config, read
	// directly off that identity, not derived. Same real role
	// GitHubBotLogin/GitLabBotUsername/AzureDevOpsBotDisplayName play:
	// attributing drift-watch-opened PRs, finding "this bot's own last
	// comment" for the edit-in-place mechanism, and (real, Bitbucket-
	// Server-specific) authenticating the git-over-HTTPS clone/fetch
	// URL, since Bitbucket Server's own real HTTP access tokens clone
	// as "https://<username>:<token>@..." -- a real username is
	// required in that position, unlike GitHub's placeholder
	// "x-access-token" or GitLab's/Azure DevOps' placeholder "oauth2"
	// convention (confirmed directly against Atlassian's own current
	// HTTP access tokens docs).
	BitbucketServerBotName string `yaml:"bitbucket_server_bot_name"`
	// BitbucketServerWebhookSecret: env
	// UBX_SERVER_BITBUCKET_SERVER_WEBHOOK_SECRET, flag
	// --bitbucket-server-webhook-secret. Deliberately no YAML key, same
	// secrets discipline as every other webhook secret. Bitbucket
	// Server's own real, bundled webhook feature signs every delivery
	// with this real, static secret via HMAC-SHA256 over the raw body,
	// carried in a real "X-Hub-Signature" header as "sha256=<hex>" --
	// confirmed directly against Atlassian's own current docs (see
	// validBitbucketServerSignature's own doc comment for the real
	// header-name-collision-with-GitHub's-older-header nuance).
	BitbucketServerWebhookSecret string `yaml:"-"`

	// IntentProviderKey: env UBX_SERVER_INTENT_PROVIDER_KEY, flag
	// --intent-provider-key. Deliberately no YAML key, same
	// "self-provisioned, masked variable, never literally in the file"
	// reasoning GitHubWebhookSecret's own doc comment gives -- this is a
	// real LLM/intent-provider API key (docs/intent-provider.md), not a
	// VCS credential, but the same secrets discipline applies.
	//
	// Forwarded into every `ubx plan` subprocess (runPlanAndComment,
	// runPlanAndCommentGitLab) as UBX_INTENT_PROVIDER_KEY, a fixed name
	// every watched repo's own .ubx/config [intent].key_ref.env is
	// expected to reference -- package server never parses that
	// repo-owned TOML cascade itself to learn a repo-chosen env var
	// name (cli/config.go's own IntentConfig.KeyRef.Env can be anything
	// a given repo's operator chose), so this fixed name is the one real
	// contract point between the two. Empty is a real, legitimate
	// deployment state (an operator who never accepts markdown-authored,
	// --from-doc-drafted proposals at all needs no intent-provider
	// credential); in that case a markdown-authored PR's automatic plan
	// step fails with `ubx plan`'s own existing, real error
	// ("key_ref.env names %q, but that environment variable is unset or
	// empty"), posted as a real PR/MR comment like any other plan
	// failure -- not silently, and not this package's own separate error
	// path to build.
	IntentProviderKey string `yaml:"-"`

	// ProviderSource: yaml `provider_source`, env UBX_SERVER_PROVIDER_SOURCE, flag --provider-source.
	ProviderSource string `yaml:"provider_source"`
	// ProviderVersion: yaml `provider_version`, env UBX_SERVER_PROVIDER_VERSION, flag --provider-version.
	ProviderVersion string `yaml:"provider_version"`
	// ProviderConfig: yaml `provider_config`, env UBX_SERVER_PROVIDER_CONFIG, flag --provider-config.
	ProviderConfig string `yaml:"provider_config"`

	// ShipOnMerge: yaml `ship_on_merge`, env UBX_SERVER_SHIP_ON_MERGE,
	// flag --ship-on-merge. Default true -- ship is still gated by
	// freshness re-verification and, when a proposal is destructive, by
	// AllowDestroy below; this only controls whether a merge event
	// attempts it at all.
	ShipOnMerge bool `yaml:"ship_on_merge"`
	// AllowDestroy: yaml `allow_destroy`, env UBX_SERVER_ALLOW_DESTROY,
	// flag --allow-destroy. Default false -- destroy is disabled by
	// default, per UBI-28's own core safety properties. Turning this on
	// does not by itself make an automatic merge-triggered ship able to
	// destroy anything -- see docs/architecture.md's "ubx server" section
	// for the required extra confirmation this alone never satisfies.
	AllowDestroy bool `yaml:"allow_destroy"`
	// SurfaceAs: yaml `surface_as`, env UBX_SERVER_SURFACE_AS, flag
	// --surface-as. "issue" or "pr", default "issue" -- same
	// least-privilege-by-default reasoning cli/surface.go's own
	// --surface-as flag documents (issue mode needs only "issues: write";
	// PR mode also needs "contents: write").
	SurfaceAs string `yaml:"surface_as"`
	// DriftWatchInterval: yaml `drift_watch_interval`, env
	// UBX_SERVER_DRIFT_WATCH_INTERVAL, flag --drift-watch-interval. A
	// Go duration string ("24h", "30m"). Default "24h".
	DriftWatchInterval time.Duration `yaml:"-"`
	// DriftWatchIntervalRaw is DriftWatchInterval's own pre-parse string
	// form -- yaml.v3 has no built-in time.Duration codec, so the YAML/
	// env/flag layers all write here first, and Load parses it into
	// DriftWatchInterval once, after every layer has been applied.
	DriftWatchIntervalRaw string `yaml:"drift_watch_interval"`

	// Repos: yaml `repos` only -- see the type doc comment above.
	Repos []RepoConfig `yaml:"repos"`
}

// RepoConfig is one entry of Config.Repos: a single repository ubx
// server watches, plus where within it the ledger root actually lives
// (mirroring `ubx plan --ledger-dir`'s own default-to-"." shape).
//
// Owner/Name (GitHub), Project (GitLab), and Project+Repository (Azure
// DevOps) are deliberately separate field groups, not one single
// shared "repo path" -- a real, structural difference across all
// three platforms, not a naming quirk: GitHub's own API always
// addresses a repository by exactly two segments (owner, name);
// GitLab's own project path can carry any real number of nested
// group/subgroup segments ("acme/backend/infra" is a perfectly real
// GitLab project path), so GitLab's own API takes that whole path as
// one opaque string, never split into two fixed fields; Azure DevOps
// needs a real, separate project name/ID AND repository name/ID (a
// real, current organization can host many real repositories per
// project, and a project name alone never uniquely addresses one) --
// confirmed directly against Microsoft's own REST API shape (see
// azuredevops/client.go's own doc comment), not assumed symmetric with
// either GitHub's two-segment or GitLab's one-opaque-string model.
type RepoConfig struct {
	// Platform: yaml `platform`. "github" (default, for backward
	// compatibility with every Phase-1-era config), "gitlab",
	// "azuredevops", or "bitbucketserver".
	Platform string `yaml:"platform"`

	// Owner/Name: yaml `owner`/`name` -- GitHub only.
	Owner string `yaml:"owner"`
	Name  string `yaml:"name"`

	// Project: yaml `project` -- GitLab's own full real namespace path
	// ("acme/infra" or "acme/backend/infra"), Azure DevOps' own real
	// project name/ID, or Bitbucket Server's own real project key
	// (used together with Repository below in both of the latter two
	// cases).
	Project string `yaml:"project"`

	// Repository: yaml `repository` -- Azure DevOps' own real
	// repository name or ID, or Bitbucket Server's own real repository
	// slug, within Project.
	Repository string `yaml:"repository"`

	LedgerDir string `yaml:"ledger_dir"`
}

// String renders r's own real repository address for logging --
// "owner/name" for GitHub, the real project path for GitLab,
// "project/repository" for Azure DevOps and Bitbucket Server (Project
// is a project key, Repository a repository slug, for the latter).
func (r RepoConfig) String() string {
	switch r.Platform {
	case "gitlab":
		return r.Project
	case "azuredevops", "bitbucketserver":
		return r.Project + "/" + r.Repository
	default:
		return r.Owner + "/" + r.Name
	}
}

// defaults returns Config's built-in defaults -- the lowest-precedence
// layer Load starts from.
func defaults() Config {
	return Config{
		ListenAddr:                     ":8080",
		WorkDir:                        "/var/lib/ubx-server/repos",
		DriftWatchIntervalRaw:          "24h",
		SurfaceAs:                      "issue",
		ShipOnMerge:                    true,
		AllowDestroy:                   false,
		GitHubAppPrivateKeyPath:        "",
		GitHubBotLogin:                 "ubx[bot]",
		AzureDevOpsWebhookSecretHeader: "X-Ubx-Webhook-Secret",
	}
}

// Load builds Config from the real, confirmed four-layer cascade:
// defaults, then yamlPath's own file if it names one that exists, then
// the process environment, then flags -- each layer only overwriting
// what it actually sets, never blanking a lower layer's value with its
// own zero value. flags may be nil (server started with none set).
func Load(yamlPath string, flags *pflag.FlagSet) (*Config, error) {
	cfg := defaults()

	if yamlPath != "" {
		data, err := os.ReadFile(yamlPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read config file %s: %w", yamlPath, err)
			}
		} else {
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("parse config file %s: %w", yamlPath, err)
			}
		}
	}

	applyEnv(&cfg)

	if flags != nil {
		if err := applyFlags(&cfg, flags); err != nil {
			return nil, err
		}
	}

	dur, err := time.ParseDuration(cfg.DriftWatchIntervalRaw)
	if err != nil {
		return nil, fmt.Errorf("drift_watch_interval %q: %w", cfg.DriftWatchIntervalRaw, err)
	}
	cfg.DriftWatchInterval = dur

	// An empty Platform defaults to "github" -- real backward
	// compatibility for every Phase-1-era YAML config, which never had
	// a platform key to set at all (RepoConfig had no other real field
	// it could mean).
	for i := range cfg.Repos {
		if cfg.Repos[i].Platform == "" {
			cfg.Repos[i].Platform = "github"
		}
	}

	return &cfg, nil
}

// applyEnv is the cascade's env-var layer -- UBX_SERVER_<KEY>, only
// overwriting a field whose env var is actually set (an unset env var
// must never blank out whatever the YAML layer already set).
func applyEnv(cfg *Config) {
	if v, ok := os.LookupEnv("UBX_SERVER_LISTEN_ADDR"); ok {
		cfg.ListenAddr = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_WORK_DIR"); ok {
		cfg.WorkDir = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_GITHUB_APP_ID"); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.GitHubAppID = n
		}
	}
	if v, ok := os.LookupEnv("UBX_SERVER_GITHUB_APP_PRIVATE_KEY_PATH"); ok {
		cfg.GitHubAppPrivateKeyPath = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_GITHUB_BOT_LOGIN"); ok {
		cfg.GitHubBotLogin = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_GITHUB_WEBHOOK_SECRET"); ok {
		cfg.GitHubWebhookSecret = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_GITHUB_API_BASE_URL"); ok {
		cfg.GitHubAPIBaseURL = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_GITLAB_TOKEN"); ok {
		cfg.GitLabToken = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_GITLAB_BOT_USERNAME"); ok {
		cfg.GitLabBotUsername = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_GITLAB_WEBHOOK_SECRET"); ok {
		cfg.GitLabWebhookSecret = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_GITLAB_API_BASE_URL"); ok {
		cfg.GitLabAPIBaseURL = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_AZURE_DEVOPS_ORGANIZATION"); ok {
		cfg.AzureDevOpsOrganization = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_AZURE_DEVOPS_TOKEN"); ok {
		cfg.AzureDevOpsToken = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_AZURE_DEVOPS_BOT_DISPLAY_NAME"); ok {
		cfg.AzureDevOpsBotDisplayName = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_AZURE_DEVOPS_WEBHOOK_SECRET_HEADER"); ok {
		cfg.AzureDevOpsWebhookSecretHeader = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_AZURE_DEVOPS_WEBHOOK_SECRET"); ok {
		cfg.AzureDevOpsWebhookSecret = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_AZURE_DEVOPS_API_BASE_URL"); ok {
		cfg.AzureDevOpsAPIBaseURL = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_BITBUCKET_SERVER_URL"); ok {
		cfg.BitbucketServerURL = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_BITBUCKET_SERVER_TOKEN"); ok {
		cfg.BitbucketServerToken = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_BITBUCKET_SERVER_BOT_NAME"); ok {
		cfg.BitbucketServerBotName = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_BITBUCKET_SERVER_WEBHOOK_SECRET"); ok {
		cfg.BitbucketServerWebhookSecret = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_INTENT_PROVIDER_KEY"); ok {
		cfg.IntentProviderKey = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_PROVIDER_SOURCE"); ok {
		cfg.ProviderSource = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_PROVIDER_VERSION"); ok {
		cfg.ProviderVersion = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_PROVIDER_CONFIG"); ok {
		cfg.ProviderConfig = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_SHIP_ON_MERGE"); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.ShipOnMerge = b
		}
	}
	if v, ok := os.LookupEnv("UBX_SERVER_ALLOW_DESTROY"); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.AllowDestroy = b
		}
	}
	if v, ok := os.LookupEnv("UBX_SERVER_SURFACE_AS"); ok {
		cfg.SurfaceAs = v
	}
	if v, ok := os.LookupEnv("UBX_SERVER_DRIFT_WATCH_INTERVAL"); ok {
		cfg.DriftWatchIntervalRaw = v
	}
}

// applyFlags is the cascade's top layer -- only a flag the caller
// actually changed from its own zero-value default overwrites cfg
// (pflag.Changed is exactly this "was it really passed" signal; without
// it, an unset flag's own zero value would silently blank out whatever
// the env/YAML layers already set, inverting the real precedence order).
func applyFlags(cfg *Config, flags *pflag.FlagSet) error {
	if flags.Changed("listen-addr") {
		cfg.ListenAddr, _ = flags.GetString("listen-addr")
	}
	if flags.Changed("work-dir") {
		cfg.WorkDir, _ = flags.GetString("work-dir")
	}
	if flags.Changed("github-app-id") {
		cfg.GitHubAppID, _ = flags.GetInt64("github-app-id")
	}
	if flags.Changed("github-app-private-key-path") {
		cfg.GitHubAppPrivateKeyPath, _ = flags.GetString("github-app-private-key-path")
	}
	if flags.Changed("github-bot-login") {
		cfg.GitHubBotLogin, _ = flags.GetString("github-bot-login")
	}
	if flags.Changed("github-webhook-secret") {
		cfg.GitHubWebhookSecret, _ = flags.GetString("github-webhook-secret")
	}
	if flags.Changed("github-api-base-url") {
		cfg.GitHubAPIBaseURL, _ = flags.GetString("github-api-base-url")
	}
	if flags.Changed("gitlab-token") {
		cfg.GitLabToken, _ = flags.GetString("gitlab-token")
	}
	if flags.Changed("gitlab-bot-username") {
		cfg.GitLabBotUsername, _ = flags.GetString("gitlab-bot-username")
	}
	if flags.Changed("gitlab-webhook-secret") {
		cfg.GitLabWebhookSecret, _ = flags.GetString("gitlab-webhook-secret")
	}
	if flags.Changed("gitlab-api-base-url") {
		cfg.GitLabAPIBaseURL, _ = flags.GetString("gitlab-api-base-url")
	}
	if flags.Changed("azure-devops-organization") {
		cfg.AzureDevOpsOrganization, _ = flags.GetString("azure-devops-organization")
	}
	if flags.Changed("azure-devops-token") {
		cfg.AzureDevOpsToken, _ = flags.GetString("azure-devops-token")
	}
	if flags.Changed("azure-devops-bot-display-name") {
		cfg.AzureDevOpsBotDisplayName, _ = flags.GetString("azure-devops-bot-display-name")
	}
	if flags.Changed("azure-devops-webhook-secret-header") {
		cfg.AzureDevOpsWebhookSecretHeader, _ = flags.GetString("azure-devops-webhook-secret-header")
	}
	if flags.Changed("azure-devops-webhook-secret") {
		cfg.AzureDevOpsWebhookSecret, _ = flags.GetString("azure-devops-webhook-secret")
	}
	if flags.Changed("azure-devops-api-base-url") {
		cfg.AzureDevOpsAPIBaseURL, _ = flags.GetString("azure-devops-api-base-url")
	}
	if flags.Changed("bitbucket-server-url") {
		cfg.BitbucketServerURL, _ = flags.GetString("bitbucket-server-url")
	}
	if flags.Changed("bitbucket-server-token") {
		cfg.BitbucketServerToken, _ = flags.GetString("bitbucket-server-token")
	}
	if flags.Changed("bitbucket-server-bot-name") {
		cfg.BitbucketServerBotName, _ = flags.GetString("bitbucket-server-bot-name")
	}
	if flags.Changed("bitbucket-server-webhook-secret") {
		cfg.BitbucketServerWebhookSecret, _ = flags.GetString("bitbucket-server-webhook-secret")
	}
	if flags.Changed("intent-provider-key") {
		cfg.IntentProviderKey, _ = flags.GetString("intent-provider-key")
	}
	if flags.Changed("provider-source") {
		cfg.ProviderSource, _ = flags.GetString("provider-source")
	}
	if flags.Changed("provider-version") {
		cfg.ProviderVersion, _ = flags.GetString("provider-version")
	}
	if flags.Changed("provider-config") {
		cfg.ProviderConfig, _ = flags.GetString("provider-config")
	}
	if flags.Changed("ship-on-merge") {
		cfg.ShipOnMerge, _ = flags.GetBool("ship-on-merge")
	}
	if flags.Changed("allow-destroy") {
		cfg.AllowDestroy, _ = flags.GetBool("allow-destroy")
	}
	if flags.Changed("surface-as") {
		cfg.SurfaceAs, _ = flags.GetString("surface-as")
	}
	if flags.Changed("drift-watch-interval") {
		cfg.DriftWatchIntervalRaw, _ = flags.GetString("drift-watch-interval")
	}
	if flags.Changed("repo") {
		repoFlags, _ := flags.GetStringArray("repo")
		repos, err := parseRepoFlags(repoFlags)
		if err != nil {
			return err
		}
		cfg.Repos = repos
	}
	return nil
}

// parseRepoFlags parses --repo's own real shorthand. GitHub (the
// default when no platform prefix is given, for backward compatibility
// with every Phase-1-era --repo value): "owner/name" or
// "owner/name:ledger_dir". GitLab (a real "gitlab:" prefix, since a
// GitLab project path can carry any number of real nested group/
// subgroup segments, never assumed to be exactly two like GitHub's own
// owner/name): "gitlab:namespace/project" or
// "gitlab:namespace/project:ledger_dir". Azure DevOps (a real
// "azuredevops:" prefix, needing two real, separate identifiers --
// project and repository -- never one opaque path the way GitLab's is,
// since a real Azure DevOps project name never carries nested
// segments the way a GitLab namespace can):
// "azuredevops:project/repository" or
// "azuredevops:project/repository:ledger_dir". Bitbucket Server (a real
// "bitbucketserver:" prefix, the identical two-real-identifier shape
// as Azure DevOps' own -- a real project key and repository slug,
// matching the existing `ubx accept --bitbucket-server-project
// PROJECTKEY/repository-slug` flag's own real format):
// "bitbucketserver:PROJECTKEY/repository-slug" or
// "bitbucketserver:PROJECTKEY/repository-slug:ledger_dir" -- ledger_dir
// defaults to "." when omitted in any of the four forms, matching `ubx
// plan --ledger-dir`'s own default.
func parseRepoFlags(raw []string) ([]RepoConfig, error) {
	repos := make([]RepoConfig, 0, len(raw))
	for _, r := range raw {
		if rest, ok := cutPrefix(r, "gitlab:"); ok {
			project, ledgerDir, hasLedgerDir := cutLast(rest, ':')
			if !hasLedgerDir {
				ledgerDir = "."
			}
			if project == "" {
				return nil, fmt.Errorf("--repo \"gitlab:...\" must name a real project path, got %q", r)
			}
			repos = append(repos, RepoConfig{Platform: "gitlab", Project: project, LedgerDir: ledgerDir})
			continue
		}

		if rest, ok := cutPrefix(r, "azuredevops:"); ok {
			projectRepo, ledgerDir, hasLedgerDir := cutLast(rest, ':')
			if !hasLedgerDir {
				ledgerDir = "."
			}
			project, repository, ok := cutFirst(projectRepo, '/')
			if !ok || project == "" || repository == "" {
				return nil, fmt.Errorf("--repo \"azuredevops:...\" must be \"azuredevops:project/repository\" or \"azuredevops:project/repository:ledger_dir\", got %q", r)
			}
			repos = append(repos, RepoConfig{Platform: "azuredevops", Project: project, Repository: repository, LedgerDir: ledgerDir})
			continue
		}

		if rest, ok := cutPrefix(r, "bitbucketserver:"); ok {
			projectRepo, ledgerDir, hasLedgerDir := cutLast(rest, ':')
			if !hasLedgerDir {
				ledgerDir = "."
			}
			project, repository, ok := cutFirst(projectRepo, '/')
			if !ok || project == "" || repository == "" {
				return nil, fmt.Errorf("--repo \"bitbucketserver:...\" must be \"bitbucketserver:PROJECTKEY/repository-slug\" or \"bitbucketserver:PROJECTKEY/repository-slug:ledger_dir\", got %q", r)
			}
			repos = append(repos, RepoConfig{Platform: "bitbucketserver", Project: project, Repository: repository, LedgerDir: ledgerDir})
			continue
		}

		ownerRepo, ledgerDir, hasLedgerDir := cutLast(r, ':')
		if !hasLedgerDir {
			ledgerDir = "."
		}
		owner, name, ok := cutFirst(ownerRepo, '/')
		if !ok || owner == "" || name == "" {
			return nil, fmt.Errorf("--repo must be \"owner/name\", \"owner/name:ledger_dir\", \"gitlab:namespace/project\", \"gitlab:namespace/project:ledger_dir\", \"azuredevops:project/repository\", \"azuredevops:project/repository:ledger_dir\", \"bitbucketserver:PROJECTKEY/repository-slug\", or \"bitbucketserver:PROJECTKEY/repository-slug:ledger_dir\", got %q", r)
		}
		repos = append(repos, RepoConfig{Platform: "github", Owner: owner, Name: name, LedgerDir: ledgerDir})
	}
	return repos, nil
}

func cutPrefix(s, prefix string) (rest string, found bool) {
	if len(s) < len(prefix) || s[:len(prefix)] != prefix {
		return s, false
	}
	return s[len(prefix):], true
}

func cutFirst(s string, sep byte) (before, after string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func cutLast(s string, sep byte) (before, after string, found bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
