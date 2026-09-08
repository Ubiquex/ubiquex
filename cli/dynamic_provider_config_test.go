package cli

import (
	"strings"
	"testing"
)

// Provider-level configuration and the dynamic path.
//
// `ubx init --region us-east-1` wrote provider_configs.<name>.region for
// a --dynamic-source stack, and that config could plan and could not
// ship. ubx-provider-dynamic declares an empty provider schema block in
// every one of its servers and its ConfigureProvider is a no-op, so the
// encoder rejected the attribute at the first Configure. Configure has
// only two callers, RunScan and executor.Ship, and plan is neither, so
// the failure waited until the last command in the documented flow.
//
// No test caught it because fakeprovider declares region as an optional
// provider attribute. Every hermetic test ran against a fake that
// accepted precisely the one key the real dynamic provider cannot, so
// the single divergence between them was the field the flag wrote.
//
// Both refusals below are loud on purpose. A stack silently inheriting
// the snapshot's baked region is worse than being told it cannot choose
// one: the first is a wrong region nobody notices until something has
// been built somewhere unintended.

func TestInit_RegionWithDynamicSource_Refused(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir, "--stack", "payments",
		"--dynamic-source", "ubiquex/aws", "--provider-version", "3.0.0",
		"--region", "us-east-1")
	if err == nil {
		t.Fatalf("expected --region to be refused with --dynamic-source, got success: %s", out)
	}
	all := out + errText(err)
	// The message has to leave the reader somewhere to go, not just say no.
	if !strings.Contains(all, "dynamic_providers.aws.auth") {
		t.Fatalf("expected the refusal to name the snapshot's own auth block, got: %s", all)
	}
	if !strings.Contains(all, "--region") {
		t.Fatalf("expected the refusal to name the flag actually given, got: %s", all)
	}
}

// --provider-config is refused on the same terms, and this is not
// belt-and-braces: --region's own help text calls itself equivalent to
// --provider-config '{"region":"..."}', so refusing one while writing the
// other would leave the documented equivalent open.
func TestInit_ProviderConfigWithDynamicSource_Refused(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir, "--stack", "payments",
		"--dynamic-source", "ubiquex/aws", "--provider-version", "3.0.0",
		"--provider-config", `{"region":"us-east-1"}`)
	if err == nil {
		t.Fatalf("expected --provider-config to be refused with --dynamic-source, got success: %s", out)
	}
	all := out + errText(err)
	if !strings.Contains(all, "--provider-config") {
		t.Fatalf("expected the refusal to name --provider-config, got: %s", all)
	}
	if !strings.Contains(all, "dynamic_providers.aws.auth") {
		t.Fatalf("expected the refusal to name the snapshot's own auth block, got: %s", all)
	}
}

// The same flags stay legal for a Terraform registry provider, which does
// declare a real provider schema and does honor ConfigureProvider. The
// refusal is about the dynamic path, not about provider config.
func TestInit_RegionWithRegistrySource_StillAllowed(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbx(t, nil, "init", "--dir", dir, "--stack", "payments",
		"--source", "hashicorp/aws", "--provider-version", "6.60.0",
		"--region", "us-east-1")
	if err != nil {
		t.Fatalf("--region with a registry --source must still work: %v\noutput: %s", err, out)
	}
	cfg := loadConfigFrom(t, dir)
	pc, ok := cfg.ProviderConfigs["hashicorp/aws"]
	if !ok {
		t.Fatalf("expected [provider_configs] for the registry source, got: %v", cfg.ProviderConfigs)
	}
	if pc["region"] != "us-east-1" {
		t.Errorf("provider_configs region = %v, want us-east-1", pc["region"])
	}
}

// The interactive prompt suggests ubiquex/aws, so the dynamic path is the
// likely one rather than the exotic one. It must not ask for a region it
// would have to discard, and it must say why rather than going quiet.
func TestInit_TTYPrompt_UbxProvider_SkipsRegionAudibly(t *testing.T) {
	dir := t.TempDir()
	out, err := runUbxTTY(t, "ubiquex/aws\n3.0.0\n", nil, "init", "--dir", dir, "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx init (TTY prompt): %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "Region, optional") {
		t.Fatalf("prompted for a region that has nowhere to go on the dynamic path: %s", out)
	}
	if !strings.Contains(out, "fixed by the pinned snapshot") {
		t.Fatalf("skipped the region prompt silently, leaving the reader to notice on their own: %s", out)
	}
	cfg := loadConfigFrom(t, dir)
	if len(cfg.ProviderConfigs) != 0 {
		t.Errorf("prompt wrote a [provider_configs] entry for a dynamic provider: %v", cfg.ProviderConfigs)
	}
}

// A hand-written config gets the same refusal, at load, rather than at
// whichever command happens to configure a provider first. The config is
// wrong the moment it is written, so that is where it is reported.
func TestLoadConfig_ProviderConfigsForDynamicKey_Refused(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "playground"

[providers.aws]
source = "ubiquex/aws"
version = "3.0.0"

[provider_configs.aws]
region = "us-east-1"
`)
	out, err := runUbx(t, nil, "history", "--stack", "playground", "--ledger-dir", dir)
	if err == nil {
		t.Fatalf("expected the config to be refused at load, got success: %s", out)
	}
	all := out + errText(err)
	if !strings.Contains(all, "dynamic_providers.aws.auth") {
		t.Fatalf("expected the refusal to name the snapshot's own auth block, got: %s", all)
	}
	// Refused at load means every command, not only the two that configure
	// a provider. history touches no provider at all and must still refuse.
	if !strings.Contains(all, "provider_configs") {
		t.Fatalf("expected the offending table named, got: %s", all)
	}
}

// The mirror image, and the one that matters for not over-firing: a
// [provider_configs] entry keyed to a [thirdparty_providers] source is
// entirely legitimate and must load cleanly.
func TestLoadConfig_ProviderConfigsForThirdpartyKey_Allowed(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "playground"

[thirdparty_providers]
"fake/widget" = "0.1.0"

[provider_configs."fake/widget"]
region = "us-east-1"
`)
	out, err := runUbx(t, nil, "history", "--stack", "playground", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("a registry provider's own [provider_configs] entry must still load: %v\noutput: %s", err, out)
	}
}

// A dynamic and a registry provider can coexist, and only the dynamic
// one's entry is refused. Proves the check keys on the actual collision
// rather than on either table merely being present.
func TestLoadConfig_MixedTables_OnlyDynamicEntryRefused(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "playground"

[providers.aws]
source = "ubiquex/aws"
version = "3.0.0"

[thirdparty_providers]
"fake/widget" = "0.1.0"

[provider_configs."fake/widget"]
region = "us-east-1"
`)
	out, err := runUbx(t, nil, "history", "--stack", "playground", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("only the dynamic key's entry should be refused, but the whole config was: %v\noutput: %s", err, out)
	}
}
