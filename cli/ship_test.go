package cli

import (
	"strings"
	"testing"

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
