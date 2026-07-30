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

// TestInit_NoFlagsGiven_MinimalRunnableTemplate is UBI-59's own default-
// output test: even with nothing given, the stack still comes from the
// directory's own name (never empty, never left to a human to fill in
// by hand) -- only the provider is genuinely left unconfigured (no flag,
// no TTY here -- runUbx's own stdin is never a real terminal, see
// scan_test.go's own doc comment on that), shown as a short commented
// example, not the old exhaustive encyclopedia.
func TestInit_NoFlagsGiven_MinimalRunnableTemplate(t *testing.T) {
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

	wantStack := filepath.Base(dir)
	if !strings.Contains(content, "stack = \""+wantStack+"\"") {
		t.Fatalf("expected a real stack line naming %q, got:\n%s", wantStack, content)
	}
	if !strings.Contains(content, "# providers = {") {
		t.Fatalf("expected a short commented providers example, got:\n%s", content)
	}
	// "k8s_audit"/"ledger"/"intent" legitimately appear once, by name, in
	// the closing "full key reference" pointer sentence -- what must be
	// absent is an actual (even commented-out) key block for any of them.
	for _, blockMarker := range []string{"k8s_audit = {", "[k8s_audit]", "k8s_audit:\n", "ledger = {", "[ledger]", "ledger:\n", "intent = {", "[intent]", "intent:\n"} {
		if strings.Contains(content, blockMarker) {
			t.Fatalf("expected the minimal template to omit rare keys entirely (not even commented) -- found %q in:\n%s", blockMarker, content)
		}
	}
	if !strings.Contains(content, "next: add a provider") {
		t.Fatalf("expected the no-provider closing next: hint, got:\n%s", content)
	}

	cfg := loadConfigFrom(t, dir)
	if cfg.Stack != wantStack {
		t.Fatalf("Stack = %q, want %q (derived from --dir)", cfg.Stack, wantStack)
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("Providers = %v, want none (nothing was given)", cfg.Providers)
	}

	if !strings.Contains(out, "next: add a provider") {
		t.Fatalf("expected the same next: hint echoed to stdout, got: %s", out)
	}
}

func TestInit_WritesRealValues_ForGivenFlags(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir,
		"--stack", "payments",
		"--source", "hashicorp/aws",
		"--provider-version", "6.54.0",
		"--provider-config", `{"timeout":"30s"}`,
		"--region", "us-east-1",
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
	// UBI-59: a provider given by flag is written into the MODERN
	// [providers]/[provider_configs] map, never the legacy singular
	// [provider]/[provider_config] table -- a brand-new stack has no
	// reason to ever see the legacy shape.
	if cfg.Providers["hashicorp/aws"] != "6.54.0" {
		t.Errorf("Providers[hashicorp/aws] = %q, want 6.54.0 (got %v)", cfg.Providers["hashicorp/aws"], cfg.Providers)
	}
	if cfg.Provider.Source != "" || cfg.Provider.Version != "" {
		t.Errorf("legacy Provider.Source/Version = %+v, want both empty -- the legacy shape must never be written when --source was given", cfg.Provider)
	}
	pc := cfg.ProviderConfigs["hashicorp/aws"]
	if pc["timeout"] != "30s" {
		t.Errorf("ProviderConfigs[hashicorp/aws][timeout] = %v, want 30s", pc["timeout"])
	}
	if pc["region"] != "us-east-1" {
		t.Errorf("ProviderConfigs[hashicorp/aws][region] = %v, want us-east-1 (--region shorthand merged with --provider-config)", pc["region"])
	}
	if cfg.GithubRepo != "acme/infra" {
		t.Errorf("GithubRepo = %q, want acme/infra", cfg.GithubRepo)
	}
	if cfg.TFDir != "./terraform" {
		t.Errorf("TFDir = %q, want ./terraform", cfg.TFDir)
	}

	if !strings.Contains(out, "next: write an intent file") {
		t.Fatalf("expected the has-a-provider next: hint, got: %s", out)
	}
}

// TestInit_Region_AloneIsShorthandForProviderConfig proves --region works
// with no --provider-config at all (the common case -- most users will
// only ever set a region, never the full JSON object).
func TestInit_Region_AloneIsShorthandForProviderConfig(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir,
		"--source", "hashicorp/aws", "--provider-version", "6.54.0", "--region", "eu-west-1")
	if err != nil {
		t.Fatalf("ubx init: %v\noutput: %s", err, out)
	}
	cfg := loadConfigFrom(t, dir)
	if cfg.ProviderConfigs["hashicorp/aws"]["region"] != "eu-west-1" {
		t.Fatalf("ProviderConfigs[hashicorp/aws][region] = %v, want eu-west-1", cfg.ProviderConfigs["hashicorp/aws"])
	}
}

// TestInit_ProviderPath_StillWritesLegacySingularTable proves the local/
// dev escape hatch (--provider <path>, no registry source) still works
// exactly as before -- it's the one shape that genuinely can't live in
// the modern map (no path slot in [providers]), so it keeps using
// [provider] the same way it always did.
func TestInit_ProviderPath_StillWritesLegacySingularTable(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir, "--provider", "/path/to/terraform-provider-aws")
	if err != nil {
		t.Fatalf("ubx init: %v\noutput: %s", err, out)
	}
	cfg := loadConfigFrom(t, dir)
	if cfg.Provider.Path != "/path/to/terraform-provider-aws" {
		t.Fatalf("Provider.Path = %q, want /path/to/terraform-provider-aws", cfg.Provider.Path)
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("Providers = %v, want none (a local path was given, not a registry source)", cfg.Providers)
	}
	if !strings.Contains(out, "next: write an intent file") {
		t.Fatalf("expected the has-a-provider next: hint (a local path still counts as configured), got: %s", out)
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
	// the outcome once the caller has explicitly opted in). No --stack was
	// given, so config.hcl's own stack now derives from the directory name
	// (UBI-59) -- still proving config.hcl wins discovery, just against the
	// new default value rather than an empty one.
	if _, err := runUbx(t, nil, "init", "--dir", dir, "--force"); err != nil {
		t.Fatalf("ubx init --force: %v", err)
	}
	cfg := loadConfigFrom(t, dir)
	wantStack := filepath.Base(dir)
	if cfg.Stack != wantStack {
		t.Fatalf("Stack = %q, want %q -- config.hcl (no --stack given) must now win discovery over the legacy config", cfg.Stack, wantStack)
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
	if !strings.Contains(string(b), `"hashicorp/aws": "6.60.0"`) {
		t.Fatalf("expected the provider version quoted (strict-mode safety), got:\n%s", b)
	}

	cfg := loadConfigFrom(t, dir)
	if cfg.Stack != "payments" || cfg.Providers["hashicorp/aws"] != "6.60.0" {
		t.Fatalf("got Stack=%q Providers=%v, want payments/hashicorp/aws=6.60.0", cfg.Stack, cfg.Providers)
	}
}

// TestInit_Full_PreservesTheAnnotatedEncyclopedia proves --full still
// gives the exhaustive, every-key reference the default output no
// longer does -- UBI-59's own explicit "optional init --full keeps the
// annotated variant" requirement.
func TestInit_Full_PreservesTheAnnotatedEncyclopedia(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir, "--full", "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx init --full: %v\noutput: %s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".ubx", "config.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	for _, want := range []string{"k8s_audit", "ledger", "# provider = {", "# providers = {", "github_repo"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected --full to include %q, got:\n%s", want, content)
		}
	}
}

// TestInit_TTYPrompt_FillsInProvider proves the "short TTY prompt" (UBI-59)
// actually populates the modern map when a real terminal supplies
// source/version/region and no flag did.
func TestInit_TTYPrompt_FillsInProvider(t *testing.T) {
	dir := t.TempDir()
	stdin := "hashicorp/aws\n6.60.0\nus-east-1\n"
	out, err := runUbxTTY(t, stdin, nil, "init", "--dir", dir, "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx init (TTY prompt): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Provider registry source") {
		t.Fatalf("expected the prompt itself in output, got: %s", out)
	}
	cfg := loadConfigFrom(t, dir)
	if cfg.Providers["hashicorp/aws"] != "6.60.0" {
		t.Fatalf("Providers = %v, want hashicorp/aws=6.60.0 from the TTY prompt", cfg.Providers)
	}
	if cfg.ProviderConfigs["hashicorp/aws"]["region"] != "us-east-1" {
		t.Fatalf("ProviderConfigs = %v, want region=us-east-1 from the TTY prompt", cfg.ProviderConfigs)
	}
}

// TestInit_TTYPrompt_BlankSourceSkipsProvider proves pressing enter at
// the very first prompt leaves the provider unconfigured -- exactly the
// same outcome as a flag-less, non-interactive invocation, never an
// error and never a hang waiting for more input.
func TestInit_TTYPrompt_BlankSourceSkipsProvider(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbxTTY(t, "\n", nil, "init", "--dir", dir, "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx init (TTY prompt, blank): %v\noutput: %s", err, out)
	}
	cfg := loadConfigFrom(t, dir)
	if len(cfg.Providers) != 0 {
		t.Fatalf("Providers = %v, want none -- a blank source must skip provider setup entirely", cfg.Providers)
	}
}

// TestInit_OutputImmediatelySupportsPlan_ViaMirror is UBI-59's own
// headline hermetic acceptance test: `ubx init`'s own output, with zero
// hand-editing, is enough for `ubx plan` to resolve a real intent file --
// no --provider/--source/--stack flags on the plan invocation at all,
// exactly what "minimal RUNNABLE config" is supposed to mean. Uses the
// same UBX_PROVIDER_MIRROR hermetic seam (docs/architecture.md, UBI-8)
// stackcascade_test.go's own TestPlan_FromDiagram_StackFromConfig_NoFlag
// already relies on, rather than a real registry/network call.
func TestInit_OutputImmediatelySupportsPlan_ViaMirror(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()
	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")

	env := []string{"FAKEPROVIDER_MODE=ok-v6", "UBX_PROVIDER_MIRROR=" + mirrorDir}

	initOut, err := runUbx(t, env, "init", "--dir", dir, "--stack", "payments", "--source", "fake/widget", "--provider-version", "0.1.0")
	if err != nil {
		t.Fatalf("ubx init: %v\noutput: %s", err, initOut)
	}

	withConfigSearchDir(t, dir)
	intentPath := filepath.Join(dir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via the config init just wrote"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})

	// No --provider/--source/--stack -- everything comes from the config
	// `ubx init` just wrote, read via the ordinary cascade.
	planOut, err := runUbx(t, env, "plan", intentPath, "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx plan (zero edits to init's own output): %v\noutput: %s", err, planOut)
	}
	if !strings.Contains(planOut, "blast radius: +1 ~0 -0") {
		t.Fatalf("expected a resolved create, got: %s", planOut)
	}
}
