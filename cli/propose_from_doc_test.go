package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/intentprovider"
)

// fakeIntentAdapter is a hermetic, fully deterministic fake -- no
// network, no real LLM -- for `ubx propose --from-doc`'s own CLI-level
// tests. withBuildIntentAdapter swaps the package-level buildIntentAdapter
// seam (cli/intentadapter.go) for the test's own duration.
type fakeIntentAdapter struct {
	draft   string
	lastReq intentprovider.DraftRequest
	err     error
}

func (a *fakeIntentAdapter) Name() string  { return "fake" }
func (a *fakeIntentAdapter) Model() string { return "fake-model-v1" }
func (a *fakeIntentAdapter) Draft(_ context.Context, req intentprovider.DraftRequest) (json.RawMessage, error) {
	a.lastReq = req
	if a.err != nil {
		return nil, a.err
	}
	return json.RawMessage(a.draft), nil
}

func withBuildIntentAdapter(t *testing.T, a intentprovider.Adapter) {
	t.Helper()
	orig := buildIntentAdapter
	buildIntentAdapter = func(*Config) (intentprovider.Adapter, error) { return a, nil }
	t.Cleanup(func() { buildIntentAdapter = orig })
}

const proposeFromDocGoodDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {
    "summary": "provision a database for payments, modeled on staging, smaller",
    "assumptions": [
      {"text": "chose db.t3.small, one tier below staging's db.t3.medium", "affects": ["aws_db_instance.payments-db.instance_class"]}
    ],
    "defaults": [
      {"text": "set engine to postgres per the explicit requirement", "affects": ["aws_db_instance.payments-db.engine"]}
    ],
    "questions": [
      {"text": "unclear whether multi-AZ is required", "affects": ["aws_db_instance.payments-db.multi_az"], "blocking": true}
    ]
  },
  "resources": [
    {"type": "aws_db_instance", "name": "payments-db", "op": "create", "config": "{\"instance_class\":\"db.t3.small\"}"}
  ],
  "destroys": []
}`

func TestProposeFromDoc_WritesDraftAndRendersAmbiguity(t *testing.T) {
	fake := &fakeIntentAdapter{draft: proposeFromDocGoodDraft}
	withBuildIntentAdapter(t, fake)

	docPath := filepath.Join(t.TempDir(), "payments.md")
	writeFile(t, docPath, "# Payments\n\nLike staging but smaller.")

	out, err := runUbx(t, nil, "propose", "--from-doc", docPath, "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx propose --from-doc: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "Assumptions (1):") {
		t.Errorf("output missing rendered Assumptions section:\n%s", out)
	}
	if !strings.Contains(out, "chose db.t3.small") {
		t.Errorf("output missing the actual assumption text:\n%s", out)
	}
	if !strings.Contains(out, "Defaults (1):") {
		t.Errorf("output missing rendered Defaults section:\n%s", out)
	}
	if !strings.Contains(out, "Questions (1):") || !strings.Contains(out, "[blocking") {
		t.Errorf("output missing rendered blocking Question:\n%s", out)
	}

	if !strings.Contains(out, `"schema_version": 1`) {
		t.Errorf("output missing the raw JSON draft:\n%s", out)
	}
	if !strings.Contains(out, `"kind": "document"`) {
		t.Errorf("output missing the populated document source:\n%s", out)
	}
	if !strings.Contains(out, `"kind": "intent_provider"`) {
		t.Errorf("output missing the populated intent_provider source:\n%s", out)
	}
	if !strings.Contains(out, `"ref": "fake:fake-model-v1"`) {
		t.Errorf("output missing the correct adapter:model ref:\n%s", out)
	}

	if !strings.Contains(fake.lastReq.Stack, "payments") {
		t.Errorf("adapter received Stack=%q, want \"payments\"", fake.lastReq.Stack)
	}
}

func TestProposeFromDoc_WritesToFileWithOut(t *testing.T) {
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: proposeFromDocGoodDraft})

	dir := t.TempDir()
	docPath := filepath.Join(dir, "payments.md")
	outPath := filepath.Join(dir, "payments.intent.json")
	writeFile(t, docPath, "# Payments\n\nLike staging but smaller.")

	_, err := runUbx(t, nil, "propose", "--from-doc", docPath, "--stack", "payments", "--out", outPath)
	if err != nil {
		t.Fatalf("ubx propose --from-doc: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("expected the draft to be written to %s: %v", outPath, err)
	}
	if !strings.Contains(string(data), `"stack": "payments"`) {
		t.Errorf("written draft missing expected content: %s", data)
	}
}

func TestProposeFromDoc_RequiresStack(t *testing.T) {
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: proposeFromDocGoodDraft})

	docPath := filepath.Join(t.TempDir(), "payments.md")
	writeFile(t, docPath, "doc")

	_, err := runUbx(t, nil, "propose", "--from-doc", docPath)
	if err == nil {
		t.Fatal("expected an error when --stack is omitted")
	}
	if !strings.Contains(err.Error(), "--stack is required") {
		t.Errorf("error = %v, want it to name --stack", err)
	}
}

func TestProposeFromDoc_RejectsFromDocWithPositionalArg(t *testing.T) {
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: proposeFromDocGoodDraft})

	docPath := filepath.Join(t.TempDir(), "payments.md")
	writeFile(t, docPath, "doc")

	_, err := runUbx(t, nil, "propose", "--from-doc", docPath, "--stack", "payments", "some-proposal.json")
	if err == nil {
		t.Fatal("expected an error when both --from-doc and a positional argument are given")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %v, want it to name the mutual exclusivity", err)
	}
}

func TestProposeFromDoc_RedactsSecretMaterialBeforeSendingToAdapter(t *testing.T) {
	fake := &fakeIntentAdapter{draft: proposeFromDocGoodDraft}
	withBuildIntentAdapter(t, fake)

	docPath := filepath.Join(t.TempDir(), "payments.md")
	writeFile(t, docPath, "# Payments\n\nUse this key: AKIAIOSFODNN7EXAMPLE to configure access.")

	out, err := runUbx(t, nil, "propose", "--from-doc", docPath, "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx propose --from-doc: %v\noutput: %s", err, out)
	}

	if strings.Contains(string(fake.lastReq.Content), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("the adapter received the raw secret -- redaction-at-capture did not run before Draft: %s", fake.lastReq.Content)
	}
	if !strings.Contains(string(fake.lastReq.Content), "[REDACTED:aws-access-key-id]") {
		t.Errorf("adapter content = %q, want a [REDACTED:aws-access-key-id] placeholder", fake.lastReq.Content)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "aws-access-key-id") {
		t.Errorf("expected a warning naming the redacted pattern, got:\n%s", out)
	}

	// The document source's own content_hash must still cover the RAW,
	// unredacted file -- docs/intent-provider.md's own "Secret material
	// in a doc" section: two deliberately distinct byte sequences, never
	// conflated.
	rawDoc, _ := os.ReadFile(docPath)
	wantHash := intentprovider.HashDocument(rawDoc)
	if !strings.Contains(out, wantHash) {
		t.Errorf("output missing the document source's own content_hash (%s) computed over the RAW file:\n%s", wantHash, out)
	}
}

func TestProposeFromDoc_AdapterErrorSurfacesLoudly(t *testing.T) {
	withBuildIntentAdapter(t, &fakeIntentAdapter{err: errAdapterUnavailable})

	docPath := filepath.Join(t.TempDir(), "payments.md")
	writeFile(t, docPath, "doc")

	_, err := runUbx(t, nil, "propose", "--from-doc", docPath, "--stack", "payments")
	if err == nil {
		t.Fatal("expected an error when the adapter itself fails")
	}
	if !strings.Contains(err.Error(), "adapter unavailable") {
		t.Errorf("error = %v, want it to surface the underlying adapter error", err)
	}
}

var errAdapterUnavailable = &staticError{"adapter unavailable"}

type staticError struct{ msg string }

func (e *staticError) Error() string { return e.msg }
