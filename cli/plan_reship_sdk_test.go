package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An SDK program could be resolved exactly once per stack (UBI-267).
//
// All three runtimes hardcode op "create", because a hermetic,
// describe-only program cannot read ledger state. The resolver checked
// that op against ledger presence, because for a hand-written file a
// wrong op is a catchable authoring mistake. Together they made a
// second plan of an unchanged program fail:
//
//	op "create" names an address the ledger already has -- use op "modify"
//
// advice the author of an SDK program cannot follow, since there is no
// op in their source to change. A code blueprint author never sees one
// at all.
//
// Found on a partially shipped stack, where it also meant the stack
// could not be completed by re-running the program that made it.

// writeReshipStack writes a Go SDK program declaring the named
// resources, so the same fixture can grow between plans the way a real
// stack does.
func writeReshipStack(t *testing.T, dir string, names ...string) string {
	t.Helper()
	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module reship-fixture\n\ngo 1.23\n\n" +
		"require github.com/ubiquex/ubx-sdk-go v0.0.0\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + blueprintCallSdkGoRoot(t) + "\n"
	if err := os.WriteFile(filepath.Join(stackDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	var body strings.Builder
	for _, n := range names {
		body.WriteString("\t\tsdk.Resource(\n")
		body.WriteString("\t\t\tsdk.ResourceBinding{WireType: \"fake_widget\", Fields: sdk.FieldMap{\"Name\": {WireName: \"name\"}}},\n")
		body.WriteString("\t\t\t\"" + n + "\",\n")
		body.WriteString("\t\t\tstruct{ Name string }{\"" + n + "-value\"},\n")
		body.WriteString("\t\t)\n")
	}

	entry := filepath.Join(stackDir, "main.go")
	src := "package main\n\n" +
		"import sdk \"github.com/ubiquex/ubx-sdk-go/runtime\"\n\n" +
		"func main() {\n" +
		"\tsdk.Main(sdk.Stack(\"payments\", func() {\n" +
		"\t\tsdk.Intent(sdk.IntentInfo{Summary: \"a stack planned more than once\"})\n" +
		body.String() +
		"\t}))\n" +
		"}\n"
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestPlan_SDKProgram_CanBeReplannedAfterShipping(t *testing.T) {
	requireHermeticSandbox(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	dir := t.TempDir()
	ledgerDir := t.TempDir()
	entry := writeReshipStack(t, dir, "queue")

	planOut, err := runUbx(t, env, "plan", "--from-code", entry,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan (first): %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	shipOut, err := runUbx(t, env, "ship", hash,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes")
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut)
	}

	// The same program, unchanged. This is what used to be refused.
	replanOut, err := runUbx(t, env, "plan", "--from-code", entry,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("re-planning an unchanged SDK program after shipping it: %v\noutput: %s", err, replanOut)
	}
	if strings.Contains(replanOut, "already has") {
		t.Fatalf("the re-plan was refused:\n%s", replanOut)
	}
	if !strings.Contains(replanOut, "~1") {
		t.Errorf("the already-shipped resource was not planned as a change:\n%s", replanOut)
	}
}

// The shape that prompted this: some resources shipped and one did not,
// so one document needs both ops and the program calls every one of
// them a create.
func TestPlan_SDKProgram_PartiallyShippedStackCompletes(t *testing.T) {
	requireHermeticSandbox(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	dir := t.TempDir()
	ledgerDir := t.TempDir()

	// The first resource ships, standing in for the DLQ that made it.
	first := writeReshipStack(t, dir, "dlq")
	planOut, err := runUbx(t, env, "plan", "--from-code", first,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan (dlq): %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)
	if shipOut, err := runUbx(t, env, "ship", hash,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes"); err != nil {
		t.Fatalf("ubx ship (dlq): %v\noutput: %s", err, shipOut)
	}

	// Now the whole program, including the resource that never shipped.
	// The author changed nothing about the DLQ and has no op to change.
	both := writeReshipStack(t, dir, "dlq", "queue")
	replanOut, err := runUbx(t, env, "plan", "--from-code", both,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("completing a partially shipped stack from its own program: %v\noutput: %s", err, replanOut)
	}
	// One create for what never shipped, one change for what did.
	if !strings.Contains(replanOut, "+1") || !strings.Contains(replanOut, "~1") {
		t.Fatalf("want a mixed +1 ~1 plan, got:\n%s", replanOut)
	}
	if !strings.Contains(replanOut, "fake_widget.queue create") {
		t.Errorf("the resource that never shipped was not planned as a create:\n%s", replanOut)
	}
}

// A hand-written intent file keeps exactly today's strictness, because
// there a human really did state an op and really can be wrong about it.
func TestPlan_HandWrittenIntent_KeepsTheExplicitOpCheck(t *testing.T) {
	requireHermeticSandbox(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	dir := t.TempDir()
	ledgerDir := t.TempDir()
	entry := writeReshipStack(t, dir, "queue")

	planOut, err := runUbx(t, env, "plan", "--from-code", entry,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)
	if shipOut, err := runUbx(t, env, "ship", hash,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes"); err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}

	intentPath := filepath.Join(dir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "hand-written, op stated by a person"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "queue", "op": "create",
				"config": map[string]interface{}{"name": "queue-value"}},
		},
	})

	out, err := runUbx(t, env, "plan", intentPath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err == nil {
		t.Fatalf("a hand-written op create against an existing address was accepted:\n%s", out)
	}
	if !strings.Contains(err.Error()+out, "already has") {
		t.Fatalf("refused for the wrong reason: %v\n%s", err, out)
	}
}

// A plan built from a program says what a change line does NOT mean.
// Every other modify in this tool comes from a document whose author
// supplied a full desired end-state, where an omission means remove, so
// without the line a reader has to infer the difference from an absence
// (UBI-267).
func TestPlan_SDKProgram_ReceiptSaysOmittedAttributesArePreserved(t *testing.T) {
	requireHermeticSandbox(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	dir := t.TempDir()
	ledgerDir := t.TempDir()
	entry := writeReshipStack(t, dir, "queue")

	planOut, err := runUbx(t, env, "plan", "--from-code", entry,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	// Nothing exists yet, so this plan is all creates and the note would
	// be answering a question nobody asked.
	if strings.Contains(planOut, "preserved, not removed") {
		t.Errorf("a create-only plan carries the modify note:\n%s", planOut)
	}

	hash := mustExtractPlanHash(t, ledgerDir, planOut)
	if shipOut, err := runUbx(t, env, "ship", hash,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes"); err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}

	replanOut, err := runUbx(t, env, "plan", "--from-code", entry,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan (re-plan): %v\noutput: %s", err, replanOut)
	}
	if !strings.Contains(replanOut, "preserved, not removed") {
		t.Errorf("a plan containing an inferred modify does not say omitted attributes are preserved:\n%s", replanOut)
	}
}

// A hand-written intent file gets no such note, because there an
// omission really does mean remove.
func TestPlan_HandWrittenIntent_HasNoPreservedNote(t *testing.T) {
	requireHermeticSandbox(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	dir := t.TempDir()
	ledgerDir := t.TempDir()
	entry := writeReshipStack(t, dir, "queue")

	planOut, err := runUbx(t, env, "plan", "--from-code", entry,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)
	if shipOut, err := runUbx(t, env, "ship", hash,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes"); err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}

	intentPath := filepath.Join(dir, "modify.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "hand-written modify"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "queue", "op": "modify",
				"config": map[string]interface{}{"name": "queue-changed"}},
		},
	})

	out, err := runUbx(t, env, "plan", intentPath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan (hand-written modify): %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "preserved, not removed") {
		t.Errorf("a hand-written modify carries the generated-document note:\n%s", out)
	}
}
