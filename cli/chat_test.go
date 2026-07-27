package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/intentprovider"
)

// chatFakeAdapter is a hermetic, fully deterministic fake -- no network,
// no real LLM. responses is consumed one per successful Draft call;
// failFirstCall, if set, makes the very first call return an error
// instead of consuming a response (docs/intent-provider-adversarial.md's
// own "a single bad turn doesn't lose the conversation" requirement).
type chatFakeAdapter struct {
	responses     []string
	failFirstCall bool
	calls         []intentprovider.DraftRequest
}

func (a *chatFakeAdapter) Name() string  { return "fake" }
func (a *chatFakeAdapter) Model() string { return "fake-model-v1" }

func (a *chatFakeAdapter) Draft(_ context.Context, req intentprovider.DraftRequest) (json.RawMessage, error) {
	a.calls = append(a.calls, req)
	callIdx := len(a.calls) - 1
	if a.failFirstCall && callIdx == 0 {
		return nil, errors.New("scripted adapter failure")
	}
	respIdx := callIdx
	if a.failFirstCall {
		respIdx--
	}
	if respIdx >= len(a.responses) {
		respIdx = len(a.responses) - 1
	}
	return json.RawMessage(a.responses[respIdx]), nil
}

func runUbxWithStdin(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func dialoguesDirFiles(t *testing.T, ledgerDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(ledgerDir, "dialogues"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read dialogues dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestChat_RequiresStack(t *testing.T) {
	withBuildIntentAdapter(t, &chatFakeAdapter{responses: []string{proposeFromDocGoodDraft}})
	_, err := runUbxWithStdin(t, "", "chat")
	if err == nil {
		t.Fatal("expected an error when --stack is omitted")
	}
	if !strings.Contains(err.Error(), "--stack is required") {
		t.Errorf("error = %v, want it to name --stack", err)
	}
}

// TestChat_AbandonedSession_EOF_NoFileWritten is
// docs/intent-provider-adversarial.md's own "abandoned session" row: a
// plain EOF (stdin closed, no /save) must leave zero orphan files behind.
func TestChat_AbandonedSession_EOF_NoFileWritten(t *testing.T) {
	withBuildIntentAdapter(t, &chatFakeAdapter{responses: []string{proposeFromDocGoodDraft}})
	ledgerDir := t.TempDir()

	out, err := runUbxWithStdin(t, "We need a Postgres database for payments.\n", "chat", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx chat: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "session abandoned, nothing saved") {
		t.Errorf("output missing the abandon message:\n%s", out)
	}
	if files := dialoguesDirFiles(t, ledgerDir); len(files) != 0 {
		t.Errorf("dialogues/ has %v, want empty -- an abandoned session must never write an orphan file", files)
	}
}

// TestChat_QuitCommand_NoFileWritten covers the explicit /quit path,
// distinct from plain EOF -- same required outcome either way.
func TestChat_QuitCommand_NoFileWritten(t *testing.T) {
	withBuildIntentAdapter(t, &chatFakeAdapter{responses: []string{proposeFromDocGoodDraft}})
	ledgerDir := t.TempDir()

	out, err := runUbxWithStdin(t, "We need a Postgres database for payments.\n/quit\n", "chat", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx chat: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "session abandoned, nothing saved") {
		t.Errorf("output missing the abandon message:\n%s", out)
	}
	if files := dialoguesDirFiles(t, ledgerDir); len(files) != 0 {
		t.Errorf("dialogues/ has %v, want empty after /quit", files)
	}
}

func TestChat_Save_WritesDialogueAndDraftWithProvenance(t *testing.T) {
	fake := &chatFakeAdapter{responses: []string{proposeFromDocGoodDraft}}
	withBuildIntentAdapter(t, fake)
	ledgerDir := t.TempDir()

	out, err := runUbxWithStdin(t, "We need a Postgres database for payments.\n/save\n", "chat", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx chat: %v\noutput: %s", err, out)
	}

	files := dialoguesDirFiles(t, ledgerDir)
	if len(files) != 1 || !strings.HasSuffix(files[0], ".dlg.json") {
		t.Fatalf("dialogues/ = %v, want exactly one *.dlg.json file", files)
	}

	dlgPath := filepath.Join(ledgerDir, "dialogues", files[0])
	data, err := os.ReadFile(dlgPath)
	if err != nil {
		t.Fatal(err)
	}
	var dlg intentprovider.Dialogue
	if err := json.Unmarshal(data, &dlg); err != nil {
		t.Fatalf("captured dialogue is not valid JSON: %v", err)
	}
	if len(dlg.Turns) != 1 || dlg.Turns[0].Text != "We need a Postgres database for payments." {
		t.Errorf("Turns = %+v, want exactly the one real turn", dlg.Turns)
	}
	if dlg.Adapter != "fake" || dlg.Model != "fake-model-v1" {
		t.Errorf("Adapter/Model = %q/%q, want fake/fake-model-v1", dlg.Adapter, dlg.Model)
	}
	if dlg.Draft == nil || len(dlg.Draft.Intent.Sources) != 0 {
		t.Errorf("embedded Draft.Intent.Sources = %+v, want empty -- the embedded draft must be the PRE-provenance version", dlg.Draft)
	}

	// The emitted draft (stdout) must carry dialogue + intent_provider
	// provenance, and the dialogue source's own ref/hash must point at
	// exactly the file just written.
	if !strings.Contains(out, `"kind": "dialogue"`) {
		t.Errorf("stdout draft missing a dialogue source:\n%s", out)
	}
	if !strings.Contains(out, `"ref": "dialogues/`+files[0][:len(files[0])-len(".dlg.json")]) && !strings.Contains(out, `"ref": "dialogues/`+files[0]) {
		t.Errorf("stdout draft's dialogue source ref doesn't reference the written file %q:\n%s", files[0], out)
	}
	if !strings.Contains(out, `"kind": "intent_provider"`) || !strings.Contains(out, `"ref": "fake:fake-model-v1"`) {
		t.Errorf("stdout draft missing the intent_provider source:\n%s", out)
	}
}

func TestChat_Save_WithNoTurns_Errors(t *testing.T) {
	withBuildIntentAdapter(t, &chatFakeAdapter{responses: []string{proposeFromDocGoodDraft}})
	ledgerDir := t.TempDir()

	_, err := runUbxWithStdin(t, "/save\n", "chat", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err == nil {
		t.Fatal("expected an error saving with no successfully drafted turn")
	}
	if !strings.Contains(err.Error(), "nothing to save") {
		t.Errorf("error = %v, want it to say there's nothing to save", err)
	}
	if files := dialoguesDirFiles(t, ledgerDir); len(files) != 0 {
		t.Errorf("dialogues/ has %v, want empty", files)
	}
}

// TestChat_MultiTurn_RedraftsWithFullAccumulatedTranscript confirms each
// new turn's own redraft call carries every PRIOR turn too, not just the
// newest one -- "each turn re-drafts via the same adapter" means a full
// re-draft of the whole conversation, never an incremental patch.
func TestChat_MultiTurn_RedraftsWithFullAccumulatedTranscript(t *testing.T) {
	fake := &chatFakeAdapter{responses: []string{proposeFromDocGoodDraft, proposeFromDocGoodDraft}}
	withBuildIntentAdapter(t, fake)
	ledgerDir := t.TempDir()

	stdin := "We need a Postgres database for payments, like staging but smaller.\nMake it multi-az.\n/save\n"
	_, err := runUbxWithStdin(t, stdin, "chat", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx chat: %v", err)
	}

	if len(fake.calls) != 2 {
		t.Fatalf("adapter called %d times, want 2 (one per turn)", len(fake.calls))
	}
	secondCallContent := string(fake.calls[1].Content)
	if !strings.Contains(secondCallContent, "[Turn 1]:") || !strings.Contains(secondCallContent, "like staging but smaller") {
		t.Errorf("second turn's own Content is missing turn 1 entirely -- not a full re-draft:\n%s", secondCallContent)
	}
	if !strings.Contains(secondCallContent, "[Turn 2]:") || !strings.Contains(secondCallContent, "Make it multi-az") {
		t.Errorf("second turn's own Content is missing turn 2 itself:\n%s", secondCallContent)
	}
}

func TestChat_RedactsSecretMidConversation(t *testing.T) {
	fake := &chatFakeAdapter{responses: []string{proposeFromDocGoodDraft, proposeFromDocGoodDraft}}
	withBuildIntentAdapter(t, fake)
	ledgerDir := t.TempDir()

	stdin := "We need a Postgres database for payments.\nOur deploy key is AKIAIOSFODNN7EXAMPLE, keep that safe.\n/save\n"
	out, err := runUbxWithStdin(t, stdin, "chat", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx chat: %v\noutput: %s", err, out)
	}

	for i, call := range fake.calls {
		if strings.Contains(string(call.Content), "AKIAIOSFODNN7EXAMPLE") {
			t.Fatalf("call %d received the raw secret -- redaction did not run before this turn reached the adapter: %s", i, call.Content)
		}
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "aws-access-key-id") {
		t.Errorf("expected a warning naming the redacted pattern, got:\n%s", out)
	}

	files := dialoguesDirFiles(t, ledgerDir)
	if len(files) != 1 {
		t.Fatalf("dialogues/ = %v, want exactly one file", files)
	}
	data, err := os.ReadFile(filepath.Join(ledgerDir, "dialogues", files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatal("the captured dialogue file itself contains the raw secret -- redaction at capture failed")
	}
	if !strings.Contains(string(data), "[REDACTED:aws-access-key-id]") {
		t.Error("captured dialogue is missing the redaction placeholder")
	}
}

// changeProposalWithDialogueSource is a minimal, valid change proposal
// (per core.Validate) whose own intent.sources names a dialogue file --
// TestWhy_Dialogue_WalksChangeToProposalToConversation's own fixture for
// UBI-46's "why walking change -> proposal -> the actual conversation"
// requirement.
const changeProposalWithDialogueSourceTemplate = `{
  "schema_version": 1,
  "stack": "payments",
  "parent": "",
  "kind": "change",
  "intent": {
    "summary": "provision the payments database, refined over a real conversation",
    "sources": [
      {"kind": "dialogue", "ref": "dialogues/%s.dlg.json", "content_hash": "sha256:%s"}
    ]
  },
  "delta": {"creates": [{"stack": "payments", "type": "aws_db_instance", "name": "payments-db"}]},
  "resolution": {"resolved_at": "2026-07-28T00:00:00Z"},
  "cost_delta": {},
  "blast_radius": {"creates": 1, "modifies": 0, "destroys": 0},
  "status": "draft"
}`

func TestWhy_Dialogue_WalksChangeToProposalToConversation(t *testing.T) {
	ledgerDir := t.TempDir()

	dlg := intentprovider.Dialogue{
		SchemaVersion: 1,
		Stack:         "payments",
		Adapter:       "claude",
		Model:         "claude-opus-4-8",
		StartedAt:     "2026-07-28T00:00:00Z",
		Turns: []intentprovider.Turn{
			{Text: "We need a Postgres database for payments, like staging but smaller.", At: "2026-07-28T00:00:01Z"},
			{Text: "Make it multi-az.", At: "2026-07-28T00:01:00Z"},
		},
	}
	dlgData, err := json.Marshal(&dlg)
	if err != nil {
		t.Fatal(err)
	}
	hash := intentprovider.HashDocument(dlgData)
	hashHex := strings.TrimPrefix(hash, "sha256:")

	if err := os.MkdirAll(filepath.Join(ledgerDir, "dialogues"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ledgerDir, "dialogues", hashHex+".dlg.json"), dlgData, 0o644); err != nil {
		t.Fatal(err)
	}

	proposalPath := filepath.Join(t.TempDir(), "change.json")
	writeFile(t, proposalPath, fmt.Sprintf(changeProposalWithDialogueSourceTemplate, hashHex, hashHex))

	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	id := mustExtractID(t, acceptOut)

	// Without --dialogue: the source line is present (renderIntentSource,
	// unchanged), but not the actual conversation content.
	plainOut, err := runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v", err)
	}
	if strings.Contains(plainOut, "Make it multi-az.") {
		t.Error("the real conversation text rendered WITHOUT --dialogue -- it must be opt-in")
	}
	if !strings.Contains(plainOut, "source: dialogue dialogues/"+hashHex+".dlg.json") {
		t.Errorf("expected the ordinary dialogue source line even without --dialogue, got: %s", plainOut)
	}

	// With --dialogue: change proposal -> the draft it came from (the
	// dialogue's own embedded Draft, not rendered directly here, but
	// implicitly what intent.summary already reflects) -> the real,
	// verbatim conversation.
	dialogueOut, err := runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir, "--dialogue")
	if err != nil {
		t.Fatalf("ubx why --dialogue: %v\noutput: %s", err, dialogueOut)
	}
	for _, want := range []string{
		"conversation (claude / claude-opus-4-8, started 2026-07-28T00:00:00Z)",
		"[Turn 1, 2026-07-28T00:00:01Z]: We need a Postgres database for payments, like staging but smaller.",
		"[Turn 2, 2026-07-28T00:01:00Z]: Make it multi-az.",
	} {
		if !strings.Contains(dialogueOut, want) {
			t.Errorf("ubx why --dialogue output missing %q, got:\n%s", want, dialogueOut)
		}
	}
}

func TestWhy_Dialogue_NoSourcePrintsAnExplicitNote(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "draft.json")
	writeFile(t, proposalPath, draftProposalJSON)

	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	id := mustExtractID(t, acceptOut)

	out, err := runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir, "--dialogue")
	if err != nil {
		t.Fatalf("ubx why --dialogue: %v", err)
	}
	if !strings.Contains(out, "no captured dialogue source") {
		t.Errorf("expected an explicit note when there's no dialogue source, got: %s", out)
	}
}

// TestChat_TurnFailureContinuesSession is
// docs/intent-provider-adversarial.md's own required outcome: a single
// bad turn (an adapter error) doesn't lose the conversation -- the next
// turn can still succeed and /save still works.
func TestChat_TurnFailureContinuesSession(t *testing.T) {
	fake := &chatFakeAdapter{responses: []string{proposeFromDocGoodDraft}, failFirstCall: true}
	withBuildIntentAdapter(t, fake)
	ledgerDir := t.TempDir()

	stdin := "This turn fails.\nWe need a Postgres database for payments.\n/save\n"
	out, err := runUbxWithStdin(t, stdin, "chat", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx chat: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "turn failed to draft") {
		t.Errorf("output missing the per-turn failure warning:\n%s", out)
	}

	files := dialoguesDirFiles(t, ledgerDir)
	if len(files) != 1 {
		t.Fatalf("dialogues/ = %v, want exactly one file after a successful /save", files)
	}
	data, _ := os.ReadFile(filepath.Join(ledgerDir, "dialogues", files[0]))
	var dlg intentprovider.Dialogue
	if err := json.Unmarshal(data, &dlg); err != nil {
		t.Fatal(err)
	}
	if len(dlg.Turns) != 2 {
		t.Errorf("Turns = %+v, want both turns present (the failed one is still something the user really said)", dlg.Turns)
	}
}
