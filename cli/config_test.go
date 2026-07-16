package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// withConfigSearchDir points configSearchStartDir at dir for the duration
// of the calling test, restoring the previous (hermetic, TestMain-set)
// value afterward.
func withConfigSearchDir(t *testing.T, dir string) {
	t.Helper()
	orig := configSearchStartDir
	configSearchStartDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { configSearchStartDir = orig })
}

// writeConfig writes content to <dir>/.ubx/config, creating the .ubx/
// directory first (writeFile itself doesn't).
func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".ubx", "config"), content)
}

func TestLoadConfig_NoConfigAnywhere(t *testing.T) {
	withConfigSearchDir(t, t.TempDir())
	cfg, err := LoadConfig(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Stack != "" || cfg.GithubRepo != "" || cfg.TFDir != "" || len(cfg.ProviderConfig) != 0 ||
		cfg.Provider.Path != "" || cfg.Provider.Source != "" || cfg.Provider.Version != "" {
		t.Fatalf("got %+v, want a zero Config", cfg)
	}
}

func TestLoadConfig_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "this is not [ valid toml")
	withConfigSearchDir(t, dir)

	_, err := LoadConfig(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error for malformed TOML")
	}
}

func TestLoadConfig_UnknownKeysWarnDontFail(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
stack = "payments"
totally_unknown_key = "surprise"
`)
	withConfigSearchDir(t, dir)

	var warnings bytes.Buffer
	cfg, err := LoadConfig(&warnings)
	if err != nil {
		t.Fatalf("LoadConfig: unexpected error for an unknown key: %v", err)
	}
	if cfg.Stack != "payments" {
		t.Fatalf("Stack = %q, want %q (known keys must still parse)", cfg.Stack, "payments")
	}
	if !bytes.Contains(warnings.Bytes(), []byte("totally_unknown_key")) {
		t.Fatalf("expected a warning naming the unknown key, got: %s", warnings.String())
	}
}

func TestLoadConfig_FullyParsedFields(t *testing.T) {
	dir := t.TempDir()
	// Root-level keys (stack/github_repo/tf_dir) come BEFORE any [table]
	// header -- see TestLoadConfig_RootKeysAfterTableGetSwallowed for why
	// that ordering is load-bearing, not cosmetic.
	writeConfig(t, dir, `
stack = "payments"
github_repo = "acme/infra"
tf_dir = "./terraform"

[provider]
source = "hashicorp/aws"
version = "6.54.0"

[provider_config]
region = "us-east-1"
`)
	withConfigSearchDir(t, dir)

	cfg, err := LoadConfig(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Provider.Source != "hashicorp/aws" || cfg.Provider.Version != "6.54.0" {
		t.Errorf("Provider = %+v, want source=hashicorp/aws version=6.54.0", cfg.Provider)
	}
	if cfg.ProviderConfig["region"] != "us-east-1" {
		t.Errorf("ProviderConfig[region] = %v, want us-east-1", cfg.ProviderConfig["region"])
	}
	if cfg.Stack != "payments" || cfg.GithubRepo != "acme/infra" || cfg.TFDir != "./terraform" {
		t.Errorf("got Stack=%q GithubRepo=%q TFDir=%q, want payments/acme/infra/./terraform", cfg.Stack, cfg.GithubRepo, cfg.TFDir)
	}
}

// TestLoadConfig_RootKeysAfterTableGetSwallowed documents a real TOML
// gotcha this session's own `ubx init` template generation got wrong on
// the first attempt (caught by writing a real config and decoding it
// back, not by inspecting the generated string by eye): a bare key
// written after a [table] header belongs to that table, not the document
// root, no matter how many blank lines separate them. This isn't a
// LoadConfig bug to fix -- it's exactly how TOML itself works -- but it's
// a real footgun worth a regression test given it already bit this
// session once.
func TestLoadConfig_RootKeysAfterTableGetSwallowed(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[provider_config]
region = "us-east-1"

stack = "payments"
`)
	withConfigSearchDir(t, dir)

	cfg, err := LoadConfig(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Stack != "" {
		t.Fatalf("Stack = %q, want empty -- it was written after [provider_config] and TOML assigns it there, not to the root", cfg.Stack)
	}
	if cfg.ProviderConfig["stack"] != "payments" {
		t.Fatalf("expected \"stack\" to land inside provider_config instead, got %v", cfg.ProviderConfig)
	}
}

func TestLoadConfig_NearestWins(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `stack = "root-stack"`)

	child := filepath.Join(root, "project")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, child, `stack = "child-stack"`)

	withConfigSearchDir(t, child)
	cfg, err := LoadConfig(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Stack != "child-stack" {
		t.Fatalf("Stack = %q, want %q (nearest .ubx/config must win)", cfg.Stack, "child-stack")
	}
}

func TestLoadConfig_FoundInParentWhenNoneNearer(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `stack = "root-stack"`)

	child := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	withConfigSearchDir(t, child)
	cfg, err := LoadConfig(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Stack != "root-stack" {
		t.Fatalf("Stack = %q, want %q (a parent directory's config must still be found)", cfg.Stack, "root-stack")
	}
}

// cmdWithFlag builds a minimal cobra.Command carrying just the named
// string flag, for testing the apply* helpers' cmd.Flags().Changed(...)
// logic in isolation without a full command.
func cmdWithFlag(t *testing.T, flagName, value string, changed bool) (*cobra.Command, *string) {
	t.Helper()
	var v string
	cmd := &cobra.Command{}
	cmd.Flags().StringVar(&v, flagName, "", "")
	if changed {
		if err := cmd.Flags().Set(flagName, value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd, &v
}

func TestApplyStackDefault_CLIWins(t *testing.T) {
	cmd, stack := cmdWithFlag(t, "stack", "cli-stack", true)
	applyStackDefault(cmd, stack, &Config{Stack: "config-stack"})
	if *stack != "cli-stack" {
		t.Fatalf("stack = %q, want the CLI-supplied value to win", *stack)
	}
}

func TestApplyStackDefault_ConfigFillsGap(t *testing.T) {
	cmd, stack := cmdWithFlag(t, "stack", "", false)
	applyStackDefault(cmd, stack, &Config{Stack: "config-stack"})
	if *stack != "config-stack" {
		t.Fatalf("stack = %q, want config to fill the gap", *stack)
	}
}

func TestApplyStackDefault_NeitherGiven(t *testing.T) {
	cmd, stack := cmdWithFlag(t, "stack", "", false)
	applyStackDefault(cmd, stack, &Config{})
	if *stack != "" {
		t.Fatalf("stack = %q, want empty when neither CLI nor config supplies one", *stack)
	}
}

func TestApplyProviderDefaults_ExplicitProviderFlagBlocksConfig(t *testing.T) {
	cmd := &cobra.Command{}
	var providerPath, source, providerVersion string
	cmd.Flags().StringVar(&providerPath, "provider", "", "")
	cmd.Flags().StringVar(&source, "source", "", "")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "")
	if err := cmd.Flags().Set("provider", "/explicit/path"); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	cfg.Provider.Source = "hashicorp/aws"
	cfg.Provider.Version = "6.54.0"
	applyProviderDefaults(cmd, &providerPath, &source, &providerVersion, cfg)

	if providerPath != "/explicit/path" || source != "" {
		t.Fatalf("got providerPath=%q source=%q, want the explicit --provider to win entirely (no config source leaking in)", providerPath, source)
	}
}

func TestApplyProviderDefaults_ConfigSourceAndVersion(t *testing.T) {
	cmd := &cobra.Command{}
	var providerPath, source, providerVersion string
	cmd.Flags().StringVar(&providerPath, "provider", "", "")
	cmd.Flags().StringVar(&source, "source", "", "")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "")

	cfg := &Config{}
	cfg.Provider.Source = "hashicorp/aws"
	cfg.Provider.Version = "6.54.0"
	applyProviderDefaults(cmd, &providerPath, &source, &providerVersion, cfg)

	if source != "hashicorp/aws" || providerVersion != "6.54.0" {
		t.Fatalf("got source=%q version=%q, want both filled from config", source, providerVersion)
	}
}

func TestApplyProviderConfigDefault_MarshalsToJSON(t *testing.T) {
	cmd, providerConfig := cmdWithFlag(t, "provider-config", "", false)
	cfg := &Config{ProviderConfig: map[string]any{"region": "us-east-1"}}
	if err := applyProviderConfigDefault(cmd, providerConfig, cfg); err != nil {
		t.Fatalf("applyProviderConfigDefault: %v", err)
	}
	if *providerConfig != `{"region":"us-east-1"}` {
		t.Fatalf("providerConfig = %s, want a JSON-marshaled form", *providerConfig)
	}
}

func TestApplyProviderConfigDefault_CLIWins(t *testing.T) {
	cmd, providerConfig := cmdWithFlag(t, "provider-config", `{"region":"cli-region"}`, true)
	cfg := &Config{ProviderConfig: map[string]any{"region": "config-region"}}
	if err := applyProviderConfigDefault(cmd, providerConfig, cfg); err != nil {
		t.Fatalf("applyProviderConfigDefault: %v", err)
	}
	if *providerConfig != `{"region":"cli-region"}` {
		t.Fatalf("providerConfig = %s, want the CLI-supplied value untouched", *providerConfig)
	}
}
