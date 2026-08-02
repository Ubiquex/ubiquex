package intentprovider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// scriptedAdapter is a hermetic, fully deterministic fake implementing
// Adapter directly -- no network, no real LLM anywhere in this test file.
// Responses is consumed one per call to Draft; each entry is either a raw
// JSON response or an error, never both. Calls records every DraftRequest
// this fake actually received, so tests can assert on exactly what
// DraftWithRetry fed back on a retry (docs/intent-provider-adversarial.md
// row 7's own required outcome: "each retry call receives the exact
// prior attempt's own rejected output and validation errors... confirmed
// directly against the fake adapter's own recorded call history, not
// assumed").
type scriptedAdapter struct {
	responses []scriptedResponse
	calls     []DraftRequest
}

type scriptedResponse struct {
	raw json.RawMessage
	err error
}

func (a *scriptedAdapter) Name() string { return "scripted" }

func (a *scriptedAdapter) Model() string { return "scripted-model" }

func (a *scriptedAdapter) Draft(_ context.Context, req DraftRequest) (json.RawMessage, error) {
	a.calls = append(a.calls, req)
	if len(a.calls) > len(a.responses) {
		panic("scriptedAdapter: more Draft calls than scripted responses")
	}
	r := a.responses[len(a.calls)-1]
	return r.raw, r.err
}

const validPaymentsDraft = `{
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

func TestDraftWithRetry_SucceedsFirstAttempt(t *testing.T) {
	a := &scriptedAdapter{responses: []scriptedResponse{
		{raw: json.RawMessage(validPaymentsDraft)},
	}}

	draft, raw, err := DraftWithRetry(context.Background(), a, "payments", []byte("some doc"), nil)
	if err != nil {
		t.Fatalf("DraftWithRetry: %v", err)
	}
	if draft.Stack != "payments" {
		t.Errorf("Stack = %q, want %q", draft.Stack, "payments")
	}
	if len(draft.Resources) != 1 || draft.Resources[0].Type != "aws_db_instance" {
		t.Errorf("Resources = %+v, want one aws_db_instance", draft.Resources)
	}
	if len(draft.Intent.Assumptions) != 1 {
		t.Errorf("Assumptions = %+v, want exactly one entry", draft.Intent.Assumptions)
	}
	if raw == nil {
		t.Error("raw output must not be nil on success")
	}
	if len(a.calls) != 1 {
		t.Fatalf("adapter called %d times, want 1", len(a.calls))
	}
	if a.calls[0].Attempt != 1 {
		t.Errorf("first call Attempt = %d, want 1", a.calls[0].Attempt)
	}
	if a.calls[0].PriorOutput != nil || a.calls[0].PriorErrors != nil {
		t.Errorf("first call must carry no prior-attempt content, got PriorOutput=%q PriorErrors=%v", a.calls[0].PriorOutput, a.calls[0].PriorErrors)
	}
}

// TestDraftWithRetry_RecoversOnSecondAttempt is
// docs/intent-provider-adversarial.md row 7's own fourth sub-case: attempt
// 1 fails, attempt 2 succeeds -- proving the retry path is not merely
// decorative.
func TestDraftWithRetry_RecoversOnSecondAttempt(t *testing.T) {
	badFirst := json.RawMessage(`{"schema_version": 1, "kind": "ubx:intent/v1", "stack": "payments", "intent": {"summary": "", "assumptions": [], "defaults": [], "questions": []}, "resources": [], "destroys": []}`)
	a := &scriptedAdapter{responses: []scriptedResponse{
		{raw: badFirst}, // empty summary -- semantically invalid
		{raw: json.RawMessage(validPaymentsDraft)},
	}}

	draft, _, err := DraftWithRetry(context.Background(), a, "payments", []byte("some doc"), nil)
	if err != nil {
		t.Fatalf("DraftWithRetry: %v", err)
	}
	if draft == nil {
		t.Fatal("expected a valid draft on recovery, got nil")
	}

	if len(a.calls) != 2 {
		t.Fatalf("adapter called %d times, want 2", len(a.calls))
	}
	second := a.calls[1]
	if second.Attempt != 2 {
		t.Errorf("second call Attempt = %d, want 2", second.Attempt)
	}
	if string(second.PriorOutput) != string(badFirst) {
		t.Errorf("second call PriorOutput = %s, want the exact first attempt's own rejected output %s", second.PriorOutput, badFirst)
	}
	if len(second.PriorErrors) == 0 {
		t.Error("second call PriorErrors must be non-empty -- the driver must feed back what was wrong")
	}
	found := false
	for _, e := range second.PriorErrors {
		if strings.Contains(e, "intent.summary") {
			found = true
		}
	}
	if !found {
		t.Errorf("PriorErrors = %v, want an error naming intent.summary", second.PriorErrors)
	}
}

// TestDraftWithRetry_HardFailsAfterThreeInvalidAttempts is
// docs/intent-provider-adversarial.md row 7's own primary required
// outcome: never a fourth silent retry, never a partial/best-effort
// acceptance, and the returned error names every one of the three
// attempts' own validation failures.
func TestDraftWithRetry_HardFailsAfterThreeInvalidAttempts(t *testing.T) {
	invalid := json.RawMessage(`{not valid json`)
	a := &scriptedAdapter{responses: []scriptedResponse{
		{raw: invalid}, {raw: invalid}, {raw: invalid},
	}}

	draft, raw, err := DraftWithRetry(context.Background(), a, "payments", []byte("some doc"), nil)
	if err == nil {
		t.Fatal("expected a hard failure after three invalid attempts, got nil error")
	}
	if draft != nil || raw != nil {
		t.Error("a failed DraftWithRetry must never return a draft or raw output")
	}
	if len(a.calls) != MaxAttempts {
		t.Fatalf("adapter called %d times, want exactly MaxAttempts=%d -- never a fourth silent retry", len(a.calls), MaxAttempts)
	}

	var draftErr *DraftError
	if !errors.As(err, &draftErr) {
		t.Fatalf("error is %T, want *DraftError", err)
	}
	if len(draftErr.Attempts) != MaxAttempts {
		t.Fatalf("DraftError names %d attempts, want %d", len(draftErr.Attempts), MaxAttempts)
	}
	for i, a := range draftErr.Attempts {
		if a.Attempt != i+1 {
			t.Errorf("Attempts[%d].Attempt = %d, want %d", i, a.Attempt, i+1)
		}
		if len(a.Errors) == 0 {
			t.Errorf("Attempts[%d].Errors is empty, want the invalid-JSON message", i)
		}
	}
}

// TestDraftWithRetry_AdapterErrorAbortsImmediately covers
// docs/intent-provider-adversarial.md row 6: a network/authentication
// failure from the adapter itself is never retried, distinct from a
// validation failure.
func TestDraftWithRetry_AdapterErrorAbortsImmediately(t *testing.T) {
	wantErr := errors.New("claude adapter: authentication failed")
	a := &scriptedAdapter{responses: []scriptedResponse{
		{err: wantErr},
		{raw: json.RawMessage(validPaymentsDraft)}, // must never be reached
	}}

	_, _, err := DraftWithRetry(context.Background(), a, "payments", []byte("some doc"), nil)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want it to wrap %v", err, wantErr)
	}
	if len(a.calls) != 1 {
		t.Errorf("adapter called %d times, want exactly 1 -- an adapter-level error must never be retried", len(a.calls))
	}
}

func TestDraftWithRetry_DuplicateResourceAddressRejected(t *testing.T) {
	dup := json.RawMessage(`{
    "schema_version": 1, "kind": "ubx:intent/v1", "stack": "payments",
    "intent": {"summary": "x", "assumptions": [], "defaults": [], "questions": []},
    "resources": [
      {"type": "aws_db_instance", "name": "payments-db", "op": "create", "config": "{}"},
      {"type": "aws_db_instance", "name": "payments-db", "op": "create", "config": "{}"}
    ],
    "destroys": []
  }`)
	a := &scriptedAdapter{responses: []scriptedResponse{{raw: dup}, {raw: dup}, {raw: dup}}}

	_, _, err := DraftWithRetry(context.Background(), a, "payments", []byte("doc"), nil)
	if err == nil {
		t.Fatal("expected a hard failure, got nil")
	}
	var draftErr *DraftError
	if !errors.As(err, &draftErr) {
		t.Fatalf("error is %T, want *DraftError", err)
	}
	if !strings.Contains(draftErr.Attempts[0].Errors[0], "duplicate resource address") {
		t.Errorf("Attempts[0].Errors = %v, want a duplicate-resource-address message", draftErr.Attempts[0].Errors)
	}
}
