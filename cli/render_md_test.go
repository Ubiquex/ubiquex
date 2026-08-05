package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/diagram"
)

// TestRenderMd_TwoResourcesWithDependsOn_RealFakeProvider is the markdown
// mirror of TestRender_TwoResourcesWithDependsOn_RealFakeProvider
// (render_test.go) -- same real shipped fleet, --md output instead of D2.
func TestRenderMd_TwoResourcesWithDependsOn_RealFakeProvider(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	out, err := runUbx(t, nil, "render", "--md", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render --md: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "# payments — current state") {
		t.Fatalf("expected the stated CURRENT STATE header, got: %s", out)
	}
	if !strings.Contains(out, "never hand-edit") {
		t.Fatalf("expected the design principle stated in the header (UBI-86's own explicit requirement), got: %s", out)
	}
	if !strings.Contains(out, "#### primary") || !strings.Contains(out, "#### mirror") {
		t.Fatalf("expected both resources by name, got: %s", out)
	}
	if !strings.Contains(out, "### fake_widget") {
		t.Fatalf("expected a fake_widget type heading, got: %s", out)
	}
	if !strings.Contains(out, "`name`: primary-widget") {
		t.Fatalf("expected primary's own real applied attributes in readable form, got: %s", out)
	}
	if !strings.Contains(out, "depends on: `payments.fake_widget.primary`") {
		t.Fatalf("expected mirror's own real depends_on edge, got: %s", out)
	}
	// Never a raw JSON dump of the whole resource -- readable formatted
	// bullets only.
	if strings.Contains(out, `{"name"`) || strings.Contains(out, `"tags":{`) {
		t.Fatalf("expected no raw JSON dump of attribute values, got: %s", out)
	}
}

func TestRenderMd_NestedAttribute_RendersAsSubBullets(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	out, err := runUbx(t, nil, "render", "--md", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render --md: %v\noutput: %s", err, out)
	}
	// primary's own config carries tags: {"env": "prod"} -- must render as
	// a nested sub-bullet, never a compact inline JSON blob.
	if !strings.Contains(out, "- `tags`:\n") {
		t.Fatalf("expected `tags` to introduce a nested sub-bullet block, got: %s", out)
	}
	if !strings.Contains(out, "  - `env`: prod") {
		t.Fatalf("expected a nested `env: prod` sub-bullet, got: %s", out)
	}
}

func TestRenderMd_Redaction_NeverRendersSecretMaterial(t *testing.T) {
	ledgerDir := t.TempDir()
	const secret = "top-secret-api-key-value"

	adoptEnv := append(append([]string{}, sensitiveEnv...), "FAKEPROVIDER_MUTATE_ATTR=api_key", "FAKEPROVIDER_MUTATE_VALUE="+secret)
	adoptPath := filepath.Join(ledgerDir, "adopt.json")
	scanOut, err := runUbx(t, adoptEnv, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_secret_resource",
		"--name", "widget1",
		"--lookup", `{"id":"widget1"}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
	)
	requireExitCode(t, err, 1, scanOut)

	acceptOut, err := runUbx(t, adoptEnv, "accept", adoptPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}

	out, err := runUbx(t, nil, "render", "--md", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render --md: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("redacted material must never render, got: %s", out)
	}
	if !strings.Contains(out, "`api_key`: (redacted)") {
		t.Fatalf("expected (redacted) for the sensitive attribute, got: %s", out)
	}
}

// TestRenderMd_BlueprintCall_GroupsUnderHeading mirrors render_blueprint_
// test.go's own TestRender_BlueprintCall_GroupsInDashedContainer_
// RealFakeProvider exactly (same real on-disk Go blueprint package, same
// diagram ubx_blueprint call, same resolve/accept/ship pipeline) --
// asserting on markdown's own grouping heading instead of the D2 dashed
// container, proving --md reuses the IDENTICAL grouping basis
// (diagram.Resource.BlueprintRef), not a second one.
func TestRenderMd_BlueprintCall_GroupsUnderHeading_RealFakeProvider(t *testing.T) {
	requireHermeticSandbox(t)
	pkgDir := writeRenderBlueprintPackage(t)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	d2Src := `
platform: "platform call" {
  class: ubx_blueprint
  blueprint: ` + jsonQuote(pkgDir) + `
  primary_name: "widget1"
}
`
	diagramIntent, err := diagram.Parse("render-md-blueprint.d2", strings.NewReader(d2Src), "platform", nil, diagram.Options{})
	if err != nil {
		t.Fatalf("diagram.Parse: %v", err)
	}
	if len(diagramIntent.BlueprintCalls) != 1 {
		t.Fatalf("expected exactly 1 blueprint call from the diagram, got %d", len(diagramIntent.BlueprintCalls))
	}
	diagramIntent.Intent.Summary = "platform, via a diagram blueprint call"
	intentPath := filepath.Join(t.TempDir(), "diagram-draft.json")
	writeResolverIntentFile(t, intentPath, diagramIntent)

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
	)
	if err != nil {
		t.Fatalf("ubx resolve (diagram blueprint call): %v\noutput: %s", err, resolveOut)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	shipOut, err := runUbx(t, env, "ship", changeID,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut)
	}

	renderOut, err := runUbx(t, nil, "render", "--md", "--stack", "platform", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render --md: %v\noutput: %s", err, renderOut)
	}

	if !strings.Contains(renderOut, "## Blueprint: platform:sha256:") {
		t.Fatalf("expected a blueprint-grouped heading with the real ref, got:\n%s", renderOut)
	}
	if !strings.Contains(renderOut, "#### primary") || !strings.Contains(renderOut, "#### mirror") {
		t.Fatalf("expected both blueprint-created resources by name, got:\n%s", renderOut)
	}
	if strings.Count(renderOut, "## Blueprint:") != 1 {
		t.Fatalf("expected exactly one blueprint group (both resources share one call), got:\n%s", renderOut)
	}
}

func TestRenderMd_Check_MatchingFile_Succeeds(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	outPath := filepath.Join(ledgerDir, "rendered.md")
	if out, err := runUbx(t, nil, "render", "--md", "--stack", "payments", "--ledger-dir", ledgerDir, "--out", outPath); err != nil {
		t.Fatalf("ubx render --md (write): %v\noutput: %s", err, out)
	}

	out, err := runUbx(t, nil, "render", "--md", "--stack", "payments", "--ledger-dir", ledgerDir, "--out", outPath, "--check")
	if err != nil {
		t.Fatalf("ubx render --md --check (should match): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "matches the current resolved state") {
		t.Fatalf("unexpected stdout: %s", out)
	}
}

func TestRenderMd_Check_StaleFile_Fails(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	outPath := filepath.Join(ledgerDir, "rendered.md")
	writeFile(t, outPath, "# stale\n")

	out, err := runUbx(t, nil, "render", "--md", "--stack", "payments", "--ledger-dir", ledgerDir, "--out", outPath, "--check")
	requireExitCode(t, err, 1, out)
	if !strings.Contains(err.Error(), "is stale") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderMd_Determinism_RepeatedRunsByteIdentical(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	first, err := runUbx(t, nil, "render", "--md", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render --md: %v\noutput: %s", err, first)
	}
	second, err := runUbx(t, nil, "render", "--md", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render --md (rerun): %v\noutput: %s", err, second)
	}
	if first != second {
		t.Fatalf("ubx render --md produced different output on a rerun against an unchanged ledger:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestRenderMd_EmptyStack_SucceedsWithPlaceholder(t *testing.T) {
	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "render", "--md", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render --md (no live resources): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "No live resources") {
		t.Fatalf("expected an explicit no-resources placeholder, got: %q", out)
	}
}
