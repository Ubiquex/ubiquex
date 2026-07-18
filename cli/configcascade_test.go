package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCascadeFile writes content to <dir>/.ubx/<name>, creating the
// .ubx/ directory (and dir itself) first.
func writeCascadeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".ubx", name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- docs/config-cascade-adversarial.md row 1 -----------------------------

func TestCascadeAdversarial_Row1_ConflictingScalarKey_ChildWins(t *testing.T) {
	root := t.TempDir()
	writeCascadeFile(t, root, "config", `stack = "root-stack"`)
	child := filepath.Join(root, "child")
	childFile := writeCascadeFile(t, child, "config", `stack = "child-stack"`)

	withConfigSearchDir(t, child)
	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.Stack != "child-stack" {
		t.Fatalf("Stack = %q, want child-stack (nearest wins outright)", rc.Config.Stack)
	}
	if rc.Provenance["stack"] != childFile {
		t.Fatalf("provenance[stack] = %q, want %q (never the root's)", rc.Provenance["stack"], childFile)
	}
}

// --- row 2 -----------------------------------------------------------------

func TestCascadeAdversarial_Row2_TableKeyMerge_SiblingKeyPreserved(t *testing.T) {
	root := t.TempDir()
	rootFile := writeCascadeFile(t, root, "config", `
[provider_configs."hashicorp/aws"]
region = "us-east-1"
retries = "3"
`)
	child := filepath.Join(root, "child")
	childFile := writeCascadeFile(t, child, "config", `
[provider_configs."hashicorp/aws"]
region = "us-west-2"
`)

	withConfigSearchDir(t, child)
	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	pc := rc.Config.ProviderConfigs["hashicorp/aws"]
	if pc["region"] != "us-west-2" {
		t.Errorf("region = %v, want us-west-2 (nearest wins for this key)", pc["region"])
	}
	if pc["retries"] != "3" {
		t.Errorf("retries = %v, want 3 (must survive from root -- the child never mentioned it)", pc["retries"])
	}
	const regionPath = `provider_configs."hashicorp/aws".region`
	const retriesPath = `provider_configs."hashicorp/aws".retries`
	if rc.Provenance[regionPath] != childFile {
		t.Errorf("provenance[%s] = %q, want %q", regionPath, rc.Provenance[regionPath], childFile)
	}
	if rc.Provenance[retriesPath] != rootFile {
		t.Errorf("provenance[%s] = %q, want %q", retriesPath, rc.Provenance[retriesPath], rootFile)
	}
}

// --- row 3 -----------------------------------------------------------------

func TestCascadeAdversarial_Row3_CrossFormatCascadeChain(t *testing.T) {
	root := t.TempDir()
	rootFile := writeCascadeFile(t, root, "config.toml", `github_repo = "acme/infra"`)
	mid := filepath.Join(root, "mid")
	midFile := writeCascadeFile(t, mid, "config.hcl", `tf_dir = "./terraform"`)
	leaf := filepath.Join(mid, "leaf")
	leafFile := writeCascadeFile(t, leaf, "config.yaml", `stack: "payments"`)

	withConfigSearchDir(t, leaf)
	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.GithubRepo != "acme/infra" || rc.Config.TFDir != "./terraform" || rc.Config.Stack != "payments" {
		t.Fatalf("got %+v, want all three keys present from their own format", rc.Config)
	}
	if rc.Provenance["github_repo"] != rootFile {
		t.Errorf("provenance[github_repo] = %q, want %q", rc.Provenance["github_repo"], rootFile)
	}
	if rc.Provenance["tf_dir"] != midFile {
		t.Errorf("provenance[tf_dir] = %q, want %q", rc.Provenance["tf_dir"], midFile)
	}
	if rc.Provenance["stack"] != leafFile {
		t.Errorf("provenance[stack] = %q, want %q", rc.Provenance["stack"], leafFile)
	}
}

// --- row 4 -----------------------------------------------------------------

func TestCascadeAdversarial_Row4_SameDirectoryMultipleFormats_HCLWins(t *testing.T) {
	dir := t.TempDir()
	hclFile := writeCascadeFile(t, dir, "config.hcl", `stack = "hcl-stack"`)
	writeCascadeFile(t, dir, "config.toml", `stack = "toml-stack"`)

	withConfigSearchDir(t, dir)
	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.Stack != "hcl-stack" {
		t.Fatalf("Stack = %q, want hcl-stack (config.hcl must win discovery over config.toml)", rc.Config.Stack)
	}
	if len(rc.Files) != 1 || rc.Files[0] != hclFile {
		t.Fatalf("Files = %v, want exactly [%s] -- the unused config.toml must never be read", rc.Files, hclFile)
	}
}

// --- row 5 -----------------------------------------------------------------

func TestCascadeAdversarial_Row5_YAMLNumericCoercionRefused(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config.yaml", "provider:\n  version: 6.60\n")
	withConfigSearchDir(t, dir)

	_, err := LoadConfigResolved(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error for an unquoted 6.60 (would silently narrow to float 6.6)")
	}
	if !strings.Contains(err.Error(), "6.60") {
		t.Fatalf("expected the error to name the offending token, got: %v", err)
	}
}

func TestCascadeAdversarial_Row5_YAMLQuotedNumericAllowed(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config.yaml", "provider:\n  version: \"6.60\"\n")
	withConfigSearchDir(t, dir)

	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.Provider.Version != "6.60" {
		t.Fatalf("Provider.Version = %q, want 6.60 (quoting is the escape hatch)", rc.Config.Provider.Version)
	}
}

// --- row 6 -----------------------------------------------------------------

func TestCascadeAdversarial_Row6_YAMLAmbiguousBooleanTokensStayStrings(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config.yaml", "github_repo: no\n")
	withConfigSearchDir(t, dir)

	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v (gopkg.in/yaml.v3 must resolve a bare 'no' as !!str, not !!bool)", err)
	}
	if rc.Config.GithubRepo != "no" {
		t.Fatalf("GithubRepo = %q, want the literal string \"no\"", rc.Config.GithubRepo)
	}
}

// --- row 7 -----------------------------------------------------------------

func TestCascadeAdversarial_Row7_HCLInterpolationRejected_WholeFileFails(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config.hcl", "stack = \"${path.module}\"\ngithub_repo = \"acme/infra\"\n")
	withConfigSearchDir(t, dir)

	_, err := LoadConfigResolved(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected a hard error for a non-literal (interpolated) attribute")
	}
	if !strings.Contains(err.Error(), "literal") {
		t.Fatalf("expected the error to explain literal-only, got: %v", err)
	}
}

func TestCascadeAdversarial_Row7_HCLFunctionCallRejected(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config.hcl", `tf_dir = upper("./terraform")`)
	withConfigSearchDir(t, dir)

	_, err := LoadConfigResolved(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected a hard error for a function call")
	}
}

func TestCascadeAdversarial_Row7_ViolationNeverPartiallyMerges(t *testing.T) {
	// A parent supplies a genuinely good key; the child's own file is
	// entirely invalid HCL (one bad attribute). The whole child file
	// must contribute nothing -- not even its own otherwise-fine keys --
	// matching the existing malformed-TOML precedent.
	root := t.TempDir()
	writeCascadeFile(t, root, "config", `github_repo = "acme/infra"`)
	child := filepath.Join(root, "child")
	writeCascadeFile(t, child, "config.hcl", "stack = \"${path.module}\"\ntf_dir = \"./terraform\"\n")

	withConfigSearchDir(t, child)
	_, err := LoadConfigResolved(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected the whole cascade load to fail -- a partial salvage of tf_dir would be silently wrong")
	}
}

// --- row 8 -----------------------------------------------------------------

func TestCascadeAdversarial_Row8_PerDirectoryStackDefault_RestInherited(t *testing.T) {
	root := t.TempDir()
	writeCascadeFile(t, root, "config", `
tf_dir = "./terraform"

[provider]
source = "hashicorp/aws"
version = "6.60.0"
`)
	stackDir := filepath.Join(root, "payments")
	writeCascadeFile(t, stackDir, "config", `stack = "payments"`)

	withConfigSearchDir(t, stackDir)
	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.Stack != "payments" {
		t.Errorf("Stack = %q, want payments", rc.Config.Stack)
	}
	if rc.Config.TFDir != "./terraform" {
		t.Errorf("TFDir = %q, want ./terraform (inherited from root)", rc.Config.TFDir)
	}
	if rc.Config.Provider.Source != "hashicorp/aws" || rc.Config.Provider.Version != "6.60.0" {
		t.Errorf("Provider = %+v, want the root's own source/version", rc.Config.Provider)
	}
}

// --- row 9 -----------------------------------------------------------------

func TestCascadeAdversarial_Row9_ProvenanceIsFormatBlind(t *testing.T) {
	root := t.TempDir()
	rootFile := writeCascadeFile(t, root, "config.toml", `github_repo = "acme/infra"`)
	mid := filepath.Join(root, "mid")
	midFile := writeCascadeFile(t, mid, "config.hcl", `tf_dir = "./terraform"`)
	leaf := filepath.Join(mid, "leaf")
	leafFile := writeCascadeFile(t, leaf, "config.yaml", `stack: "payments"`)

	withConfigSearchDir(t, leaf)
	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	report := renderProvenance(rc)
	for _, want := range []string{rootFile, midFile, leafFile} {
		if !strings.Contains(report, want) {
			t.Errorf("provenance report missing %q:\n%s", want, report)
		}
	}
	if !strings.HasSuffix(rootFile, ".toml") || !strings.HasSuffix(midFile, ".hcl") || !strings.HasSuffix(leafFile, ".yaml") {
		t.Fatalf("test setup itself is wrong: expected three distinct extensions")
	}
}

// --- row 10 ------------------------------------------------------------------

func TestCascadeAdversarial_Row10_UnknownKeyWarning_AllThreeFormats(t *testing.T) {
	cases := []struct {
		name, file, content string
	}{
		{"toml", "config.toml", "stcak = \"payments\"\nstack = \"payments\"\n"},
		{"hcl", "config.hcl", "stcak = \"payments\"\nstack = \"payments\"\n"},
		{"yaml", "config.yaml", "stcak: \"payments\"\nstack: \"payments\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := writeCascadeFile(t, dir, tc.file, tc.content)
			withConfigSearchDir(t, dir)

			var warnings bytes.Buffer
			rc, err := LoadConfigResolved(&warnings)
			if err != nil {
				t.Fatalf("LoadConfigResolved: %v", err)
			}
			if rc.Config.Stack != "payments" {
				t.Errorf("Stack = %q, want payments (the correctly-spelled sibling key must still load)", rc.Config.Stack)
			}
			if !strings.Contains(warnings.String(), "stcak") || !strings.Contains(warnings.String(), file) {
				t.Errorf("expected a warning naming %q and %q, got: %s", "stcak", file, warnings.String())
			}
		})
	}
}

// --- row 11 ------------------------------------------------------------------

func TestCascadeAdversarial_Row11_FreeformTablesNeverWarn(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config", `
[provider_config]
totally_custom_flag = true

[provider_configs."some/vendor"]
another_custom_key = "value"
`)
	withConfigSearchDir(t, dir)

	var warnings bytes.Buffer
	_, err := LoadConfigResolved(&warnings)
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if warnings.Len() != 0 {
		t.Fatalf("expected no warnings for freeform provider_config/provider_configs keys, got: %s", warnings.String())
	}
}

// --- unknown top-level/provider/k8s_audit keys still warn -------------------

func TestCascadeAdversarial_UnknownProviderSubKeyWarns(t *testing.T) {
	dir := t.TempDir()
	file := writeCascadeFile(t, dir, "config", `
[provider]
source = "hashicorp/aws"
version = "6.60.0"
bogus_field = "oops"
`)
	withConfigSearchDir(t, dir)

	var warnings bytes.Buffer
	if _, err := LoadConfigResolved(&warnings); err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if !strings.Contains(warnings.String(), "provider.bogus_field") || !strings.Contains(warnings.String(), file) {
		t.Fatalf("expected a warning naming provider.bogus_field and %q, got: %s", file, warnings.String())
	}
}

// --- [ledger] table (UBI-32 Arc B) ------------------------------------------

func TestCascade_LedgerStoreParsed(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config", `
[ledger]
store = "s3://acme-ledger/acme/prod/"
`)
	withConfigSearchDir(t, dir)

	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.Ledger.Store != "s3://acme-ledger/acme/prod/" {
		t.Fatalf("Ledger.Store = %q, want the configured s3 URI", rc.Config.Ledger.Store)
	}
}

func TestCascade_LedgerAbsentIsZeroValue(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config", `stack = "payments"`)
	withConfigSearchDir(t, dir)

	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.Ledger.Store != "" {
		t.Fatalf("Ledger.Store = %q, want empty (no [ledger] table at all -- every config predating this session)", rc.Config.Ledger.Store)
	}
}

func TestCascade_UnknownLedgerSubKeyWarns(t *testing.T) {
	dir := t.TempDir()
	file := writeCascadeFile(t, dir, "config", `
[ledger]
store = "git"
bogus_field = "oops"
`)
	withConfigSearchDir(t, dir)

	var warnings bytes.Buffer
	if _, err := LoadConfigResolved(&warnings); err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if !strings.Contains(warnings.String(), "ledger.bogus_field") || !strings.Contains(warnings.String(), file) {
		t.Fatalf("expected a warning naming ledger.bogus_field and %q, got: %s", file, warnings.String())
	}
}

// --- HCL blocks are rejected outright (confighcl.go's own design) ----------

func TestConfigHCL_BlocksRejected(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config.hcl", "provider {\n  source = \"hashicorp/aws\"\n}\n")
	withConfigSearchDir(t, dir)

	_, err := LoadConfigResolved(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error -- config supports attribute-object syntax only, not HCL blocks")
	}
	if !strings.Contains(err.Error(), "block") {
		t.Fatalf("expected the error to mention blocks, got: %v", err)
	}
}

// --- YAML non-mapping top level ---------------------------------------------

func TestConfigYAML_NonMappingTopLevelRejected(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config.yaml", "- a\n- b\n")
	withConfigSearchDir(t, dir)

	_, err := LoadConfigResolved(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error for a non-mapping top-level YAML document")
	}
}

// --- joinProvenancePath quoting ----------------------------------------------

func TestJoinProvenancePath(t *testing.T) {
	cases := []struct{ base, key, want string }{
		{"", "stack", "stack"},
		{"provider", "source", "provider.source"},
		{"", "hashicorp/aws", `"hashicorp/aws"`},
		{"provider_configs", "hashicorp/aws", `provider_configs."hashicorp/aws"`},
	}
	for _, tc := range cases {
		if got := joinProvenancePath(tc.base, tc.key); got != tc.want {
			t.Errorf("joinProvenancePath(%q, %q) = %q, want %q", tc.base, tc.key, got, tc.want)
		}
	}
}
