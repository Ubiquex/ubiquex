package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
)

// TestStalePlanError_SaysWhatHappenedAndWhatToDo covers UBI-262.
//
// The refusal itself is correct and load-bearing: a plan records the
// ledger head it was resolved against, and appending it onto a
// different head would break the chain. What was wrong was only what
// the person was told. The raw error named the mechanism ("accept:
// append: proposal parent does not match ledger head"), printed an
// empty string for a plan built against an empty ledger, printed a
// full 64-character head, and never mentioned `ubx plan`.
func TestStalePlanError_SaysWhatHappenedAndWhatToDo(t *testing.T) {
	dir := t.TempDir()
	ledger := core.Open(dir)

	// A plan resolved against an empty ledger, with the ledger since
	// moved. This is the shape a first plan takes.
	err := staleplanError(ledger, &core.Proposal{Parent: ""})
	msg := err.Error()
	for _, want := range []string{"an empty ledger", "ubx plan", "no longer the head"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "append:") || strings.Contains(msg, "parent does not match") {
		t.Errorf("the mechanism leaked into a user-facing error:\n%s", msg)
	}

	// A plan resolved against a real head. Both hashes are truncated,
	// because a 64-character hash is not something a reader can act on.
	const oldHead = "640f7f8744ecb83826a8167fb5e3027db15b7c309c042a226ff5b47fbc4ea1c3"
	msg = staleplanError(ledger, &core.Proposal{Parent: oldHead}).Error()
	if !strings.Contains(msg, "640f7f8744ec…") {
		t.Errorf("the plan's own head should be shown, truncated:\n%s", msg)
	}
	if strings.Contains(msg, oldHead) {
		t.Errorf("a full 64-character hash reached a user-facing message:\n%s", msg)
	}
}

// The error has to keep pointing at the fix as the fix, so a rename of
// the command would fail here rather than silently leaving advice that
// no longer works.
func TestStalePlanError_NamesTheCommandThatFixesIt(t *testing.T) {
	ledger := core.Open(t.TempDir())
	msg := staleplanError(ledger, &core.Proposal{Parent: ""}).Error()
	if !strings.Contains(msg, "Re-run `ubx plan`") {
		t.Errorf("the one command that fixes this has to be named:\n%s", msg)
	}
}

// TestConfirmAndAccept_StalePlanRefusedBeforeAnythingRenders covers
// UBI-263's one real sharp edge.
//
// core.Accept's own parent check is the authority and is unchanged, but
// it runs at the very end: after the ship header, after the orphan
// check, and after a human has been asked to type "yes". A plan that
// could not possibly ship still produced the full confirmation
// ceremony, so a person could answer the question before being told the
// answer could not be acted on.
//
// Asking the same question first costs one Head() read.
func TestConfirmAndAccept_StalePlanRefusedBeforeAnythingRenders(t *testing.T) {
	dir := t.TempDir()
	ledger := core.Open(dir)

	// A ledger with a real head, and a plan that predates it.
	seed := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "payments",
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "the ledger moved"},
		BlastRadius:   core.BlastRadius{Creates: 1},
		Delta:         core.Delta{Creates: []json.RawMessage{json.RawMessage(`{"type":"fake_widget","name":"seed","op":"create","config":{"name":"seed"}}`)}},
	}
	if _, err := core.Accept(ledger, seed); err != nil {
		t.Fatalf("seed the ledger: %v", err)
	}

	stale := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "payments",
		Kind:          core.KindChange,
		Parent:        "", // resolved against the empty ledger, before seed
		Intent:        core.Intent{Summary: "a plan that cannot ship"},
		BlastRadius:   core.BlastRadius{Creates: 1},
		Delta:         core.Delta{Creates: []json.RawMessage{json.RawMessage(`{"type":"fake_widget","name":"one","op":"create","config":{"name":"one"}}`)}},
	}

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	// No --yes and no TTY: if the refusal did NOT come first, this would
	// fail on the confirmation instead, which is the tell.
	_, err := confirmAndAccept(cmd, ledger, newStyler(cmd), stale, false)
	if err == nil {
		t.Fatal("want a refusal for a plan the ledger has moved past")
	}
	if !strings.Contains(err.Error(), "no longer the head") {
		t.Errorf("want the stale-plan refusal, got: %v", err)
	}
	if strings.Contains(err.Error(), "interactive terminal") {
		t.Error("the confirmation ran before the staleness check -- the refusal has to come first")
	}
	if out.Len() != 0 {
		t.Errorf("nothing should render for a plan that cannot ship, got:\n%s", out.String())
	}
}
