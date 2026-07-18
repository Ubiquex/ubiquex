package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

// configFileName is the file every ubx command looks for, relative to
// whatever directory Discovery lands on (docs/architecture.md — Config
// defaults, UBI-19): "nearest .ubx/config wins", the same discovery
// convention .git itself uses.
const configFileName = ".ubx/config"

// Config is .ubx/config's parsed shape -- the five keys UBI-19 scoped in
// (docs/architecture.md): provider identity, provider configuration,
// default stack, GitHub repository, and .tf directory -- plus UBI-22's
// [k8s_audit] table, and UBI-43's own [providers]/[provider_configs]
// tables (below). --ledger-dir is deliberately not one of them.
type Config struct {
	Provider struct {
		Path    string `toml:"path"`
		Source  string `toml:"source"`
		Version string `toml:"version"`
	} `toml:"provider"`
	ProviderConfig  map[string]any            `toml:"provider_config"`
	Providers       map[string]string         `toml:"providers"`
	ProviderConfigs map[string]map[string]any `toml:"provider_configs"`
	Stack           string                    `toml:"stack"`
	GithubRepo      string                    `toml:"github_repo"`
	TFDir           string                    `toml:"tf_dir"`
	K8sAudit        K8sAuditConfig            `toml:"k8s_audit"`
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
	Cluster string `toml:"cluster"`
	// Region is the AWS region the cluster (and its CloudWatch Logs log
	// group) lives in.
	Region string `toml:"region"`
	// LogGroup overrides the CloudWatch Logs log group to search, for a
	// cluster whose control-plane logging wasn't left at EKS's own
	// default naming convention. Empty means k8saudit.LogGroupForCluster(Cluster).
	LogGroup string `toml:"log_group"`
}

// LoadConfig discovers and parses .ubx/config, walking from the current
// working directory upward through parent directories and using the
// first one found. Returns a zero Config (not an error) if none exists
// anywhere up to the filesystem root -- config is optional everywhere;
// every value it could supply simply falls through to that flag's own
// existing default/required behavior instead. Unknown keys are reported
// as warnings to warnOut and otherwise ignored; a file that isn't valid
// TOML at all is a hard error.
func LoadConfig(warnOut io.Writer) (*Config, error) {
	path, err := findConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if path == "" {
		return &Config{}, nil
	}

	var cfg Config
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, key := range meta.Undecoded() {
		fmt.Fprintf(warnOut, "warning: %s: unknown config key %q (ignored)\n", path, key)
	}
	return &cfg, nil
}

// configSearchStartDir returns where findConfig starts walking upward
// from -- the real working directory in production. A package var (not a
// bare os.Getwd() call) purely so tests can point it at an isolated temp
// directory instead: without this seam, every test in this package would
// silently depend on whether some ambient .ubx/config happens to exist
// anywhere from the real process cwd up to the filesystem root (a
// developer's home directory, say) -- exactly the kind of host-machine-
// state leak `go test ./...` staying hermetic is supposed to rule out.
var configSearchStartDir = os.Getwd

// findConfig walks from configSearchStartDir() upward through parent
// directories looking for .ubx/config, returning "" (not an error) if it
// reaches the filesystem root without finding one.
func findConfig() (string, error) {
	dir, err := configSearchStartDir()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, configFileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

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
