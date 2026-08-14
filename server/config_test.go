package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
)

func newTestFlags() *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("listen-addr", "", "")
	fs.String("work-dir", "", "")
	fs.Int64("github-app-id", 0, "")
	fs.String("github-app-private-key-path", "", "")
	fs.String("github-bot-login", "", "")
	fs.String("github-webhook-secret", "", "")
	fs.String("github-api-base-url", "", "")
	fs.String("gitlab-token", "", "")
	fs.String("gitlab-bot-username", "", "")
	fs.String("gitlab-webhook-secret", "", "")
	fs.String("gitlab-api-base-url", "", "")
	fs.String("azure-devops-organization", "", "")
	fs.String("azure-devops-token", "", "")
	fs.String("azure-devops-bot-display-name", "", "")
	fs.String("azure-devops-webhook-secret-header", "", "")
	fs.String("azure-devops-webhook-secret", "", "")
	fs.String("azure-devops-api-base-url", "", "")
	fs.String("provider-source", "", "")
	fs.String("provider-version", "", "")
	fs.String("provider-config", "", "")
	fs.Bool("ship-on-merge", true, "")
	fs.Bool("allow-destroy", false, "")
	fs.String("surface-as", "", "")
	fs.String("drift-watch-interval", "", "")
	fs.StringArray("repo", nil, "")
	return fs
}

func TestLoad_DefaultsOnly(t *testing.T) {
	cfg, err := Load("", nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.AllowDestroy != false {
		t.Error("AllowDestroy default must be false -- destroy is disabled by default")
	}
	if cfg.ShipOnMerge != true {
		t.Error("ShipOnMerge default must be true")
	}
	if cfg.SurfaceAs != "issue" {
		t.Errorf("SurfaceAs = %q, want issue", cfg.SurfaceAs)
	}
	if cfg.DriftWatchInterval.String() != "24h0m0s" {
		t.Errorf("DriftWatchInterval = %v, want 24h", cfg.DriftWatchInterval)
	}
}

func TestLoad_YAMLOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ubx-server.yaml")
	yamlContent := `
listen_addr: ":9090"
github_app_id: 123456
allow_destroy: true
repos:
  - owner: acme
    name: infra
    ledger_dir: "."
`
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want :9090 (from YAML)", cfg.ListenAddr)
	}
	if cfg.GitHubAppID != 123456 {
		t.Errorf("GitHubAppID = %d, want 123456", cfg.GitHubAppID)
	}
	if !cfg.AllowDestroy {
		t.Error("AllowDestroy should be true from YAML")
	}
	// Untouched-by-YAML fields still carry their built-in defaults.
	if cfg.SurfaceAs != "issue" {
		t.Errorf("SurfaceAs = %q, want issue (default, YAML didn't set it)", cfg.SurfaceAs)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Owner != "acme" || cfg.Repos[0].Name != "infra" {
		t.Errorf("Repos = %+v, want one acme/infra entry", cfg.Repos)
	}
}

// TestLoad_EnvOverridesYAML is the real, load-bearing precedence check:
// both a YAML file and an env var set the same key, and the env var must
// win.
func TestLoad_EnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ubx-server.yaml")
	if err := os.WriteFile(path, []byte("listen_addr: \":9090\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UBX_SERVER_LISTEN_ADDR", ":7070")

	cfg, err := Load(path, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":7070" {
		t.Errorf("ListenAddr = %q, want :7070 (env must win over YAML)", cfg.ListenAddr)
	}
}

// TestLoad_FlagsOverrideEnv is the top of the real cascade: a flag beats
// everything below it.
func TestLoad_FlagsOverrideEnv(t *testing.T) {
	t.Setenv("UBX_SERVER_LISTEN_ADDR", ":7070")

	flags := newTestFlags()
	if err := flags.Parse([]string{"--listen-addr", ":6060"}); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", flags)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":6060" {
		t.Errorf("ListenAddr = %q, want :6060 (flag must win over env)", cfg.ListenAddr)
	}
}

// TestLoad_UnsetFlagsDoNotBlankLowerLayers guards against the real,
// easy-to-introduce bug this cascade's own doc comment calls out: a
// flag's own zero value (bool false, empty string) must never overwrite
// a value the env/YAML layers already set, when that flag was never
// actually passed.
func TestLoad_UnsetFlagsDoNotBlankLowerLayers(t *testing.T) {
	t.Setenv("UBX_SERVER_ALLOW_DESTROY", "true")

	flags := newTestFlags()
	if err := flags.Parse(nil); err != nil { // no flags passed at all
		t.Fatal(err)
	}

	cfg, err := Load("", flags)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.AllowDestroy {
		t.Error("AllowDestroy should still be true from env -- an unset --allow-destroy flag must not blank it to false")
	}
}

func TestLoad_RepoFlagShorthand(t *testing.T) {
	flags := newTestFlags()
	if err := flags.Parse([]string{"--repo", "acme/infra", "--repo", "acme/payments:stacks/payments"}); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", flags)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Repos) != 2 {
		t.Fatalf("Repos = %+v, want 2 entries", cfg.Repos)
	}
	if cfg.Repos[0] != (RepoConfig{Platform: "github", Owner: "acme", Name: "infra", LedgerDir: "."}) {
		t.Errorf("Repos[0] = %+v, want acme/infra with default ledger_dir \".\"", cfg.Repos[0])
	}
	if cfg.Repos[1] != (RepoConfig{Platform: "github", Owner: "acme", Name: "payments", LedgerDir: "stacks/payments"}) {
		t.Errorf("Repos[1] = %+v, want acme/payments:stacks/payments parsed correctly", cfg.Repos[1])
	}
}

// TestLoad_RepoFlagGitLabShorthand covers the real "gitlab:" prefix,
// including a real, nested GitLab project path (a real subgroup) --
// never assumed to split into exactly two segments the way GitHub's
// own owner/name always does.
func TestLoad_RepoFlagGitLabShorthand(t *testing.T) {
	flags := newTestFlags()
	if err := flags.Parse([]string{"--repo", "gitlab:acme/infra", "--repo", "gitlab:acme/backend/infra:stacks/payments"}); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", flags)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Repos) != 2 {
		t.Fatalf("Repos = %+v, want 2 entries", cfg.Repos)
	}
	if cfg.Repos[0] != (RepoConfig{Platform: "gitlab", Project: "acme/infra", LedgerDir: "."}) {
		t.Errorf("Repos[0] = %+v, want gitlab:acme/infra with default ledger_dir \".\"", cfg.Repos[0])
	}
	if cfg.Repos[1] != (RepoConfig{Platform: "gitlab", Project: "acme/backend/infra", LedgerDir: "stacks/payments"}) {
		t.Errorf("Repos[1] = %+v, want the real, nested subgroup project path parsed as one whole string", cfg.Repos[1])
	}
}

func TestLoad_RepoFlagAzureDevOpsShorthand(t *testing.T) {
	flags := newTestFlags()
	if err := flags.Parse([]string{"--repo", "azuredevops:acme-infra/infra", "--repo", "azuredevops:acme-infra/payments-infra:stacks/payments"}); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", flags)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Repos) != 2 {
		t.Fatalf("Repos = %+v, want 2 entries", cfg.Repos)
	}
	if cfg.Repos[0] != (RepoConfig{Platform: "azuredevops", Project: "acme-infra", Repository: "infra", LedgerDir: "."}) {
		t.Errorf("Repos[0] = %+v, want azuredevops:acme-infra/infra with default ledger_dir \".\"", cfg.Repos[0])
	}
	if cfg.Repos[1] != (RepoConfig{Platform: "azuredevops", Project: "acme-infra", Repository: "payments-infra", LedgerDir: "stacks/payments"}) {
		t.Errorf("Repos[1] = %+v, want project and repository parsed as two real, separate identifiers", cfg.Repos[1])
	}
}

func TestLoad_RepoFlagMalformedRejected(t *testing.T) {
	flags := newTestFlags()
	if err := flags.Parse([]string{"--repo", "not-a-valid-repo"}); err != nil {
		t.Fatal(err)
	}

	if _, err := Load("", flags); err == nil {
		t.Fatal("expected an error for a malformed --repo value, got nil")
	}
}

func TestLoad_MissingYAMLFileIsNotAnError(t *testing.T) {
	// A configured but not-yet-created config path is a real, common
	// case (e.g. a fresh Docker deployment before the operator has
	// mounted one) -- it must fall through to defaults/env/flags, not
	// fail outright.
	cfg, err := Load("/nonexistent/ubx-server.yaml", nil)
	if err != nil {
		t.Fatalf("Load with a missing (not malformed) config file should not error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want the built-in default", cfg.ListenAddr)
	}
}

func TestLoad_MalformedYAMLIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ubx-server.yaml")
	if err := os.WriteFile(path, []byte("not: valid: yaml: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path, nil); err == nil {
		t.Fatal("expected an error for malformed YAML, got nil")
	}
}
