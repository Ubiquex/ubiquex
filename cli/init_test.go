package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestInit_WritesCommentedTemplate_NoFlagsGiven(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir)
	if err != nil {
		t.Fatalf("ubx init: %v\noutput: %s", err, out)
	}

	path := filepath.Join(dir, ".ubx", "config")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(b)

	if !strings.Contains(content, "# stack =") {
		t.Fatalf("expected a commented stack example, got:\n%s", content)
	}
	if !strings.Contains(content, "# source =") || !strings.Contains(content, "# version =") {
		t.Fatalf("expected commented provider examples, got:\n%s", content)
	}

	// Must still be valid, decodable TOML even though every value is
	// commented out.
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatalf("generated template isn't valid TOML: %v", err)
	}
	if cfg.Stack != "" {
		t.Fatalf("Stack = %q, want empty (no flags were given)", cfg.Stack)
	}
}

func TestInit_WritesRealValues_ForGivenFlags(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir,
		"--stack", "payments",
		"--source", "hashicorp/aws",
		"--provider-version", "6.54.0",
		"--provider-config", `{"region":"us-east-1"}`,
		"--github-repo", "acme/infra",
		"--tf-dir", "./terraform",
	)
	if err != nil {
		t.Fatalf("ubx init: %v\noutput: %s", err, out)
	}

	path := filepath.Join(dir, ".ubx", "config")
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatalf("decode generated config: %v", err)
	}
	if cfg.Stack != "payments" {
		t.Errorf("Stack = %q, want payments", cfg.Stack)
	}
	if cfg.Provider.Source != "hashicorp/aws" || cfg.Provider.Version != "6.54.0" {
		t.Errorf("Provider = %+v, want source=hashicorp/aws version=6.54.0", cfg.Provider)
	}
	if cfg.ProviderConfig["region"] != "us-east-1" {
		t.Errorf("ProviderConfig[region] = %v, want us-east-1", cfg.ProviderConfig["region"])
	}
	if cfg.GithubRepo != "acme/infra" {
		t.Errorf("GithubRepo = %q, want acme/infra", cfg.GithubRepo)
	}
	if cfg.TFDir != "./terraform" {
		t.Errorf("TFDir = %q, want ./terraform", cfg.TFDir)
	}
}

func TestInit_RefusesToOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if _, err := runUbx(t, nil, "init", "--dir", dir, "--stack", "first"); err != nil {
		t.Fatalf("ubx init (1st): %v", err)
	}
	_, err := runUbx(t, nil, "init", "--dir", dir, "--stack", "second")
	if err == nil {
		t.Fatal("expected an error when overwriting an existing config without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected an already-exists error, got: %v", err)
	}

	var cfg Config
	if _, err := toml.DecodeFile(filepath.Join(dir, ".ubx", "config"), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Stack != "first" {
		t.Fatalf("Stack = %q, want the original (untouched) value \"first\"", cfg.Stack)
	}
}

func TestInit_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if _, err := runUbx(t, nil, "init", "--dir", dir, "--stack", "first"); err != nil {
		t.Fatalf("ubx init (1st): %v", err)
	}
	if _, err := runUbx(t, nil, "init", "--dir", dir, "--stack", "second", "--force"); err != nil {
		t.Fatalf("ubx init --force: %v", err)
	}

	var cfg Config
	if _, err := toml.DecodeFile(filepath.Join(dir, ".ubx", "config"), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Stack != "second" {
		t.Fatalf("Stack = %q, want the overwritten value \"second\"", cfg.Stack)
	}
}

func TestInit_RejectsProviderAndSourceTogether(t *testing.T) {
	_, err := runUbx(t, nil, "init", "--dir", t.TempDir(), "--provider", "/path/to/provider", "--source", "hashicorp/aws")
	if err == nil {
		t.Fatal("expected an error when --provider and --source are both given")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutual-exclusivity error, got: %v", err)
	}
}

func TestInit_RequiresProviderVersionWithSource(t *testing.T) {
	_, err := runUbx(t, nil, "init", "--dir", t.TempDir(), "--source", "hashicorp/aws")
	if err == nil {
		t.Fatal("expected an error when --source is given without --provider-version")
	}
	if !strings.Contains(err.Error(), "requires --provider-version") {
		t.Fatalf("expected a requires-provider-version error, got: %v", err)
	}
}
