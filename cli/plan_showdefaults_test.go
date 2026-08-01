package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// showDefaultsDraft is this file's own shared fixture: one assumption,
// one default, and one blocking question, so every test below can prove
// both halves of UBI-72's own rule in a single draft -- the AI-defaults
// block collapses under show_defaults=false, but the question never
// does.
const showDefaultsDraft = `{
	"schema_version": 1,
	"kind": "ubx:intent/v1",
	"stack": "payments",
	"intent": {
		"summary": "widget via ubx plan --from-doc",
		"assumptions": [
			{"text": "used the default tag set since none were specified", "affects": ["fake_widget.widget1.tags"]}
		],
		"defaults": [
			{"text": "left retention_days at the provider's own default", "affects": ["fake_widget.widget1.retention_days"]}
		],
		"questions": [
			{"text": "should this widget be public?", "affects": ["fake_widget.widget1.public"], "blocking": true}
		]
	},
	"resources": [
		{"type": "fake_widget", "name": "widget1", "op": "create", "config": "{\"name\":\"widget1\"}"}
	],
	"destroys": []
}`

// runPlanWithShowDefaultsDraft is every test below's own shared
// invocation: drafts showDefaultsDraft via the hermetic fake intent
// adapter (no real LLM/network) and resolves it through `ubx plan
// --from-doc`, with extraArgs appended verbatim (flags, mostly).
func runPlanWithShowDefaultsDraft(t *testing.T, ledgerDir string, extraArgs ...string) (out string, err error) {
	t.Helper()
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: showDefaultsDraft})

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	docPath := filepath.Join(ledgerDir, "payments.md")
	writeFile(t, docPath, "# Payments\n\nA single widget.")

	args := append([]string{
		"plan", "--from-doc", docPath, "--stack", "payments",
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir,
	}, extraArgs...)
	return runUbx(t, env, args...)
}

// TestPlan_ShowDefaults_DefaultTrue_FullBlockShown is the zero-config
// baseline: no [intent] show_defaults key anywhere in the cascade, no
// flag -- the full "AI defaults" block renders, same as before UBI-72
// existed. Proves IntentConfig.ShowDefaults' own nil-means-true default
// actually holds, not just that a bare bool would happen to zero-value
// the same way (it wouldn't distinguish "never set" from "set false").
func TestPlan_ShowDefaults_DefaultTrue_FullBlockShown(t *testing.T) {
	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)

	out, err := runPlanWithShowDefaultsDraft(t, ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan --from-doc: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "AI defaults — you are signing these:") {
		t.Fatalf("expected the full AI-defaults header by default, got: %s", out)
	}
	if !strings.Contains(out, "used the default tag set") || !strings.Contains(out, "left retention_days at the provider's own default") {
		t.Fatalf("expected both bullet lines rendered in full, got: %s", out)
	}
	if strings.Contains(out, "AI default(s) applied") {
		t.Fatalf("did not expect the collapsed one-liner when shown in full, got: %s", out)
	}
}

// TestPlan_ShowDefaults_ConfigFalse_CollapsesToOneLine is UBI-72's own
// headline behavior: [intent] show_defaults = false collapses the block
// to a one-line count naming --show-defaults and the saved plan file --
// never omitting the content, just not rendering it inline.
func TestPlan_ShowDefaults_ConfigFalse_CollapsesToOneLine(t *testing.T) {
	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)
	writeConfig(t, ledgerDir, `intent = { show_defaults = false }`)

	out, err := runPlanWithShowDefaultsDraft(t, ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan --from-doc: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "AI defaults — you are signing these:") {
		t.Fatalf("expected the full header collapsed away, got: %s", out)
	}
	if strings.Contains(out, "used the default tag set") || strings.Contains(out, "left retention_days at the provider's own default") {
		t.Fatalf("expected the bullet text collapsed away, got: %s", out)
	}
	if !strings.Contains(out, "2 AI default(s) applied -- ubx plan --show-defaults to review, or see the saved plan file") {
		t.Fatalf("expected the exact collapsed one-liner (1 assumption + 1 default = 2), got: %s", out)
	}

	// UBI-72: display toggle only -- the saved plan file still carries
	// the full assumption/default text regardless of what rendered.
	hash := mustExtractPlanHash(t, ledgerDir, out)
	_, p, err := resolvePlanHash(ledgerDir, hash)
	if err != nil {
		t.Fatalf("resolvePlanHash: %v", err)
	}
	if len(p.Intent.Assumptions) != 1 || p.Intent.Assumptions[0].Text != "used the default tag set since none were specified" {
		t.Fatalf("expected the full assumption still present in the saved plan file, got: %+v", p.Intent.Assumptions)
	}
	if len(p.Intent.Defaults) != 1 || p.Intent.Defaults[0].Text != "left retention_days at the provider's own default" {
		t.Fatalf("expected the full default still present in the saved plan file, got: %+v", p.Intent.Defaults)
	}
}

// TestPlan_ShowDefaults_QuestionsNeverCollapse proves the ticket's own
// explicit carve-out: a blocking question is never informational the
// way an assumption/default is, so it renders in full even with
// show_defaults=false -- only the AI-defaults portion collapses.
func TestPlan_ShowDefaults_QuestionsNeverCollapse(t *testing.T) {
	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)
	writeConfig(t, ledgerDir, `intent = { show_defaults = false }`)

	out, err := runPlanWithShowDefaultsDraft(t, ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan --from-doc: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "should this widget be public?") {
		t.Fatalf("expected the blocking question rendered in full even with show_defaults=false, got: %s", out)
	}
	if !strings.Contains(out, "[blocking -- review before accepting]") {
		t.Fatalf("expected the question's own blocking tag preserved, got: %s", out)
	}
	if !strings.Contains(out, "AI default(s) applied") {
		t.Fatalf("expected the AI-defaults portion still collapsed, got: %s", out)
	}
}

// TestPlan_ShowDefaultsFlag_OverridesConfigFalse proves --show-defaults
// wins over a config default of false, either direction the ticket
// names ("--show-defaults/--hide-defaults flags override config either
// direction").
func TestPlan_ShowDefaultsFlag_OverridesConfigFalse(t *testing.T) {
	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)
	writeConfig(t, ledgerDir, `intent = { show_defaults = false }`)

	out, err := runPlanWithShowDefaultsDraft(t, ledgerDir, "--show-defaults")
	if err != nil {
		t.Fatalf("ubx plan --from-doc --show-defaults: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "AI defaults — you are signing these:") {
		t.Fatalf("expected --show-defaults to override config's show_defaults=false, got: %s", out)
	}
	if strings.Contains(out, "AI default(s) applied") {
		t.Fatalf("did not expect the collapsed one-liner once --show-defaults forced the full block, got: %s", out)
	}
}

// TestPlan_HideDefaultsFlag_OverridesConfigDefaultTrue is
// TestPlan_ShowDefaultsFlag_OverridesConfigFalse's own mirror: no config
// at all (so the true default applies), --hide-defaults still collapses.
func TestPlan_HideDefaultsFlag_OverridesConfigDefaultTrue(t *testing.T) {
	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)

	out, err := runPlanWithShowDefaultsDraft(t, ledgerDir, "--hide-defaults")
	if err != nil {
		t.Fatalf("ubx plan --from-doc --hide-defaults: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "AI defaults — you are signing these:") {
		t.Fatalf("expected --hide-defaults to collapse the block despite no config override, got: %s", out)
	}
	if !strings.Contains(out, "AI default(s) applied") {
		t.Fatalf("expected the collapsed one-liner, got: %s", out)
	}
}

// TestPlan_ShowAndHideDefaultsFlags_MutuallyExclusive_Errors proves both
// flags at once is refused outright, never silently resolved one way.
func TestPlan_ShowAndHideDefaultsFlags_MutuallyExclusive_Errors(t *testing.T) {
	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)

	out, err := runPlanWithShowDefaultsDraft(t, ledgerDir, "--show-defaults", "--hide-defaults")
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "--show-defaults and --hide-defaults are mutually exclusive") {
		t.Fatalf("expected a clear mutual-exclusivity error, got: %v", err)
	}
}

// TestPlan_ShowDefaults_TTY_CollapsedLineIsPurple_NOCOLORStripsIt is
// UBI-72's own NO_COLOR/non-TTY discipline check: the collapsed one-liner
// is styled (purple, matching the AI-judgment color the full block's own
// header already uses) on a real terminal, and completely plain with
// NO_COLOR set even on that same forced terminal -- docs/cli-output-
// spec.md principle 2, unaffected by this ticket.
func TestPlan_ShowDefaults_TTY_CollapsedLineIsPurple_NOCOLORStripsIt(t *testing.T) {
	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)
	writeConfig(t, ledgerDir, `intent = { show_defaults = false }`)
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: showDefaultsDraft})

	docPath := filepath.Join(ledgerDir, "payments.md")
	writeFile(t, docPath, "# Payments\n\nA single widget.")

	out, err := runUbxTTY(t, "", []string{"FAKEPROVIDER_MODE=ok-v6", "NO_COLOR="},
		"plan", "--from-doc", docPath, "--stack", "payments",
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx plan --from-doc: %v\noutput: %s", err, out)
	}
	line := lineContaining(out, "AI default(s) applied")
	if line == "" {
		t.Fatalf("expected the collapsed one-liner, got: %s", out)
	}
	if !strings.HasPrefix(line, ansiPurple) || !strings.HasSuffix(line, ansiReset) {
		t.Fatalf("expected the collapsed line wrapped in purple, got: %q", line)
	}

	noColorOut, err := runUbxTTY(t, "", []string{"FAKEPROVIDER_MODE=ok-v6", "NO_COLOR=1"},
		"plan", "--from-doc", docPath, "--stack", "payments",
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx plan --from-doc (NO_COLOR): %v\noutput: %s", err, noColorOut)
	}
	if strings.Contains(noColorOut, ansiPurple) {
		t.Fatalf("expected NO_COLOR to strip all ANSI codes, got: %s", noColorOut)
	}
	if !strings.Contains(noColorOut, "AI default(s) applied") {
		t.Fatalf("expected the collapsed text to still render plainly under NO_COLOR, got: %s", noColorOut)
	}
}
