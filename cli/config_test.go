package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

// TestLoadConfig_IntentShowDefaults_ParsesAndNeverWarns is UBI-72's own
// regression guard: `intent.show_defaults` is a known key (configcascade.go's
// own knownIntentKeys), not a surprise -- caught live this session, where
// the key parsed and took effect correctly but still spuriously warned
// "unknown config key" until knownIntentKeys was updated alongside it.
func TestLoadConfig_IntentShowDefaults_ParsesAndNeverWarns(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `intent = { show_defaults = false }`)
	withConfigSearchDir(t, dir)

	var warnings bytes.Buffer
	cfg, err := LoadConfig(&warnings)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if warnings.Len() != 0 {
		t.Fatalf("expected no unknown-key warning for intent.show_defaults, got: %s", warnings.String())
	}
	if cfg.Intent.ShowDefaults == nil || *cfg.Intent.ShowDefaults != false {
		t.Fatalf("expected ShowDefaults to decode to a pointer to false, got: %v", cfg.Intent.ShowDefaults)
	}
}

// TestLoadConfig_IntentShowDefaults_AbsentIsNil proves the nil-means-
// "cascade never mentioned it" case IntentConfig.ShowDefaults' own doc
// comment depends on -- absent from every config in the cascade decodes
// to a nil pointer, not a bare false, so resolveShowDefaults' own default
// of true is reachable at all.
func TestLoadConfig_IntentShowDefaults_AbsentIsNil(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `stack = "payments"`)
	withConfigSearchDir(t, dir)

	cfg, err := LoadConfig(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Intent.ShowDefaults != nil {
		t.Fatalf("expected a nil ShowDefaults when show_defaults is never mentioned, got: %v", *cfg.Intent.ShowDefaults)
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

[k8s_audit]
cluster = "my-eks-cluster"
region = "us-west-2"
log_group = "/aws/eks/my-eks-cluster/cluster"
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
	wantK8sAudit := K8sAuditConfig{Cluster: "my-eks-cluster", Region: "us-west-2", LogGroup: "/aws/eks/my-eks-cluster/cluster"}
	if cfg.K8sAudit != wantK8sAudit {
		t.Errorf("K8sAudit = %+v, want %+v", cfg.K8sAudit, wantK8sAudit)
	}
}

// TestLoadConfig_K8sAuditAbsentIsZeroValue confirms the UBI-22 default:
// no [k8s_audit] table at all (every existing config predates this
// session) decodes to a zero-value K8sAuditConfig -- Cluster == "" is the
// one signal newAttributionBackend checks to degrade to
// audit_unattributed/not_configured, never an error.
func TestLoadConfig_K8sAuditAbsentIsZeroValue(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `stack = "payments"`)
	withConfigSearchDir(t, dir)

	cfg, err := LoadConfig(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.K8sAudit != (K8sAuditConfig{}) {
		t.Errorf("K8sAudit = %+v, want zero value", cfg.K8sAudit)
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

// --- [thirdparty_providers]/[provider_configs] (2026-07-18, UBI-43 session 4) -------

func TestLoadConfig_MultiProviderTables(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
stack = "payments"

[thirdparty_providers]
"hashicorp/aws" = "6.60.0"
"hashicorp/helm" = "3.0.2"

[provider_configs."hashicorp/aws"]
region = "us-east-1"

[provider_configs."hashicorp/helm"]
kubeconfig = "~/.kube/config"
`)
	withConfigSearchDir(t, dir)

	cfg, err := LoadConfig(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	wantProviders := map[string]string{"hashicorp/aws": "6.60.0", "hashicorp/helm": "3.0.2"}
	if len(cfg.ThirdpartyProviders) != len(wantProviders) {
		t.Fatalf("Providers = %+v, want %+v", cfg.ThirdpartyProviders, wantProviders)
	}
	for source, version := range wantProviders {
		if cfg.ThirdpartyProviders[source] != version {
			t.Errorf("Providers[%q] = %q, want %q", source, cfg.ThirdpartyProviders[source], version)
		}
	}
	if cfg.ProviderConfigs["hashicorp/aws"]["region"] != "us-east-1" {
		t.Errorf("ProviderConfigs[hashicorp/aws][region] = %v, want us-east-1", cfg.ProviderConfigs["hashicorp/aws"]["region"])
	}
	if cfg.ProviderConfigs["hashicorp/helm"]["kubeconfig"] != "~/.kube/config" {
		t.Errorf("ProviderConfigs[hashicorp/helm][kubeconfig] = %v, want ~/.kube/config", cfg.ProviderConfigs["hashicorp/helm"]["kubeconfig"])
	}
}

// TestLoadConfig_ProvidersAbsentIsNilNotEmptyMap confirms a single-provider
// stack (no [thirdparty_providers] table at all -- every config predating this
// session) decodes to a genuinely nil (not just empty) map -- the one
// signal cli/ship.go and cli/resolve.go check (len(cfg.ThirdpartyProviders) > 0) to
// fall back to today's --provider/--source flow unchanged.
func TestLoadConfig_ProvidersAbsentIsNilNotEmptyMap(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `stack = "payments"`)
	withConfigSearchDir(t, dir)

	cfg, err := LoadConfig(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.ThirdpartyProviders) != 0 {
		t.Fatalf("Providers = %+v, want empty", cfg.ThirdpartyProviders)
	}
}

func TestWarnIfLegacyProviderFlagsGiven_NoneGiven_NoWarning(t *testing.T) {
	cmd := &cobra.Command{}
	var providerPath, source, providerVersion, providerConfig string
	cmd.Flags().StringVar(&providerPath, "provider", "", "")
	cmd.Flags().StringVar(&source, "source", "", "")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "", "")
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	warnIfLegacyProviderFlagsGiven(cmd)
	if stderr.Len() != 0 {
		t.Fatalf("expected no warning when no legacy flag was given, got: %s", stderr.String())
	}
}

func TestWarnIfLegacyProviderFlagsGiven_SourceGiven_WarnsNamingIt(t *testing.T) {
	cmd := &cobra.Command{}
	var providerPath, source, providerVersion, providerConfig string
	cmd.Flags().StringVar(&providerPath, "provider", "", "")
	cmd.Flags().StringVar(&source, "source", "", "")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "", "")
	if err := cmd.Flags().Set("source", "hashicorp/aws"); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	warnIfLegacyProviderFlagsGiven(cmd)
	if !bytes.Contains(stderr.Bytes(), []byte("--source")) {
		t.Fatalf("expected a warning naming --source, got: %s", stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("[thirdparty_providers]")) {
		t.Fatalf("expected the warning to explain the [thirdparty_providers] table is the authority, got: %s", stderr.String())
	}
}

// cmdWithBoolFlags builds a *cobra.Command carrying show-defaults/
// hide-defaults bool flags, set (Changed) only for names listed in set --
// resolveShowDefaults's own direct unit tests below don't need a full
// `ubx plan` invocation, just the flag-precedence matrix itself.
func cmdWithBoolFlags(t *testing.T, set ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	var show, hide bool
	cmd.Flags().BoolVar(&show, "show-defaults", false, "")
	cmd.Flags().BoolVar(&hide, "hide-defaults", false, "")
	for _, name := range set {
		if err := cmd.Flags().Set(name, "true"); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

func TestResolveShowDefaults_NeitherFlagNorConfig_DefaultsTrue(t *testing.T) {
	got, err := resolveShowDefaults(cmdWithBoolFlags(t), &Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatalf("got %v, want true (the nil-config default)", got)
	}
}

func TestResolveShowDefaults_ConfigFalse_NoFlags(t *testing.T) {
	f := false
	got, err := resolveShowDefaults(cmdWithBoolFlags(t), &Config{Intent: IntentConfig{ShowDefaults: &f}})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatalf("got %v, want false (config's own explicit value)", got)
	}
}

func TestResolveShowDefaults_ConfigTrue_NoFlags(t *testing.T) {
	tr := true
	got, err := resolveShowDefaults(cmdWithBoolFlags(t), &Config{Intent: IntentConfig{ShowDefaults: &tr}})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatalf("got %v, want true (config's own explicit value)", got)
	}
}

func TestResolveShowDefaults_ShowFlag_OverridesConfigFalse(t *testing.T) {
	f := false
	got, err := resolveShowDefaults(cmdWithBoolFlags(t, "show-defaults"), &Config{Intent: IntentConfig{ShowDefaults: &f}})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatalf("got %v, want true (--show-defaults overrides config)", got)
	}
}

func TestResolveShowDefaults_HideFlag_OverridesConfigTrue(t *testing.T) {
	tr := true
	got, err := resolveShowDefaults(cmdWithBoolFlags(t, "hide-defaults"), &Config{Intent: IntentConfig{ShowDefaults: &tr}})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatalf("got %v, want false (--hide-defaults overrides config)", got)
	}
}

func TestResolveShowDefaults_BothFlags_Errors(t *testing.T) {
	_, err := resolveShowDefaults(cmdWithBoolFlags(t, "show-defaults", "hide-defaults"), &Config{})
	if err == nil {
		t.Fatal("expected an error when both --show-defaults and --hide-defaults are given")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive error, got: %v", err)
	}
}
