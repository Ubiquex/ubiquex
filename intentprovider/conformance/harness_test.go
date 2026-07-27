package conformance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ubiquex/ubiquex-cli/intentprovider"
)

// fakeAdapter is a hermetic, fully deterministic fake -- no network, no
// real LLM. Proves the harness itself (Fixtures, Doc, Run) works
// end-to-end without depending on whether any real adapter is correct,
// per docs/intent-provider.md's own "Conformance suite" section: "a fake
// Adapter proves this harness itself works hermetically."
type fakeAdapter struct{ draft string }

func (f *fakeAdapter) Name() string { return "fake" }

func (f *fakeAdapter) Draft(context.Context, intentprovider.DraftRequest) (json.RawMessage, error) {
	return json.RawMessage(f.draft), nil
}

var _ intentprovider.Adapter = (*fakeAdapter)(nil)

const goodPaymentsDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {
    "summary": "provision a database for payments, modeled on staging, smaller",
    "assumptions": [
      {"text": "chose db.t3.small, one step down from staging's db.t3.medium", "affects": ["aws_db_instance.payments-db.instance_class"]}
    ],
    "defaults": [],
    "questions": []
  },
  "resources": [
    {"type": "aws_db_instance", "name": "payments-db", "op": "create", "config": "{\"instance_class\":\"db.t3.small\"}"}
  ],
  "destroys": []
}`

func TestRun_FakeAdapterPasses(t *testing.T) {
	Run(t, &fakeAdapter{draft: goodPaymentsDraft})
}

// TestFixtures_DocFilesEmbedAndParse confirms every fixture's own doc
// file is actually embedded and readable -- a missing/misnamed embed
// path fails loudly here rather than surfacing as a confusing "file does
// not exist" only inside a specific t.Run.
func TestFixtures_DocFilesEmbedAndParse(t *testing.T) {
	if len(Fixtures) == 0 {
		t.Fatal("Fixtures is empty -- fixture #1 (the payments doc) must always be present")
	}
	for _, f := range Fixtures {
		doc := f.Doc(t)
		if len(doc) == 0 {
			t.Errorf("fixture %s: embedded doc is empty", f.Name)
		}
	}
}
