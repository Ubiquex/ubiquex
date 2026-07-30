package intentprovider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

func TestDialogue_Transcript_NumbersTurnsInOrder(t *testing.T) {
	d := &Dialogue{
		Turns: []Turn{
			{Text: "We need a Postgres database for payments.", At: "2026-07-28T00:00:00Z"},
			{Text: "Make it multi-az.", At: "2026-07-28T00:01:00Z"},
		},
	}

	got := string(d.Transcript())

	if !strings.Contains(got, "[Turn 1]: We need a Postgres database for payments.") {
		t.Errorf("Transcript missing numbered turn 1:\n%s", got)
	}
	if !strings.Contains(got, "[Turn 2]: Make it multi-az.") {
		t.Errorf("Transcript missing numbered turn 2:\n%s", got)
	}
	if strings.Index(got, "[Turn 1]") > strings.Index(got, "[Turn 2]") {
		t.Error("turns are out of order in the transcript")
	}
}

func TestDialogue_Transcript_Empty(t *testing.T) {
	d := &Dialogue{}
	if got := d.Transcript(); string(got) != "" {
		t.Errorf("Transcript() with no turns = %q, want empty", got)
	}
}

// TestDialogue_TamperingChangesHash is
// docs/intent-provider-adversarial.md's own "dialogue tampering
// post-pin" row: the exact mechanism every other intent.sources kind in
// this codebase already relies on (content_hash tamper-evidence) applies
// unchanged to a dialogue -- no new code, just confirmed directly rather
// than assumed to generalize.
func TestDialogue_TamperingChangesHash(t *testing.T) {
	dlg := &Dialogue{
		SchemaVersion: 1,
		Stack:         "payments",
		Adapter:       "claude",
		Model:         "claude-opus-4-8",
		Turns:         []Turn{{Text: "Provision a small Postgres database.", At: "2026-07-28T00:00:00Z"}},
	}
	original, err := json.Marshal(dlg)
	if err != nil {
		t.Fatal(err)
	}
	pinnedHash := HashDocument(original)

	// A human (or an attacker) edits the already-pinned dialogue file on
	// disk after the fact -- e.g. quietly changing what the user
	// supposedly said.
	tampered := []byte(strings.Replace(string(original), "small", "enormous", 1))
	if string(tampered) == string(original) {
		t.Fatal("test setup bug: tampering didn't actually change anything")
	}

	rehash := HashDocument(tampered)
	if rehash == pinnedHash {
		t.Fatal("tampering the dialogue content did not change its hash -- tamper-evidence is broken")
	}
}

func TestPopulateSources_DialogueKind(t *testing.T) {
	draft := &resolver.IntentFile{Stack: "payments"}
	dlgHash := HashDocument([]byte(`{"schema_version":1}`))

	PopulateSources(draft, SourceKindDialogue, "dialogues/abc123.dlg.json", dlgHash, "claude", "claude-opus-4-8", []byte(validPaymentsDraft))

	if len(draft.Intent.Sources) != 2 {
		t.Fatalf("Sources = %+v, want exactly 2 entries", draft.Intent.Sources)
	}
	dlg := draft.Intent.Sources[0]
	if dlg.Kind != SourceKindDialogue || dlg.Ref != "dialogues/abc123.dlg.json" || dlg.ContentHash != dlgHash {
		t.Errorf("dialogue source = %+v, want kind=%q ref=%q hash=%q", dlg, SourceKindDialogue, "dialogues/abc123.dlg.json", dlgHash)
	}
}
