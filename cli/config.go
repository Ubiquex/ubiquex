package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// configFileName is the legacy, extensionless file name UBI-19
// originally used (relative to a `.ubx/` directory) -- still the third
// entry in configcascade.go's own per-directory discovery order
// (`config.hcl` -> `config.toml` -> `config` -> `config.yaml`), kept
// forever so no existing config silently stops being found.
const configFileName = ".ubx/config"

// Config is .ubx/config's parsed shape -- the five keys UBI-19 scoped in
// (docs/architecture.md): provider identity, provider configuration,
// default stack, GitHub repository, and .tf directory -- plus UBI-22's
// [k8s_audit] table, and UBI-43's own [providers]/[provider_configs]
// tables (below). --ledger-dir is deliberately not one of them.
//
// `json` tags (added UBI-32 Arc A, alongside the pre-existing `toml`
// ones) exist for exactly one reason: the cascade loader
// (configcascade.go) merges every format's own parsed generic tree
// BEFORE ever touching this struct, then decodes the single merged
// result via one JSON round-trip -- one decode path for all three
// formats, rather than three separate format-specific struct decoders.
// The `toml` tags stay for symmetry and because they're a precise
// mirror of the `json` ones (both name the identical key), not because
// TOML still decodes into this struct directly anywhere.
type Config struct {
	Provider struct {
		Path    string `toml:"path" json:"path"`
		Source  string `toml:"source" json:"source"`
		Version string `toml:"version" json:"version"`
	} `toml:"provider" json:"provider"`
	ProviderConfig  map[string]any            `toml:"provider_config" json:"provider_config"`
	Providers       map[string]string         `toml:"providers" json:"providers"`
	ProviderConfigs map[string]map[string]any `toml:"provider_configs" json:"provider_configs"`
	Stack           string                    `toml:"stack" json:"stack"`
	GithubRepo      string                    `toml:"github_repo" json:"github_repo"`
	TFDir           string                    `toml:"tf_dir" json:"tf_dir"`
	K8sAudit        K8sAuditConfig            `toml:"k8s_audit" json:"k8s_audit"`
	Ledger          LedgerConfig              `toml:"ledger" json:"ledger"`
}

// LedgerConfig is .ubx/config's [ledger] table (UBI-32 Arc B,
// docs/architecture.md -- "Ledger stores"): which LedgerStore backs this
// stack. Store empty or "git" means today's exact in-repo directory
// behavior, driven by --ledger-dir alone, unchanged -- Store only ever
// names a remote store (s3://bucket/prefix/, gs://.../azblob://...; gs/
// azblob designed but not yet wired, see docs/ledgerstore-adversarial.md).
// A stack name is always appended to Store as a further path segment
// (docs/architecture.md's own addressing rule, <base store>/<stack>/),
// never configured separately here.
type LedgerConfig struct {
	Store string `toml:"store" json:"store"`
}

// Providers/ProviderConfigs are .ubx/config's own [providers]/
// [provider_configs] tables (2026-07-18, UBI-43 session 4,
// docs/architecture.md §Multi-provider stacks): a stack's whole declared
// provider set, and each one's own configuration. Deliberately two
// separate tables, not one nested one -- [providers] is exactly the
// shape docs/architecture.md's own design room text already ratified
// (source → pinned version, a flat map, explicit pins only), never
// reopened by this session; [provider_configs] is new, additive, this
// session's own decision for the config shape the design left open
// ("likely per-source config values"):
//
//	[providers]
//	"hashicorp/aws"  = "6.60.0"
//	"hashicorp/helm" = "3.0.2"
//
//	[provider_configs."hashicorp/aws"]
//	region = "us-east-1"
//
//	[provider_configs."hashicorp/helm"]
//	kubeconfig = "~/.kube/config"
//
// A source with no matching [provider_configs] entry gets an empty `{}`
// config -- exactly `--provider-config`'s own existing default for a
// single-provider stack, extended per-source rather than reinvented.
// Both are empty (nil maps) for a single-provider stack that hasn't
// adopted this yet -- cli/ship.go and cli/resolve.go fall back to
// today's exact --provider/--source/--provider-config flow unchanged
// when Providers is empty; see cli/providerpool.go for the concrete
// executor.ApplierPool this table drives once it's populated, and
// docs/resolver.md's own staged --source/--provider-version retirement
// plan for what happens when both a table and a flag are given at once.

// K8sAuditConfig is .ubx/config's [k8s_audit] table (UBI-22,
// docs/architecture.md -- Kubernetes support): which EKS cluster's
// control-plane audit log to search for Kubernetes/Helm drift
// attribution. Unlike [provider]/[provider_config], this has no CLI flag
// equivalent -- it's config-only, and entirely optional: a zero-value
// K8sAuditConfig (Cluster == "") means attribution for a
// kubernetes_*/helm_release drift degrades to
// audit_unattributed/not_configured, never blocking detection (see
// cli/attribution.go's newAttributionBackend).
type K8sAuditConfig struct {
	// Cluster is the EKS cluster name. Empty means "not configured" --
	// the one signal newAttributionBackend checks.
	Cluster string `toml:"cluster" json:"cluster"`
	// Region is the AWS region the cluster (and its CloudWatch Logs log
	// group) lives in.
	Region string `toml:"region" json:"region"`
	// LogGroup overrides the CloudWatch Logs log group to search, for a
	// cluster whose control-plane logging wasn't left at EKS's own
	// default naming convention. Empty means k8saudit.LogGroupForCluster(Cluster).
	LogGroup string `toml:"log_group" json:"log_group"`
}

// LoadConfig discovers and parses the full .ubx/config cascade (UBI-32
// Arc A -- see configcascade.go and docs/architecture.md's "Config:
// cascading, per-key, child overrides parent"), walking from the current
// working directory upward through every parent directory, merging every
// `.ubx/config*` found per key, nearest wins. Returns a zero Config (not
// an error) if none exists anywhere up to the filesystem root -- config
// is optional everywhere; every value it could supply simply falls
// through to that flag's own existing default/required behavior instead.
// Unknown keys are reported as warnings to warnOut and otherwise
// ignored; a file that isn't valid in its own format at all is a hard
// error. Most callers want this; a caller that also needs to explain
// *where* each value came from (the provenance view, `ubx config`) wants
// LoadConfigResolved instead.
func LoadConfig(warnOut io.Writer) (*Config, error) {
	rc, err := LoadConfigResolved(warnOut)
	if err != nil {
		return nil, err
	}
	return rc.Config, nil
}

// configSearchStartDir returns where the cascade starts walking upward
// from -- the real working directory in production. A package var (not a
// bare os.Getwd() call) purely so tests can point it at an isolated temp
// directory instead: without this seam, every test in this package would
// silently depend on whether some ambient .ubx/config happens to exist
// anywhere from the real process cwd up to the filesystem root (a
// developer's home directory, say) -- exactly the kind of host-machine-
// state leak `go test ./...` staying hermetic is supposed to rule out.
var configSearchStartDir = os.Getwd

// applyStackDefault fills stack from cfg if --stack wasn't explicitly
// given on the command line. CLI flag always wins; config only ever
// fills a gap.
func applyStackDefault(cmd *cobra.Command, stack *string, cfg *Config) {
	if !cmd.Flags().Changed("stack") && cfg.Stack != "" {
		*stack = cfg.Stack
	}
}

// applyProviderDefaults fills providerPath/source/providerVersion from
// cfg's [provider] table if neither --provider nor --source was
// explicitly given -- the same mutual exclusivity the flags themselves
// already enforce, so config never has to re-decide it.
func applyProviderDefaults(cmd *cobra.Command, providerPath, source, providerVersion *string, cfg *Config) {
	if cmd.Flags().Changed("provider") || cmd.Flags().Changed("source") {
		return
	}
	switch {
	case cfg.Provider.Path != "":
		*providerPath = cfg.Provider.Path
	case cfg.Provider.Source != "":
		*source = cfg.Provider.Source
		if !cmd.Flags().Changed("provider-version") && cfg.Provider.Version != "" {
			*providerVersion = cfg.Provider.Version
		}
	}
}

// applyProviderConfigDefault fills providerConfig (a JSON string, same
// shape --provider-config already takes) from cfg's [provider_config]
// table if --provider-config wasn't explicitly given.
func applyProviderConfigDefault(cmd *cobra.Command, providerConfig *string, cfg *Config) error {
	if cmd.Flags().Changed("provider-config") || len(cfg.ProviderConfig) == 0 {
		return nil
	}
	b, err := json.Marshal(cfg.ProviderConfig)
	if err != nil {
		return fmt.Errorf("config: marshal provider_config: %w", err)
	}
	*providerConfig = string(b)
	return nil
}

// warnIfLegacyProviderFlagsGiven implements docs/resolver.md's own staged
// --source/--provider-version retirement plan, stage 2 (2026-07-18,
// UBI-43 session 4): once a stack declares a real [providers] table,
// that table is the authority for it, and the singular
// --provider/--source/--provider-version/--provider-config flags stop
// being meaningful -- but a caller who still passes them (muscle memory,
// a script written before this stack adopted the table) gets a warning,
// not a silent override or a hard error. Config always wins; the flags
// are simply ignored, loudly. Callers only reach this once they've
// already confirmed cfg.Providers is non-empty -- it doesn't re-check
// that itself, so it can't be misused as the sole gate.
func warnIfLegacyProviderFlagsGiven(cmd *cobra.Command) {
	var given []string
	for _, name := range []string{"provider", "source", "provider-version", "provider-config"} {
		if cmd.Flags().Changed(name) {
			given = append(given, "--"+name)
		}
	}
	if len(given) == 0 {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"warning: %s ignored -- this stack declares a [providers] table in .ubx/config, which is the authority for a multi-provider stack\n",
		strings.Join(given, ", "))
}

// applyGithubRepoDefault fills githubRepo from cfg if --github-repo
// wasn't explicitly given.
func applyGithubRepoDefault(cmd *cobra.Command, githubRepo *string, cfg *Config) {
	if !cmd.Flags().Changed("github-repo") && cfg.GithubRepo != "" {
		*githubRepo = cfg.GithubRepo
	}
}

// applyTFDirDefault fills tfDir from cfg if --tf-dir wasn't explicitly
// given.
func applyTFDirDefault(cmd *cobra.Command, tfDir *string, cfg *Config) {
	if !cmd.Flags().Changed("tf-dir") && cfg.TFDir != "" {
		*tfDir = cfg.TFDir
	}
}
