package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnownDependentsFor(t *testing.T) {
	cfgWith := func(dirs ...string) *Config {
		c := &Config{}
		c.KnownDependents = dirs
		return c
	}

	for _, tc := range []struct {
		name  string
		cfg   *Config
		flags []string
		want  []string
	}{
		{"neither", cfgWith(), nil, []string{}},
		{"config only", cfgWith("../networking"), nil, []string{"../networking"}},
		{"flag only", cfgWith(), []string{"../security"}, []string{"../security"}},
		// The load-bearing case: a flag adds a neighbour, it does not
		// replace the configured ones. Replacing would turn "also check
		// here" into "check only here" without saying so, which is the
		// wrong direction for a safety check to fail in.
		{"flag adds to config", cfgWith("../networking"), []string{"../security"},
			[]string{"../networking", "../security"}},
		{"duplicate across sources", cfgWith("../networking"), []string{"../networking"},
			[]string{"../networking"}},
		{"duplicate within config", cfgWith("../a", "../a", "../b"), nil, []string{"../a", "../b"}},
		{"empty strings dropped", cfgWith("", "../a"), []string{""}, []string{"../a"}},
		{"nil config", nil, []string{"../security"}, []string{"../security"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := knownDependentsFor(tc.cfg, tc.flags)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// writeKnownDependentsConfig points the config cascade at dir and gives
// it a known_dependents list.
func writeKnownDependentsConfig(t *testing.T, dir string, dependents ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(dependents))
	for i, d := range dependents {
		quoted[i] = "\"" + d + "\""
	}
	content := "known_dependents = [" + strings.Join(quoted, ", ") + "]\n"
	if err := os.WriteFile(filepath.Join(dir, ".ubx", "config.hcl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := configSearchStartDir
	configSearchStartDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { configSearchStartDir = orig })
}

// seedShippedWidget creates and ships one fake_widget into dir's ledger,
// returning its address. A shipped create is what the orphan check and
// the fold both operate on, so an adopted resource would not exercise
// the same path.
func seedShippedWidget(t *testing.T, dir, stack, name string) string {
	t.Helper()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	intentPath := filepath.Join(dir, name+"-intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          stack,
		"intent":         map[string]interface{}{"summary": "create " + name},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": name, "op": "create",
				"config": map[string]interface{}{"name": name + "-widget"}},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("seed plan: %v\n%s", err, planOut)
	}
	hash := mustExtractPlanHash(t, dir, planOut)
	if _, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", dir, "--yes"); err != nil {
		t.Fatalf("seed ship: %v", err)
	}
	return stack + ".fake_widget." + name
}

// pinAcross makes consumerDir's stack hold a real cross_stack_pin
// against targetAddr in producerDir, the exact record the orphan check
// walks neighbour ledgers looking for.
func pinAcross(t *testing.T, consumerDir, consumerStack, producerDir, targetAddr string) {
	t.Helper()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	intentPath := filepath.Join(consumerDir, "cross-intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          consumerStack,
		"intent":         map[string]interface{}{"summary": "consume across stacks"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "consumer", "op": "create",
				"config": map[string]interface{}{
					"name": map[string]interface{}{
						"$cross": map[string]interface{}{"ledger_dir": producerDir, "to": targetAddr + ".name"},
					},
				}},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", consumerDir)
	if err != nil {
		t.Fatalf("cross plan: %v\n%s", err, planOut)
	}
	hash := mustExtractPlanHash(t, consumerDir, planOut)
	if _, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", consumerDir, "--yes"); err != nil {
		t.Fatalf("cross ship: %v", err)
	}
}

// The decisive test: known_dependents in .ubx/config, no flag anywhere,
// and a real cross-stack pin is found and the destroy refused.
//
// Everything else here is about a status string. This is about whether
// the config key actually buys protection, which is the only reason to
// add it.
func TestTerminate_KnownDependentsFromConfigAlone_RefusesOrphaningDestroy(t *testing.T) {
	producer := t.TempDir()
	consumer := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	target := seedShippedWidget(t, producer, "payments", "target")
	pinAcross(t, consumer, "networking", producer, target)

	writeKnownDependentsConfig(t, producer, consumer)

	out, err := runUbx(t, env, "terminate", target, "--provider", fakeProviderBinary, "--ledger-dir", producer)
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "pinned by a cross-stack reference") {
		t.Fatalf("expected the configured neighbour's pin to refuse this destroy, got: %v", err)
	}
}

// The same setup with nothing configured still succeeds, and says so.
// This is the state every destroy was in before this change: allowed,
// unchecked, and silent about it.
func TestTerminate_NoKnownDependents_WarnsRatherThanChecking(t *testing.T) {
	producer := t.TempDir()
	consumer := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	target := seedShippedWidget(t, producer, "payments", "target")
	pinAcross(t, consumer, "networking", producer, target)

	out, err := runUbx(t, env, "terminate", target, "--provider", fakeProviderBinary, "--ledger-dir", producer)
	if err != nil {
		t.Fatalf("an unchecked destroy still resolves, by design: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no dependent stacks were checked") {
		t.Fatalf("the gap must be visible in the receipt, not only in the proposal JSON, got: %s", out)
	}
	if !strings.Contains(out, "known_dependents") || !strings.Contains(out, "--known-dependent") {
		t.Fatalf("the warning must name both remedies, got: %s", out)
	}
}

// A configured neighbour that does not pin the target renders the
// positive result, naming what was checked. Without this line a clean
// check and a ubx that performs no check at all look identical.
func TestTerminate_KnownDependentsClear_RendersWhatWasChecked(t *testing.T) {
	producer := t.TempDir()
	neighbour := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	target := seedShippedWidget(t, producer, "payments", "target")
	seedShippedWidget(t, neighbour, "networking", "unrelated")
	writeKnownDependentsConfig(t, producer, neighbour)

	out, err := runUbx(t, env, "terminate", target, "--provider", fakeProviderBinary, "--ledger-dir", producer)
	if err != nil {
		t.Fatalf("ubx terminate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cross-stack check:") || !strings.Contains(out, neighbour) {
		t.Fatalf("expected the receipt to name the neighbour that was checked, got: %s", out)
	}
	if strings.Contains(out, "no dependent stacks were checked") {
		t.Fatalf("a performed check must not also warn that none was performed: %s", out)
	}
}

// The flag adds to the configured list rather than replacing it, proven
// where it matters: the configured neighbour is the one holding the pin,
// and a flag naming an unrelated directory must not disarm it.
func TestTerminate_FlagDoesNotReplaceConfiguredDependents(t *testing.T) {
	producer := t.TempDir()
	pinning := t.TempDir()
	unrelated := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	target := seedShippedWidget(t, producer, "payments", "target")
	pinAcross(t, pinning, "networking", producer, target)
	seedShippedWidget(t, unrelated, "security", "unrelated")
	writeKnownDependentsConfig(t, producer, pinning)

	out, err := runUbx(t, env, "terminate", target,
		"--provider", fakeProviderBinary, "--ledger-dir", producer,
		"--known-dependent", unrelated)
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "pinned by a cross-stack reference") {
		t.Fatalf("a --known-dependent flag replaced the configured list instead of adding to it, disarming a real pin: %v", err)
	}
}

// The ship confirmation is the last moment before a destroy is signed,
// and often a different person, or the same person days later, than the
// one who read the plan.
func TestShip_ConfirmationSurfacesUncheckedOrphanRisk(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	target := seedShippedWidget(t, ledgerDir, "payments", "target")
	termOut, err := runUbx(t, env, "terminate", target, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx terminate: %v\n%s", err, termOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, termOut)

	shipOut, _ := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir, "--confirm-destroys")
	if !strings.Contains(shipOut, "no dependent stacks were checked") {
		t.Fatalf("the ship confirmation must carry the same warning the plan did, got: %s", shipOut)
	}
}

// known_dependents is a real config key, not one the cascade warns about
// and then decodes anyway. Four keys in this struct are in exactly that
// state already (gitlab_project, azure_devops_project,
// bitbucket_server_url, bitbucket_server_project are decoded but absent
// from knownTopLevelKeys), which is what makes this worth asserting
// rather than assuming.
func TestConfig_KnownDependentsIsARecognisedKey(t *testing.T) {
	dir := t.TempDir()
	writeKnownDependentsConfig(t, dir, "../networking", "../security")

	warnings := &strings.Builder{}
	cfg, err := LoadConfig(warnings)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if strings.Contains(warnings.String(), "known_dependents") {
		t.Fatalf("known_dependents warned as an unknown key: %s", warnings.String())
	}
	if len(cfg.KnownDependents) != 2 || cfg.KnownDependents[0] != "../networking" {
		t.Fatalf("known_dependents did not decode: %v", cfg.KnownDependents)
	}
}
