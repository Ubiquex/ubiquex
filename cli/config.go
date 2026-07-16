package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

// configFileName is the file every ubx command looks for, relative to
// whatever directory Discovery lands on (docs/architecture.md — Config
// defaults, UBI-19): "nearest .ubx/config wins", the same discovery
// convention .git itself uses.
const configFileName = ".ubx/config"

// Config is .ubx/config's parsed shape -- exactly the five keys UBI-19
// scoped in (docs/architecture.md): provider identity, provider
// configuration, default stack, GitHub repository, and .tf directory.
// --ledger-dir is deliberately not one of them.
type Config struct {
	Provider struct {
		Path    string `toml:"path"`
		Source  string `toml:"source"`
		Version string `toml:"version"`
	} `toml:"provider"`
	ProviderConfig map[string]any `toml:"provider_config"`
	Stack          string         `toml:"stack"`
	GithubRepo     string         `toml:"github_repo"`
	TFDir          string         `toml:"tf_dir"`
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
