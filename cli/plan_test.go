package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestPlanShip_SimpleCreate_FusedAcceptApply exercises UBI-49's own two-step
// fusion end to end: `ubx plan` resolves an intent file into a draft, renders
// its full receipt, and saves it under .ubx/plans/<hash>.json; `ubx ship
// <hash>` -- with no separate `ubx accept` in between -- finds it there,
// accepts it inline (local tier), and applies it against the real fake
// provider binary, exactly like the four-verb ceremony's own
// TestResolveAcceptShip_CreateChain_RealFakeProvider but collapsed to two
// commands.
func TestPlanShip_SimpleCreate_FusedAcceptApply(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "widget1",
				"op":   "create",
				"config": map[string]interface{}{
					"name": "widget1",
					"tags": map[string]string{"env": "prod"},
				},
			},
		},
	})

	planOut, err := runUbx(t, env, "plan", intentPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	if !strings.Contains(planOut, "delta: +1 create(s), ~0 modify(ies), -0 destroy(s)") {
		t.Fatalf("expected a delta line in the receipt, got: %s", planOut)
	}
	if !strings.Contains(planOut, "blast radius: +1 ~0 -0") {
		t.Fatalf("expected a blast radius line in the receipt, got: %s", planOut)
	}
	if !strings.Contains(planOut, "cost delta: $") {
		t.Fatalf("expected a cost delta line in the receipt, got: %s", planOut)
	}

	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	planFile := filepath.Join(ledgerDir, ".ubx", "plans", hash+".json")
	if _, err := os.Stat(planFile); err != nil {
		t.Fatalf("expected a saved plan file at %s: %v", planFile, err)
	}

	shipOut, err := runUbx(t, env, "ship", hash,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--yes",
	)
	if err != nil {
		t.Fatalf("ubx ship <hash> (no prior accept): %v\noutput: %s", err, shipOut)
	}
	// UBI-49 polish: ship's own "accepted" line shows the short hash form
	// now (docs/cli-output-spec.md principle 3), not the full one.
	if !strings.Contains(shipOut, "accepted "+displayHash(hash, false)+" (stack payments) via local plan") {
		t.Fatalf("expected ship to report inline local-tier acceptance, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut)
	}

	whyOut, err := runUbx(t, env, "why", hash, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "via local at") {
		t.Fatalf("expected why to show local acceptance method, got: %s", whyOut)
	}
	if !strings.Contains(whyOut, "ship history:") {
		t.Fatalf("expected why to show ship history, got: %s", whyOut)
	}
}

// TestPlanShip_DestroysRequireConfirmFlag mirrors
// TestAccept_DestroysRequireConfirmFlag_Refused for the fused ship path:
// inline local-tier acceptance is still subject to the same
// --confirm-destroys friction (docs/schema.md) as standalone `ubx accept`.
func TestPlanShip_DestroysRequireConfirmFlag(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// Seed a resource to destroy via the ordinary scan/accept adoption path.
	adoptOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "victim",
		"--lookup", `{"id":"victim"}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	requireExitCode(t, err, 1, adoptOut)
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}
	ledgerHeadBefore := readLedgerHead(t, ledgerDir)

	destroyIntentPath := filepath.Join(ledgerDir, "destroy-intent.json")
	writeFile(t, destroyIntentPath, `{
		"schema_version": 1,
		"kind": "ubx:intent/v1",
		"stack": "payments",
		"intent": {"summary": "decommission victim via ubx plan"},
		"resources": [],
		"destroys": ["payments.fake_widget.victim"]
	}`)

	planOut, err := runUbx(t, env, "plan", destroyIntentPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx plan (destroy): %v\noutput: %s", err, planOut)
	}
	if !strings.Contains(planOut, "destroy: payments.fake_widget.victim") {
		t.Fatalf("expected the receipt to render the destroy target, got: %s", planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if exitCode(err) != 1 {
		t.Fatalf("expected exit 1 without --confirm-destroys, got %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(err.Error(), "--confirm-destroys") {
		t.Fatalf("expected the error to name --confirm-destroys, got: %v", err)
	}
	if got := readLedgerHead(t, ledgerDir); got != ledgerHeadBefore {
		t.Fatalf("ledger head moved to %q without --confirm-destroys -- the refused plan must never be accepted", got)
	}

	shipOut2, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--confirm-destroys", "--yes")
	if err != nil {
		t.Fatalf("ubx ship --confirm-destroys: %v\noutput: %s", err, shipOut2)
	}
	if !strings.Contains(shipOut2, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut2)
	}
}

// TestShip_PlanFile_HashMismatch_Refused proves the plan-store fallback
// verifies content against the hash it was found at, rather than trusting
// filename==hash blindly -- a hand-edited or corrupted plan file must be
// refused, not silently shipped under the caller's requested hash.
func TestShip_PlanFile_HashMismatch_Refused(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget for tamper test"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	planFile := filepath.Join(ledgerDir, ".ubx", "plans", hash+".json")
	raw, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]interface{}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	p["intent"].(map[string]interface{})["summary"] = "tampered after planning"
	tampered, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planFile, tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, shipOut)
	if !strings.Contains(err.Error(), "hashes to") {
		t.Fatalf("expected a hash-mismatch error, got: %v", err)
	}
}

// TestShip_NoSuchHash_Errors confirms a hash matching neither an accepted
// ledger entry nor a locally saved plan is a genuine error (exit 2), with a
// message naming both places ship looked.
func TestShip_NoSuchHash_Errors(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	fakeHash := strings.Repeat("a", 64)

	shipOut, err := runUbx(t, env, "ship", fakeHash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, shipOut)
	if !strings.Contains(err.Error(), "no accepted proposal") || !strings.Contains(err.Error(), "no plan file") {
		t.Fatalf("expected an error naming both lookup paths, got: %v", err)
	}
}

// TestPlanShip_CrossStackPin_StaleBlocksShip is the plan/ship-fusion twin of
// TestResolveAccept_CrossStackPin_StaleBlocksAccept: a cross-stack pin
// resolved by `ubx plan` must still block inline acceptance if the neighbor
// ledger advances before `ubx ship` runs -- resolver.VerifyPins is called
// unconditionally from acceptPlanInline exactly like it is from accept.go.
func TestPlanShip_CrossStackPin_StaleBlocksShip(t *testing.T) {
	stackDir := t.TempDir()
	neighborDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	neighborAdopt := filepath.Join(neighborDir, "adopt.json")
	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "networking", "--type", "fake_widget", "--name", "vpc",
		"--lookup", `{"name":"vpc","tags":{"env":"prod"}}`,
		"--ledger-dir", neighborDir,
		"--out", neighborAdopt,
	)
	requireExitCode(t, err, 1, scanOut)
	if _, err := runUbx(t, env, "accept", neighborAdopt, "--ledger-dir", neighborDir); err != nil {
		t.Fatalf("ubx accept (neighbor seed): %v", err)
	}

	intentPath := filepath.Join(stackDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget pinned to neighbor vpc, via ubx plan"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "widget1",
				"op":   "create",
				"config": map[string]interface{}{
					"name": map[string]interface{}{
						"$cross": map[string]interface{}{"ledger_dir": neighborDir, "to": "networking.fake_widget.vpc.name"},
					},
				},
			},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", stackDir)
	if err != nil {
		t.Fatalf("ubx plan (cross-stack): %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, stackDir, planOut)

	neighborAdopt2 := filepath.Join(neighborDir, "adopt2.json")
	scanOut2, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "networking", "--type", "fake_widget", "--name", "subnet",
		"--lookup", `{"name":"subnet"}`,
		"--ledger-dir", neighborDir,
		"--out", neighborAdopt2,
	)
	requireExitCode(t, err, 1, scanOut2)
	if _, err := runUbx(t, env, "accept", neighborAdopt2, "--ledger-dir", neighborDir); err != nil {
		t.Fatalf("ubx accept (neighbor advance): %v", err)
	}

	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", stackDir)
	requireExitCode(t, err, 1, shipOut)
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected a stale cross-stack pin error, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(stackDir, ".ubx", "ledger.lock")); statErr == nil {
		t.Fatal("ledger.lock should not exist -- the stale-pinned plan must not have been accepted")
	}
}

// TestPlan_MutuallyExclusiveInputs / TestPlan_RequiresOneInput cover the
// same input-mode validation propose.go's own tests already establish for
// --from-doc/--from-diagram, extended to plan's fourth mode (--from-code).
func TestPlan_MutuallyExclusiveInputs(t *testing.T) {
	ledgerDir := t.TempDir()
	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeFile(t, intentPath, `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x"},"resources":[]}`)

	_, err := runUbx(t, nil, "plan", intentPath, "--from-code", "entry.ts", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive-inputs error, got: %v", err)
	}
}

func TestPlan_RequiresOneInput(t *testing.T) {
	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "plan", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "requires exactly one of") {
		t.Fatalf("expected a requires-one-input error, got: %v", err)
	}
}

// TestPlan_FromCode_SimpleCreate proves `ubx plan --from-code` drives the
// exact same evaluator + resolve path "ubx resolve --from-code" already
// uses (resolve_from_code_test.go's own TestResolveFromCode_SimpleCreate),
// through the fused command.
func TestPlan_FromCode_SimpleCreate(t *testing.T) {
	requireDeno(t)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	entryPath := filepath.Join("testdata", "sdk_resolve", "create_widget.ts")

	planOut, err := runUbx(t, env, "plan",
		"--from-code", entryPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx plan --from-code: %v\noutput: %s", err, planOut)
	}
	if !strings.Contains(planOut, "blast radius: +1 ~0 -0") {
		t.Fatalf("expected a 1-create blast radius, got: %s", planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes")
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut)
	}
}

// TestPlanShip_FromDoc_FullReceipt is `ubx plan --from-doc`'s own defining
// behavior, distinct from `ubx propose --from-doc` (which deliberately
// stops at the draft, never resolving): the SAME command drafts via the
// configured intent provider AND resolves the result, rendering both the
// draft's own ambiguity content and the resolved delta/blast radius
// together in one receipt -- the human checkpoint this fusion relies on
// instead of a separate draft-review step.
func TestPlanShip_FromDoc_FullReceipt(t *testing.T) {
	fake := &fakeIntentAdapter{draft: `{
		"schema_version": 1,
		"kind": "ubx:intent/v1",
		"stack": "payments",
		"intent": {
			"summary": "widget via ubx plan --from-doc",
			"assumptions": [
				{"text": "used the default tag set since none were specified", "affects": ["fake_widget.widget1.tags"]}
			]
		},
		"resources": [
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": "{\"name\":\"widget1\"}"}
		],
		"destroys": []
	}`}
	withBuildIntentAdapter(t, fake)

	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	docPath := filepath.Join(ledgerDir, "payments.md")
	writeFile(t, docPath, "# Payments\n\nA single widget.")

	planOut, err := runUbx(t, env, "plan",
		"--from-doc", docPath, "--stack", "payments",
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx plan --from-doc: %v\noutput: %s", err, planOut)
	}
	if !strings.Contains(planOut, "blast radius: +1 ~0 -0") {
		t.Fatalf("expected the resolved delta's own blast radius, got: %s", planOut)
	}
	if !strings.Contains(planOut, "AI defaults") || !strings.Contains(planOut, "used the default tag set") {
		t.Fatalf("expected the draft's own assumption alongside the resolved receipt, got: %s", planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes")
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut)
	}
}

// TestPlanShip_FromDiagram_ResolvesAndApplies mirrors
// TestProposeFromDiagram_NoAmbiguity_RendersUnambiguousMessage's own setup
// (a real UBX_PROVIDER_MIRROR-backed provider, a real [providers] table)
// but drives it through `ubx plan --from-diagram` all the way to a fused
// `ubx ship`, proving the diagram medium's own double provider-load
// (documented in plan.go) still produces a correct, appliable result.
// TestPlanShip_FromDiagram_Resolves proves the diagram-medium half that's
// actually always true: `ubx plan --from-diagram` resolves a real
// proposal end to end. It does NOT ship it -- see
// TestPlanShip_FromDiagram_RequiredAttributeMissing_ShipRefusesClearly,
// below, for why: diagram.Parse always emits an empty "{}" config
// (topology only, never attribute values, diagram/parse.go) has no
// mechanism to supply one, so shipping a diagram-authored resource
// against a real schema with ANY Required attribute (true of nearly
// every real resource type, `fake_widget`'s own "name" included) now
// correctly refuses rather than silently sending a null the real
// provider would reject with a cryptic error (UBI-63 session 2's own
// required-attribute validation, provider/ctyvalue.go). This is a real,
// pre-existing diagram-medium limitation this validation surfaced, not
// a regression from it -- see docs/diagram-medium.md's own "topology
// only" scope note.
func TestPlanShip_FromDiagram_Resolves(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[providers]
"fake/widget" = "0.1.0"
`)

	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, `
classes: {
  fake_widget: {}
}
payments: {
  primary: main-widget {
    class: fake_widget
  }
}
`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6", "UBX_PROVIDER_MIRROR=" + mirrorDir}
	planOut, err := runUbx(t, env, "plan", "--from-diagram", diagramPath, "--stack", "payments", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx plan --from-diagram: %v\noutput: %s", err, planOut)
	}
	if !strings.Contains(planOut, "blast radius: +1 ~0 -0") {
		t.Fatalf("expected a 1-create blast radius, got: %s", planOut)
	}
}

// TestPlanShip_FromDiagram_RequiredAttributeMissing_ShipRefusesClearly
// is the negative twin, above: shipping the identical diagram-authored
// proposal now fails with a clear, actionable error naming the exact
// missing attribute -- never the old cryptic remote rejection a real
// provider would have returned for a silently-nulled required field.
func TestPlanShip_FromDiagram_RequiredAttributeMissing_ShipRefusesClearly(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[providers]
"fake/widget" = "0.1.0"
`)

	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, `
classes: {
  fake_widget: {}
}
payments: {
  primary: main-widget {
    class: fake_widget
  }
}
`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6", "UBX_PROVIDER_MIRROR=" + mirrorDir}
	planOut, err := runUbx(t, env, "plan", "--from-diagram", diagramPath, "--stack", "payments", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx plan --from-diagram: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, dir, planOut)

	// A "failed" outcome is the expected, correct result here -- ubx
	// ship's own non-zero exit for it surfaces as a non-nil err, not a
	// test failure.
	shipOut, _ := runUbx(t, env, "ship", hash, "--ledger-dir", dir, "--yes")
	if !strings.Contains(shipOut, `a required attribute is missing from config: "name"`) {
		t.Fatalf("expected a clear, actionable required-attribute error naming \"name\", got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "outcome: failed") {
		t.Fatalf("expected outcome: failed, got: %s", shipOut)
	}
}

func readLedgerHead(t *testing.T, ledgerDir string) string {
	t.Helper()
	head, err := core.Open(ledgerDir).Head()
	if err != nil {
		t.Fatalf("read ledger head: %v", err)
	}
	return head
}

// mustExtractPlanHash pulls the "ubx-proposal: <hash>" line `ubx plan`
// prints as its own last line -- unlike `ubx propose`'s bare trailer-only
// output, plan's own hash line follows a full receipt, so this scans for
// the line rather than assuming it's the entire output. UBI-49 polish:
// the line itself only ever shows the short (12-char + "…") form now
// (docs/cli-output-spec.md principle 3 -- the full hash is no longer
// printed anywhere in human-text output at all), so this resolves it
// back to the real, full hash via resolvePlanHash -- the exact same
// prefix-resolution `ubx ship`/`ubx accept` themselves use, not a
// test-only shortcut.
func mustExtractPlanHash(t *testing.T, ledgerDir, planOutput string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^ubx-proposal: ([0-9a-f]+)`).FindStringSubmatch(planOutput)
	if m == nil {
		t.Fatalf("could not find a ubx-proposal hash line in plan output: %s", planOutput)
	}
	fullHash, _, err := resolvePlanHash(ledgerDir, m[1])
	if err != nil {
		t.Fatalf("resolvePlanHash(%s) from plan output's own short hash: %v", m[1], err)
	}
	return fullHash
}
