package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

func renderDivergence(t *testing.T, found []divergedResource) string {
	t.Helper()
	var buf bytes.Buffer
	writeDivergenceNote(&buf, &styler{}, found)
	return buf.String()
}

// TestWriteDivergenceNote_MakesTheWeakerClaim is the decision this whole
// mechanism turns on, so it is asserted rather than left to review.
//
// "This would undo your restore" cannot be said safely: a plan touching
// one attribute of a diverged resource is not necessarily reversing the
// divergence. The claim made instead is always true, and the conclusion
// stays with the reader.
func TestWriteDivergenceNote_MakesTheWeakerClaim(t *testing.T) {
	out := renderDivergence(t, []divergedResource{{
		Address: core.Address{Stack: "payments", Type: "aws_sqs_queue", Name: "orders"},
		Divergence: core.Divergence{
			Kind:       core.DivergenceRestore,
			TargetHead: "5f22981fdfac53b58e3382843645da5ece2987977947dc645459b40627f58b5a",
			ResolvedAt: "2026-09-15T22:43:40Z",
		},
	}})

	for _, want := range []string{
		"last changed deliberately",     // the fact
		"payments.aws_sqs_queue.orders", // which resource
		"restore to head",               // which kind
		"2026-09-15T22:43:40Z",          // and when
		"If that is what you want",      // the conclusion is the reader's
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	// The claims it must not make.
	for _, refuse := range []string{"undo", "revert your", "will restore", "mistake", "warning:"} {
		if strings.Contains(strings.ToLower(out), refuse) {
			t.Errorf("output makes the stronger claim %q, which is not always true:\n%s", refuse, out)
		}
	}
}

// TestWriteDivergenceNote_NamesDriftRevertToo: restore is one instance of
// the concept, not the concept. If this only ever said "restore", the
// mechanism would have to be built again the first time someone noticed
// drift_revert has the identical problem.
func TestWriteDivergenceNote_NamesDriftRevertToo(t *testing.T) {
	out := renderDivergence(t, []divergedResource{{
		Address:    core.Address{Stack: "payments", Type: "fake_widget", Name: "a"},
		Divergence: core.Divergence{Kind: core.DivergenceDriftRevert, ResolvedAt: "2026-09-14T10:02:00Z"},
	}})
	if !strings.Contains(out, "drift revert") {
		t.Errorf("a drift revert is a deliberate divergence too:\n%s", out)
	}
	if strings.Contains(out, "restore") {
		t.Errorf("a drift revert must not be described as a restore:\n%s", out)
	}
}

// TestWriteDivergenceNote_SilentWhenNothingDiverged: the overwhelmingly
// common plan touches nothing that was placed deliberately, and a section
// explaining that would be noise on every run forever.
func TestWriteDivergenceNote_SilentWhenNothingDiverged(t *testing.T) {
	if out := renderDivergence(t, nil); out != "" {
		t.Errorf("an ordinary plan got a divergence section:\n%s", out)
	}
}

// TestPlanTouches_ExcludesCreates: an address a plan CREATES holds
// nothing yet, so nothing can have placed its current state deliberately.
// Reporting one would be a claim about a resource that does not exist.
func TestPlanTouches_ExcludesCreates(t *testing.T) {
	p := &core.Proposal{Delta: core.Delta{
		Modifies: []core.Modification{{
			Target: core.Address{Stack: "s", Type: "t", Name: "m"},
		}},
		Destroys: []core.DestroyEntry{{
			Address: core.Address{Stack: "s", Type: "t", Name: "d"},
		}},
	}}
	got := planTouches(p)
	if len(got) != 2 {
		t.Fatalf("planTouches = %v, want the modify and the destroy only", got)
	}
	// A destroy specifically: destroying something a restore put back is
	// where a silent undo costs the most.
	var sawDestroy bool
	for _, a := range got {
		if a.Name == "d" {
			sawDestroy = true
		}
	}
	if !sawDestroy {
		t.Errorf("destroys must be checked: %v", got)
	}
}

// TestPlan_SaysWhenItWouldChangeSomethingSetDeliberately is the whole
// mechanism end to end through the real commands.
//
// Restore a stack to an earlier head, ship it, then run an ordinary plan
// from the declaration that still describes the newer shape. The plan
// correctly proposes changing it back, and now says the state it is about
// to change was put there on purpose.
func TestPlan_SaysWhenItWouldChangeSomethingSetDeliberately(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// batch1 is the head worth going back to; batch2 moves a on.
	targetHead := resolveAcceptShip(t, dir, ledgerDir, env, restoreInitialIntent, "batch1")
	resolveAcceptShip(t, dir, ledgerDir, env, restoreDestroyAIntent, "batch2-destroy-a", "--confirm-destroys")
	resolveAcceptShip(t, dir, ledgerDir, env, restoreRecreateAIntent, "batch2-recreate-a")

	// Restore, then actually ship it, so the divergence is what the
	// resource currently holds rather than a pending idea.
	restoreOut, err := runUbx(t, env, "restore", targetHead, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx restore: %v\noutput: %s", err, restoreOut)
	}
	restoreID := extractProposalHash(t, restoreOut)
	planPath := filepath.Join(ledgerDir, ".ubx", "plans", restoreID+".json")
	acceptOut, err := runUbx(t, env, "accept", planPath, "--ledger-dir", ledgerDir, "--confirm-destroys")
	if err != nil {
		t.Fatalf("ubx accept restore: %v\noutput: %s", err, acceptOut)
	}
	acceptedID := mustExtractID(t, acceptOut)
	if shipOut, err := runUbx(t, env, "ship", acceptedID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx ship restore: %v\noutput: %s", err, shipOut)
	}

	// Now the ordinary plan someone runs next, from the declaration that
	// still describes the newer shape.
	// The declaration still describes the newer shape, as a modify now
	// that the restore has put the resource back.
	const nextPlanIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "the declaration, unchanged"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "modify", "config": {"name": "widget-a-v2"}},
    {"type": "fake_widget", "name": "b", "op": "modify", "config": {"name": "widget-b"}}
  ]
}`
	intentPath := filepath.Join(dir, "next-plan.json")
	if err := os.WriteFile(intentPath, []byte(nextPlanIntent), 0o644); err != nil {
		t.Fatal(err)
	}
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}

	for _, want := range []string{
		"last changed deliberately",
		"restore to head",
		"If that is what you want",
	} {
		if !strings.Contains(planOut, want) {
			t.Fatalf("the plan does not say it would change something set deliberately:\n%s", planOut)
		}
	}
}

// TestWriteDivergenceNote_ReadsCorrectlyForOneAndMany: the singular case
// is the common one, and "1 resource were last changed" reads as a bug in
// the tool at exactly the moment a reader should be thinking about their
// infrastructure instead.
func TestWriteDivergenceNote_ReadsCorrectlyForOneAndMany(t *testing.T) {
	one := divergedResource{
		Address:    core.Address{Stack: "s", Type: "t", Name: "a"},
		Divergence: core.Divergence{Kind: core.DivergenceDriftRevert},
	}
	two := divergedResource{
		Address:    core.Address{Stack: "s", Type: "t", Name: "b"},
		Divergence: core.Divergence{Kind: core.DivergenceDriftRevert},
	}

	single := renderDivergence(t, []divergedResource{one})
	for _, want := range []string{"1 resource here was last changed", "changes it again", "the declaration it is compared against"} {
		if !strings.Contains(single, want) {
			t.Errorf("singular output does not read correctly, missing %q:\n%s", want, single)
		}
	}

	plural := renderDivergence(t, []divergedResource{one, two})
	for _, want := range []string{"2 resources here were last changed", "changes them again", "the declaration they are compared against"} {
		if !strings.Contains(plural, want) {
			t.Errorf("plural output does not read correctly, missing %q:\n%s", want, plural)
		}
	}
}

// TestRenderPlanReceipt_DivergenceNoteSitsWithTheResources pins where
// the note goes, which is a correctness question rather than a taste one.
//
// It first rendered after the totals, between the summary and the
// next-step line. That put a statement about ONE address below the counts
// for all of them, at an indent level nothing else used there, so it read
// as a footnote about the plan rather than a fact about a resource in it.
//
// The receipt's order is resources, then totals, then next. A claim about
// a particular address belongs in the first section.
func TestRenderPlanReceipt_DivergenceNoteSitsWithTheResources(t *testing.T) {
	p := &core.Proposal{
		Stack: "payments",
		Delta: core.Delta{Modifies: []core.Modification{{
			Target: core.Address{Stack: "payments", Type: "fake_widget", Name: "a"},
			After:  map[string]json.RawMessage{"name": json.RawMessage(`"v2"`)},
		}}},
		BlastRadius: core.BlastRadius{Modifies: 1},
	}
	var buf bytes.Buffer
	renderPlanReceipt(&buf, &styler{}, p, "Plan  payments", true, func(w io.Writer) {
		writeDivergenceNote(w, &styler{}, []divergedResource{{
			Address:    core.Address{Stack: "payments", Type: "fake_widget", Name: "a"},
			Divergence: core.Divergence{Kind: core.DivergenceRestore, TargetHead: "abc123"},
		}})
	})

	out := buf.String()
	resource := strings.Index(out, "fake_widget.a change")
	note := strings.Index(out, "last changed deliberately")
	totals := strings.Index(out, "delta:")
	for name, i := range map[string]int{"resource": resource, "note": note, "totals": totals} {
		if i < 0 {
			t.Fatalf("%s is missing from the receipt:\n%s", name, out)
		}
	}
	if !(resource < note && note < totals) {
		t.Errorf("the note is not between the resources and the totals:\n%s", out)
	}
}

// TestRenderPlanReceipt_NoBeforeBlockIsFine: the three other callers pass
// nothing, and a receipt without one must render exactly as it did.
func TestRenderPlanReceipt_NoBeforeBlockIsFine(t *testing.T) {
	p := &core.Proposal{Stack: "payments", Delta: core.Delta{Modifies: []core.Modification{{
		Target: core.Address{Stack: "payments", Type: "fake_widget", Name: "a"},
	}}}}
	var with, without bytes.Buffer
	renderPlanReceipt(&with, &styler{}, p, "Plan  payments", true, func(w io.Writer) {})
	renderPlanReceipt(&without, &styler{}, p, "Plan  payments", true, nil)
	if with.String() != without.String() {
		t.Errorf("an empty before-block changed the receipt:\n%s\nvs\n%s", with.String(), without.String())
	}
}
