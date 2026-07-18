package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// loadConfigFrom points configSearchStartDir at dir for the duration of
// the call and returns the resulting Config -- the shared way these
// tests decode whatever format `ubx init` actually wrote, without caring
// which one it was.
func loadConfigFrom(t *testing.T, dir string) *Config {
	t.Helper()
	withConfigSearchDir(t, dir)
	cfg, err := LoadConfig(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfig(%s): %v", dir, err)
	}
	return cfg
}

func TestInit_WritesCommentedTemplate_NoFlagsGiven(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir)
	if err != nil {
		t.Fatalf("ubx init: %v\noutput: %s", err, out)
	}

	// HCL is the canonical default format (UBI-32 Arc A).
	path := filepath.Join(dir, ".ubx", "config.hcl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(b)

	if !strings.Contains(content, "# stack =") {
		t.Fatalf("expected a commented stack example, got:\n%s", content)
	}
	if !strings.Contains(content, "source = \"hashicorp/aws\"") {
		t.Fatalf("expected commented provider examples, got:\n%s", content)
	}

	cfg := loadConfigFrom(t, dir)
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

	cfg := loadConfigFrom(t, dir)
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

	cfg := loadConfigFrom(t, dir)
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

	cfg := loadConfigFrom(t, dir)
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

// TestInit_RefusesToShadowADifferentFormatInTheSameDirectory is a real
// gotcha found live (UBI-32 Arc A): a bare `ubx init` in a directory that
// already has a working legacy `.ubx/config` would, without this check,
// silently write `.ubx/config.hcl` alongside it -- and configcascade.go's
// own discovery order (config.hcl -> config.toml -> config -> config.yaml)
// means only ONE of the two ever gets read again, entirely silently.
func TestInit_RefusesToShadowADifferentFormatInTheSameDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, ".ubx", "config")
	if err := os.WriteFile(legacy, []byte(`stack = "legacy-payments"`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runUbx(t, nil, "init", "--dir", dir) // default --format=hcl
	if err == nil {
		t.Fatal("expected an error -- writing config.hcl here would shadow the existing legacy config")
	}
	if !strings.Contains(err.Error(), "already exists in this directory") {
		t.Fatalf("expected a same-directory-conflict error, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ubx", "config.hcl")); !os.IsNotExist(err) {
		t.Fatalf("config.hcl must not have been written at all, got stat err: %v", err)
	}

	// --force proceeds anyway, and the legacy file's own value is now
	// correctly shadowed (proving discovery order, not this check, decides
	// the outcome once the caller has explicitly opted in).
	if _, err := runUbx(t, nil, "init", "--dir", dir, "--force"); err != nil {
		t.Fatalf("ubx init --force: %v", err)
	}
	cfg := loadConfigFrom(t, dir)
	if cfg.Stack != "" {
		t.Fatalf("Stack = %q, want empty -- config.hcl (no --stack given) must now win discovery over the legacy config", cfg.Stack)
	}
}

func TestInit_RejectsUnknownFormat(t *testing.T) {
	_, err := runUbx(t, nil, "init", "--dir", t.TempDir(), "--format", "ini")
	if err == nil {
		t.Fatal("expected an error for an unrecognized --format")
	}
	if !strings.Contains(err.Error(), "--format") {
		t.Fatalf("expected the error to name --format, got: %v", err)
	}
}

func TestInit_FormatTOML_WritesLegacyDecodableFile(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir, "--format", "toml", "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx init --format=toml: %v\noutput: %s", err, out)
	}
	path := filepath.Join(dir, ".ubx", "config.toml")
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatalf("generated file isn't valid TOML: %v", err)
	}
	if cfg.Stack != "payments" {
		t.Fatalf("Stack = %q, want payments", cfg.Stack)
	}

	if got := loadConfigFrom(t, dir); got.Stack != "payments" {
		t.Fatalf("LoadConfig via the cascade: Stack = %q, want payments", got.Stack)
	}
}

func TestInit_FormatYAML_WritesFullyQuotedFile(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir, "--format", "yaml",
		"--stack", "payments",
		"--source", "hashicorp/aws",
		"--provider-version", "6.60.0",
	)
	if err != nil {
		t.Fatalf("ubx init --format=yaml: %v\noutput: %s", err, out)
	}
	path := filepath.Join(dir, ".ubx", "config.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(b), `version: "6.60.0"`) {
		t.Fatalf("expected the provider version quoted (strict-mode safety), got:\n%s", b)
	}

	cfg := loadConfigFrom(t, dir)
	if cfg.Stack != "payments" || cfg.Provider.Version != "6.60.0" {
		t.Fatalf("got Stack=%q Provider.Version=%q, want payments/6.60.0", cfg.Stack, cfg.Provider.Version)
	}
}
