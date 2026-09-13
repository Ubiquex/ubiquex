package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// blueprint_plan_provenance_test.go covers UBI-257: `ubx plan` never
// stamped a blueprint's content hash, so the tamper-evidence a
// blueprint gives up full language power to buy was collected on
// `resolve` and dropped on the verb most people run.

// TestPlan_DirectSDKImport_StampsTheContentHash is the ticket's own
// first defect, at the level that matters: not the resolved JSON, but
// the plan file `ubx ship` will later accept verbatim.
//
// Before this, that file recorded {"kind":"blueprint","ref":"platform"}
// -- a bare name. The ledger could then say WHICH blueprint produced a
// resource but never WHICH BYTES, so two versions of a blueprint were
// indistinguishable after the fact, which is precisely the question a
// ledger exists to answer.
func TestPlan_DirectSDKImport_StampsTheContentHash(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	pkgDir := writeBlueprintPackage(t, dir, "platform")
	entry := writeBlueprintCallingStack(t, dir, pkgDir, `"widget1"`)

	out, err := runUbx(t, env, "plan",
		"--from-code", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--timeout", "60s",
	)
	if err != nil {
		t.Fatalf("ubx plan --from-code: %v\noutput: %s", err, out)
	}

	for _, ref := range planBlueprintRefs(t, ledgerDir) {
		name, hash, ok := strings.Cut(ref, ":")
		if !ok || name != "platform" || !strings.HasPrefix(hash, "sha256:") {
			t.Errorf("plan recorded ref %q, want \"platform:sha256:<hex>\" -- a bare name is the whole defect", ref)
		}
	}
}

// plan and resolve have to agree, since the ticket's own framing is
// that a user doing the ordinary thing got a weaker record than a user
// doing the rigorous thing, with no warning either way.
func TestPlan_AndResolve_RecordTheIdenticalBlueprintRef(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	pkgDir := writeBlueprintPackage(t, dir, "platform")
	entry := writeBlueprintCallingStack(t, dir, pkgDir, `"widget1"`)

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	if out, err := runUbx(t, env, "resolve",
		"--from-code", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
		"--timeout", "60s",
	); err != nil {
		t.Fatalf("ubx resolve: %v\n%s", err, out)
	}
	if out, err := runUbx(t, env, "plan",
		"--from-code", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--timeout", "60s",
	); err != nil {
		t.Fatalf("ubx plan: %v\n%s", err, out)
	}

	resolveRefs := docBlueprintRefs(t, resolvedPath)
	planRefs := planBlueprintRefs(t, ledgerDir)
	if len(resolveRefs) == 0 || len(planRefs) == 0 {
		t.Fatalf("expected refs from both verbs, got resolve=%v plan=%v", resolveRefs, planRefs)
	}
	for i := range resolveRefs {
		if resolveRefs[i] != planRefs[i] {
			t.Errorf("verbs disagree: resolve=%q plan=%q", resolveRefs[i], planRefs[i])
		}
	}
}

// planBlueprintRefs reads every blueprint source ref out of the single
// plan file plan just wrote.
func planBlueprintRefs(t *testing.T, ledgerDir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(ledgerDir, ".ubx", "plans", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 plan file under %s, found %d", ledgerDir, len(matches))
	}
	return docBlueprintRefs(t, matches[0])
}

// docBlueprintRefs pulls blueprint refs out of a proposal document,
// whether it is a bare proposal or a plan file wrapping one.
func docBlueprintRefs(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Proposal *struct {
			Delta deltaWithSources `json:"delta"`
		} `json:"proposal"`
		Delta deltaWithSources `json:"delta"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, raw)
	}
	delta := doc.Delta
	if doc.Proposal != nil {
		delta = doc.Proposal.Delta
	}

	var refs []string
	for _, c := range delta.Creates {
		for _, s := range c.Sources {
			if s.Kind == "blueprint" {
				refs = append(refs, s.Ref)
			}
		}
	}
	if len(refs) == 0 {
		t.Fatalf("no blueprint sources at all in %s:\n%s", path, raw)
	}
	return refs
}

type deltaWithSources struct {
	Creates []struct {
		Sources []struct {
			Kind string `json:"kind"`
			Ref  string `json:"ref"`
		} `json:"sources"`
	} `json:"creates"`
}

// TestPlan_RefusesABlueprintItCannotHash is the strictness decision,
// pinned because it is a real behaviour change rather than a pure fix.
//
// plan used to fall back to a bare name here and carry on. It now
// refuses, matching resolve. A fallback would make the tamper-evidence
// optional, and collecting it only sometimes is the worst of the three
// options: nothing downstream can distinguish a blueprint whose hash
// was skipped from one that never had a hash to begin with, so every
// bare ref becomes unfalsifiable.
func TestPlan_RefusesABlueprintItCannotHash(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	pkgDir := writeBlueprintPackage(t, dir, "platform")
	entry := writeBlueprintCallingStack(t, dir, pkgDir, `"widget1"`)

	// Make the blueprint unhashable by removing the marker that makes
	// its directory a blueprint root at all. The generated code still
	// tags every resource with the blueprint's own name, so the name is
	// pending and nothing can complete it.
	root := filepath.Dir(pkgDir)
	if err := os.Remove(filepath.Join(root, "Ubxfile")); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, env, "plan",
		"--from-code", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--timeout", "60s",
	)
	if err == nil {
		t.Fatalf("plan accepted a blueprint it could not hash, which is the defect:\n%s", out)
	}
	got := err.Error() + "\n" + out
	if !strings.Contains(got, "direct-call provenance") {
		t.Errorf("want the provenance refusal, got:\n%s", got)
	}
	// And nothing was written: a refused plan must not leave a plan file
	// carrying a bare-name ref for a later ship to accept.
	matches, _ := filepath.Glob(filepath.Join(ledgerDir, ".ubx", "plans", "*.json"))
	if len(matches) != 0 {
		t.Errorf("a refused plan left %d plan file(s) behind", len(matches))
	}
}
